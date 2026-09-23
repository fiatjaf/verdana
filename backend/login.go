package backend

import (
	"context"
	"errors"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/keyer"
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

// Login takes an nsec or a bunker:// URL, resolves the signer and moves the
// launcher to its main phase. Blocking: call it from a goroutine.
func Login(input string) {
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
	userKeyer = nil

	// The keyer outlives the handshake: a bunker signer listens for its
	// responses on a subscription tied to this ctx, so it stays open
	// until logout or the next login.
	sessionCtx, cancelSession := context.WithCancel(context.Background())
	sessionCancel = cancelSession

	clientKey := state.ClientKey

	// keyer.New blocks on the bunker's "connect" answer, so race it
	// against the login deadline instead of handing it a ctx that dies
	// on return (that would kill the response subscription too).
	type keyerResult struct {
		k   nostr.Keyer
		err error
	}
	keyerDone := make(chan keyerResult, 1)
	go func() {
		k, err := keyer.New(sessionCtx, sys.Pool, input, &keyer.SignerOptions{
			BunkerClientSecretKey: clientKey,
			BunkerAuthHandler: func(url string) {
				log.Info().Str("url", url).Msg("bunker auth")
			},
		})
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

	stateMu.Lock()
	if state.Login != input {
		state.Login = input
		saveState()
	}
	stateMu.Unlock()

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
