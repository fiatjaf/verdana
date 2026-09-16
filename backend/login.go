package backend

import (
	"context"
	"strings"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/keyer"
)

var (
	userKeyer  nostr.Keyer
	userPubkey nostr.PubKey
)

// Login takes an nsec or a bunker:// URL, resolves the signer and moves the
// launcher to its main phase. Blocking: call it from a goroutine.
func Login(input string) {
	input = strings.TrimSpace(input)
	if input == "" {
		setLoginErr("no key or bunker url given")
		return
	}

	log.Info().Msg("starting login")
	setPhase(PhaseLoading)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	stateMu.Lock()
	clientKey := state.ClientKey
	stateMu.Unlock()

	k, err := keyer.New(ctx, sys.Pool, input, &keyer.SignerOptions{
		BunkerClientSecretKey: clientKey,
		BunkerAuthHandler:     func(url string) {},
	})
	if err != nil {
		log.Error().Err(err).Msg("login failed")
		setLoginErr(err.Error())
		return
	}

	pk, err := k.GetPublicKey(ctx)
	if err != nil {
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
	go Fetch()
}

// Logout forgets the signer and the stored login, and sends the launcher back
// to its login screen. Open napps are closed: they were talking to that key.
func Logout() {
	CloseAllWindows()

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

// LoggedIn says whether there is a signer to sign with.
func LoggedIn() bool { return userKeyer != nil }

// UserPubkey is the logged-in user's pubkey in hex, or "".
func UserPubkey() string {
	if userPubkey == nostr.ZeroPK {
		return ""
	}
	return userPubkey.Hex()
}
