package backend

import (
	"context"
	"errors"
	"strings"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/keyer"
	"fiatjaf.com/nostr/nip05"
	"fiatjaf.com/nostr/nip46"
)

var (
	userKeyer  nostr.Keyer
	userPubkey nostr.PubKey

	// sessionCancel ends the current login session. A bunker signer's
	// response subscription is bound to the ctx the keyer was created
	// with, so that ctx must live until logout — killing it earlier
	// (e.g. with the login handshake's timeout ctx) makes every later
	// sign/encrypt fail with "context canceled".
	sessionCancel context.CancelFunc
)

// loginAmber finishes logging in through a NIP-55 signer app: the Android
// side already got the key's pubkey and the signer's package from the app
// itself, so this only wire it up and save it for the next launches. The
// input is "amber:<pkg>:<pubkeyhex>" — pkg is part of what is stored so a
// restart resumes with the same signer.
func loginAmber(input string) {
	rest := strings.TrimPrefix(input, "amber:")
	pkg, pkHex, found := strings.Cut(rest, ":")
	pk, err := nostr.PubKeyFromHex(pkHex)
	if !found || err != nil {
		setLoginErr("unreadable NIP-55 signer login: " + input)
		return
	}
	log.Info().Str("pkg", pkg).Str("pubkey", pk.Hex()).Msg("logging in through a NIP-55 signer")

	// no handshake to wait on: the signer app is the session, and it holds
	// the key whether we are online or not
	userKeyer = AmberSigner{PubKey: pk, Package: pkg}
	userPubkey = pk
	sessionCancel = nil
	go pushIdentityChanged()

	stateMu.Lock()
	if state.Login != input {
		state.Login = input
		saveState()
	}
	stateMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	setProfileFromUser(ctx, pk)
}

// Login takes an nsec, a bunker:// URL or an "amber:<pkg>:<pubkeyhex>" NIP-55
// signer url, resolves the signer and moves the launcher to its main phase.
// Blocking: call it from a goroutine.
func Login(input string) { login(input, false) }

// resumeLogin is Login for the input stored by a previous run.
func resumeLogin(input string) { login(input, true) }

func login(input string, resume bool) {
	if strings.HasPrefix(input, "amber:") {
		loginAmber(input)
		return
	}

	if input == "" {
		setLoginErr("no key or bunker url given")
		return
	}

	log.Info().Msg("starting login")
	setPhase(PhaseLoading)

	// A new login ends any previous session first.
	if sessionCancel != nil {
		sessionCancel()
		sessionCancel = nil
	}
	if userKeyer != nil {
		// the old identity is gone even if this login fails, so napplets
		// hear "" now rather than keep a key we no longer hold. Synchronous,
		// so it can't land after the new key's push.
		userKeyer = nil
		pushIdentityChanged()
	}

	// The keyer outlives the handshake: a bunker signer listens for its
	// responses on a subscription tied to this ctx, so it stays open
	// until logout or the next login.
	sessionCtx, cancelSession := context.WithCancel(context.Background())
	sessionCancel = cancelSession

	clientKey := state.ClientKey

	// a fresh bunker login blocks on the bunker's "connect" answer, so race it
	// against the login deadline instead of handing it a ctx that dies
	// on return (that would kill the response subscription too).
	type keyerResult struct {
		k   nostr.Keyer
		err error
	}
	keyerDone := make(chan keyerResult, 1)
	onAuth := func(url string) {
		log.Info().Str("url", url).Msg("bunker auth")
	}
	go func() {
		if nip46.IsValidBunkerURL(input) || nip05.IsValidIdentifier(input) {
			k, err := loginBunker(sessionCtx, clientKey, input, resume, onAuth)
			keyerDone <- keyerResult{k, err}
			return
		}
		k, err := keyer.New(sessionCtx, sys.Pool, input, &keyer.SignerOptions{})
		keyerDone <- keyerResult{k, err}
	}()

	var k nostr.Keyer
	select {
	case res := <-keyerDone:
		if res.err != nil {
			cancelSession()
			sessionCancel = nil
			log.Error().Err(res.err).Msg("login failed")
			setLoginErr(res.err.Error())
			return
		}
		k = res.k
	case <-time.After(20 * time.Second):
		cancelSession()
		sessionCancel = nil
		log.Error().Msg("login timed out")
		setLoginErr("login timed out")
		return
	}

	ctx, cancel := context.WithTimeout(sessionCtx, 60*time.Second)
	defer cancel()

	pk, err := k.GetPublicKey(ctx)
	if err != nil {
		cancelSession()
		sessionCancel = nil
		log.Error().Err(err).Msg("get public key failed")
		setLoginErr(err.Error())
		return
	}

	userKeyer = k
	userPubkey = pk
	go pushIdentityChanged()

	stateMu.Lock()
	if state.Login != input {
		state.Login = input
		saveState()
	}
	stateMu.Unlock()

	setProfileFromUser(ctx, pk)
}

// loginBunker reaches a NIP-46 signer from a bunker:// url or a NIP-05
// address. The client key is persisted, so on resume the bunker already
// knows us: NIP-46 only wants "connect" once, and its secret is single-use,
// so replaying it on every launch makes a signer like Amber prompt for a new
// connection (or ignore it) while the login times out. The first RPC
// (get_public_key) then tells whether the bunker still knows us.
func loginBunker(ctx context.Context, clientKey nostr.SecretKey, input string, resume bool, onAuth func(string)) (nostr.Keyer, error) {
	parsed, err := nip46.ParseBunkerInput(ctx, input)
	if err != nil {
		return nil, err
	}
	b, err := newBunkerSigner(ctx, sys.Pool, clientKey, parsed.HostPubKey, parsed.Relays, onAuth)
	if err != nil {
		return nil, err
	}
	if !resume {
		if err := b.connect(ctx, parsed.Secret); err != nil {
			return nil, err
		}
	}
	return b, nil
}

// setProfileFromUser is the tail every login shares: fetch that key's
// profile metadata, put it on the launcher state and go discover napps.
func setProfileFromUser(ctx context.Context, pk nostr.PubKey) {
	pm := sys.FetchProfileMetadata(ctx, pk)
	name := pm.Name
	if name == "" {
		name = pm.DisplayName
	}
	if name == "" {
		name = pk.Hex()
	}
	setProfile(pk.Hex(), name, pm.Picture)

	log.Info().Str("pubkey", pk.Hex()).Str("name", name).Msg("login successful")
	go Discover()
}

// Logout forgets the signer and the stored login, and sends the launcher back
// to its login screen. Open napps are closed: they were talking to that key.
func Logout() {
	CloseAllWindows()

	if sessionCancel != nil {
		sessionCancel()
		sessionCancel = nil
	}
	userKeyer = nil
	userPubkey = nostr.PubKey{}
	pushIdentityChanged()

	stateMu.Lock()
	state.Login = ""
	saveState()
	stateMu.Unlock()

	setProfile("", "", "")
	setPhase(PhaseLogin)
	log.Info().Msg("logged out")
}

// keyerErr translates signer errors into something a napp (and the logs)
// can act on. The NIP-46 client reports a bunker that never answered as a
// bare "context canceled", which looks like we gave up locally.
func keyerErr(err error) error {
	if err != nil && err.Error() == "context canceled" {
		return errors.New("signer did not answer (is your bunker online?)")
	}
	return err
}

// LoggedIn says whether there is a signer to sign with.
func LoggedIn() bool { return userKeyer != nil }

// UserPubkey is the logged-in user's pubkey in hex, or "".
func UserPubkey() string {
	if userPubkey == nostr.ZeroPK {
		return ""
	}
	return userPubkey.Hex()
}
