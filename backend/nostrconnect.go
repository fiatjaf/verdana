package backend

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"sync"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip04"
	"fiatjaf.com/nostr/nip44"
	"fiatjaf.com/nostr/nip46"
)

// The client-initiated half of NIP-46: while the login screen is up the
// launcher shows a nostrconnect:// uri (as a QR code) naming our client key,
// a relay and a one-time secret, and listens on that relay. A signer that
// scans it answers "connect" with the secret; its author is the remote
// signer, and from there it is an ordinary bunker login.
//
// fiatjaf.com/nostr has helpers for this (nip46.NewBunkerFromNostrConnect),
// but they hide the secret and the signer's pubkey — so the session can't be
// stored — and listen with "since": now, which bunkerSigner explains the
// trouble with. The waiting here reuses bunkerSigner's listener instead.

// defaultNostrConnectRelay is where the QR code points until the user picks
// another relay.
const defaultNostrConnectRelay = "wss://bucket.coracle.social"

// nostrConnectPerms are the requests the bridge sends a signer on a napp's
// behalf, asked for up front so the signer can grant them in one go.
var nostrConnectPerms = []string{
	"get_public_key",
	"sign_event",
	"nip44_encrypt",
	"nip44_decrypt",
	"nip04_encrypt",
	"nip04_decrypt",
}

// buildNostrConnectURI is the uri a signer scans to connect to clientPub.
func buildNostrConnectURI(clientPub nostr.PubKey, relays []string, secret string) string {
	q := url.Values{}
	for _, r := range relays {
		q.Add("relay", r)
	}
	q.Set("secret", secret)
	q.Set("perms", strings.Join(nostrConnectPerms, ","))
	q.Set("name", "Verdana")
	return "nostrconnect://" + clientPub.Hex() + "?" + q.Encode()
}

// isNostrConnectAnswer says whether evt is a signer accepting our
// nostrconnect uri: a response to clientKey carrying the uri's secret.
func isNostrConnectAnswer(clientKey nostr.SecretKey, evt nostr.Event, secret string) bool {
	if evt.Kind != nostr.KindNostrConnect || evt.PubKey == clientKey.Public() {
		return false
	}
	// as with bunkerSigner, some signers still answer in NIP-04
	plain := ""
	if conv44, err := nip44.GenerateConversationKey(evt.PubKey, clientKey); err == nil {
		plain, err = nip44.Decrypt(evt.Content, conv44)
		if err != nil {
			plain = ""
		}
	}
	if plain == "" {
		conv04, err := nip04.ComputeSharedSecret(evt.PubKey, clientKey)
		if err != nil {
			return false
		}
		if plain, err = nip04.Decrypt(evt.Content, conv04); err != nil {
			return false
		}
	}
	var resp nip46.Response
	if err := json.Unmarshal([]byte(plain), &resp); err != nil {
		return false
	}
	return resp.Error == "" && resp.Result == secret
}

// waitNostrConnect listens on relays until a signer answers the
// nostrconnect uri with secret, and returns that signer's pubkey.
func waitNostrConnect(ctx context.Context, pool *nostr.Pool, clientKey nostr.SecretKey, relays []string, secret string) (nostr.PubKey, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	found := make(chan nostr.PubKey, 1)
	onEvent := func(evt nostr.Event) {
		if isNostrConnectAnswer(clientKey, evt, secret) {
			select {
			case found <- evt.PubKey:
			default:
			}
		}
	}
	for _, r := range relays {
		go listenNostrConnect(ctx, pool, nostr.NormalizeURL(r), clientKey.Public(), onEvent, func() {})
	}

	select {
	case pk := <-found:
		return pk, nil
	case <-ctx.Done():
		return nostr.ZeroPK, context.Cause(ctx)
	}
}

// nostrConnectBunkerURL is how a nostrconnect login is stored: the bunker
// url of the signer that answered, without a secret. The signer knows our
// client key from then on, so resuming never sends "connect" again.
func nostrConnectBunkerURL(signer nostr.PubKey, relays []string) string {
	q := url.Values{}
	for _, r := range relays {
		q.Add("relay", r)
	}
	return "bunker://" + signer.Hex() + "?" + q.Encode()
}

// nc is the nostrconnect uri on offer while the launcher is on its login
// screen. It is started and stopped with the phase (see setPhaseLocked), so
// its mutex nests inside ls.mu.
var nc struct {
	mu     sync.Mutex
	uri    string
	cancel context.CancelFunc
}

// startNostrConnectLocked offers a fresh nostrconnect uri — new secret, the
// stored relay — and waits for a signer to answer it in the background,
// replacing whatever uri was on offer. ls.mu must be held.
func startNostrConnectLocked() {
	stateMu.Lock()
	clientKey := state.ClientKey
	relay := state.NostrConnectRelay
	stateMu.Unlock()
	if relay == "" {
		relay = defaultNostrConnectRelay
	}
	relays := []string{relay}

	var raw [16]byte
	rand.Read(raw[:])
	secret := hex.EncodeToString(raw[:])

	ctx, cancel := context.WithCancel(context.Background())
	nc.mu.Lock()
	if nc.cancel != nil {
		nc.cancel()
	}
	nc.uri = buildNostrConnectURI(clientKey.Public(), relays, secret)
	nc.cancel = cancel
	nc.mu.Unlock()

	if sys == nil {
		return // not started: nothing to listen with
	}
	pool := sys.Pool

	log.Debug().Str("relay", relay).Msg("waiting for a nostrconnect signer")
	go func() {
		signer, err := waitNostrConnect(ctx, pool, clientKey, relays, secret)
		if err != nil {
			return // replaced, or the login screen went away
		}

		// only the uri still on offer gets to log in: a manual login or a
		// relay change in the meantime cancelled this one
		nc.mu.Lock()
		if ctx.Err() != nil {
			nc.mu.Unlock()
			return
		}
		nc.uri = ""
		nc.cancel = nil
		nc.mu.Unlock()
		cancel()

		log.Info().Str("signer", signer.Hex()).Msg("nostrconnect signer answered")
		login(nostrConnectBunkerURL(signer, relays), true)
	}()
}

// stopNostrConnect withdraws the nostrconnect uri on offer, if any.
func stopNostrConnect() {
	nc.mu.Lock()
	if nc.cancel != nil {
		nc.cancel()
		nc.cancel = nil
	}
	nc.uri = ""
	nc.mu.Unlock()
}

// nostrConnectURI is the nostrconnect uri on offer, or "".
func nostrConnectURI() string {
	nc.mu.Lock()
	defer nc.mu.Unlock()
	return nc.uri
}

// NostrConnectRelay is the relay the login screen's nostrconnect uri points
// signers to.
func NostrConnectRelay() string {
	stateMu.Lock()
	defer stateMu.Unlock()
	if state.NostrConnectRelay == "" {
		return defaultNostrConnectRelay
	}
	return state.NostrConnectRelay
}

// SetNostrConnectRelay stores the relay for nostrconnect logins and, on the
// login screen, offers a new uri on it.
func SetNostrConnectRelay(input string) {
	relay, err := cleanRelayURL(input)
	if err != nil {
		ls.mu.Lock()
		ls.loginErr = err.Error()
		ls.mu.Unlock()
		notifyState()
		return
	}

	stateMu.Lock()
	changed := state.NostrConnectRelay != relay
	state.NostrConnectRelay = relay
	if changed {
		saveState()
	}
	stateMu.Unlock()

	ls.mu.Lock()
	ls.loginErr = ""
	if changed && ls.phase == PhaseLogin {
		startNostrConnectLocked()
	}
	ls.mu.Unlock()
	notifyState()
}

// cleanRelayURL turns what was typed into a relay field into a ws(s) url.
func cleanRelayURL(input string) (string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", errors.New("no relay given")
	}
	if !strings.Contains(input, "://") {
		input = "wss://" + input
	}
	u, err := url.Parse(input)
	if err != nil || (u.Scheme != "wss" && u.Scheme != "ws") || u.Host == "" {
		return "", errors.New("not a relay url: " + strings.TrimSpace(input))
	}
	return nostr.NormalizeURL(input), nil
}
