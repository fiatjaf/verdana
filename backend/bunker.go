package backend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip04"
	"fiatjaf.com/nostr/nip44"
	"fiatjaf.com/nostr/nip46"
)

// bunkerSigner is a NIP-46 client: a nostr.Keyer whose key lives in a
// remote signer reached over relays.
//
// It stands in for nip46.BunkerClient, which listens for responses with
// "since": now. A relay applies since to live events too, so when the
// signer's clock is a few seconds behind ours every response it sends
// right away is created "before" our subscription and never delivered: the
// signer shows the request, we time out. The pool's reconnect logic resets
// since to now as well. With "limit": 0 the relay sends nothing stored
// anyway, so this client subscribes with no since at all and manages its
// relay subscriptions itself.
type bunkerSigner struct {
	pool      *nostr.Pool
	clientKey nostr.SecretKey
	clientPub nostr.PubKey
	target    nostr.PubKey
	relays    []string
	onAuth    func(string)

	conv44 [32]byte
	conv04 []byte

	idPrefix string
	serial   atomic.Uint64

	mu        sync.Mutex
	listeners map[string]chan nip46.Response
	pubkey    nostr.PubKey
}

var _ nostr.Keyer = (*bunkerSigner)(nil)

// bunkerReadyTimeout is how long a new client waits for one of the signer's
// relays to confirm its subscription before sending anything anyway.
const bunkerReadyTimeout = 5 * time.Second

// newBunkerSigner starts listening for the signer's responses on its
// relays and returns once one of them has the subscription open (or
// bunkerReadyTimeout passed). The subscriptions live as long as ctx.
func newBunkerSigner(ctx context.Context, pool *nostr.Pool, clientKey nostr.SecretKey, target nostr.PubKey, relays []string, onAuth func(string)) (*bunkerSigner, error) {
	if len(relays) == 0 {
		return nil, errors.New("the bunker url names no relays")
	}
	conv44, err := nip44.GenerateConversationKey(target, clientKey)
	if err != nil {
		return nil, fmt.Errorf("bunker key: %w", err)
	}
	conv04, err := nip04.ComputeSharedSecret(target, clientKey)
	if err != nil {
		return nil, fmt.Errorf("bunker key: %w", err)
	}
	if onAuth == nil {
		onAuth = func(string) {}
	}

	b := &bunkerSigner{
		pool:      pool,
		clientKey: clientKey,
		clientPub: clientKey.Public(),
		target:    target,
		relays:    relays,
		onAuth:    onAuth,
		conv44:    conv44,
		conv04:    conv04,
		idPrefix:  "verdana-" + strconv.Itoa(rand.Intn(1<<16)),
		listeners: make(map[string]chan nip46.Response),
	}

	ready := make(chan struct{})
	readyOnce := sync.OnceFunc(func() { close(ready) })
	for _, url := range relays {
		go listenNostrConnect(ctx, pool, nostr.NormalizeURL(url), b.clientPub, b.handle, readyOnce)
	}

	select {
	case <-ready:
	case <-time.After(bunkerReadyTimeout):
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
	return b, nil
}

// listenNostrConnect keeps a subscription for the NIP-46 events addressed to
// clientPub open on one relay until ctx ends, reconnecting with backoff when
// the relay drops it. ready is called on every EOSE.
func listenNostrConnect(ctx context.Context, pool *nostr.Pool, url string, clientPub nostr.PubKey, onEvent func(nostr.Event), ready func()) {
	filter := nostr.Filter{
		Kinds:     []nostr.Kind{nostr.KindNostrConnect},
		Tags:      nostr.TagMap{"p": []string{clientPub.Hex()}},
		LimitZero: true,
	}
	backoff := time.Second
	for ctx.Err() == nil {
		relay, err := pool.EnsureRelay(url)
		if err == nil {
			var sub *nostr.Subscription
			sub, err = relay.Subscribe(ctx, filter, nostr.SubscriptionOptions{Label: "verdana-bunker"})
			if err == nil {
				backoff = time.Second
				readNostrConnect(ctx, sub, onEvent, ready)
			}
		}
		if err != nil {
			log.Debug().Err(err).Str("relay", url).Msg("bunker relay unavailable")
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(2*time.Minute, backoff*2)
	}
}

// readNostrConnect handles one subscription's events until it ends.
func readNostrConnect(ctx context.Context, sub *nostr.Subscription, onEvent func(nostr.Event), ready func()) {
	defer sub.Unsub()
	for {
		select {
		case <-ctx.Done():
			return
		case <-sub.EndOfStoredEvents:
			ready()
		case reason := <-sub.ClosedReason:
			log.Debug().Str("relay", sub.Relay.URL).Str("reason", reason).Msg("bunker subscription closed")
			return
		case evt, ok := <-sub.Events:
			if !ok {
				return
			}
			onEvent(evt)
		}
	}
}

// handle routes a response to the request waiting for it.
func (b *bunkerSigner) handle(evt nostr.Event) {
	if evt.Kind != nostr.KindNostrConnect || evt.PubKey != b.target {
		return
	}
	// NIP-46 moved to NIP-44, but some signers still answer in NIP-04
	plain, err := nip44.Decrypt(evt.Content, b.conv44)
	if err != nil {
		if plain, err = nip04.Decrypt(evt.Content, b.conv04); err != nil {
			return
		}
	}
	var resp nip46.Response
	if err := json.Unmarshal([]byte(plain), &resp); err != nil {
		return
	}
	if resp.Result == "auth_url" {
		b.onAuth(resp.Error)
		return
	}

	b.mu.Lock()
	ch, ok := b.listeners[resp.ID]
	delete(b.listeners, resp.ID)
	b.mu.Unlock()
	if ok {
		ch <- resp // buffered: never blocks the subscription
	}
}

// rpc sends one request to the signer on all its relays and waits for the
// answer.
func (b *bunkerSigner) rpc(ctx context.Context, method string, params ...string) (string, error) {
	if params == nil {
		params = []string{}
	}
	id := b.idPrefix + "-" + strconv.FormatUint(b.serial.Add(1), 10)
	req, err := json.Marshal(nip46.Request{ID: id, Method: method, Params: params})
	if err != nil {
		return "", err
	}
	content, err := nip44.Encrypt(string(req), b.conv44)
	if err != nil {
		return "", fmt.Errorf("encrypting %s request: %w", method, err)
	}
	evt := nostr.Event{
		Kind:      nostr.KindNostrConnect,
		CreatedAt: nostr.Now(),
		Tags:      nostr.Tags{{"p", b.target.Hex()}},
		Content:   content,
	}
	if err := evt.Sign(b.clientKey); err != nil {
		return "", fmt.Errorf("signing %s request: %w", method, err)
	}

	ch := make(chan nip46.Response, 1)
	b.mu.Lock()
	b.listeners[id] = ch
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.listeners, id)
		b.mu.Unlock()
	}()

	// the answer is what counts, not the relays' OKs: some never send one
	// for ephemeral events, and the answer may well beat the OK back
	failed := make(chan error, len(b.relays))
	for _, url := range b.relays {
		go func() {
			relay, err := b.pool.EnsureRelay(url)
			if err == nil {
				err = relay.Publish(ctx, evt)
			}
			failed <- err
		}()
	}

	var errs []error
	for {
		select {
		case resp := <-ch:
			if resp.Error != "" {
				return "", fmt.Errorf("signer refused %s: %s", method, resp.Error)
			}
			return resp.Result, nil
		case err := <-failed:
			if err == nil {
				continue
			}
			errs = append(errs, err)
			if len(errs) == len(b.relays) {
				return "", fmt.Errorf("couldn't send %s to the signer: %w", method, errors.Join(errs...))
			}
		case <-ctx.Done():
			return "", errors.New("signer did not answer " + method + " (is your bunker online?)")
		}
	}
}

// connect introduces our client key to the signer with the bunker url's
// secret. Only needed once: the signer remembers the client key.
func (b *bunkerSigner) connect(ctx context.Context, secret string) error {
	_, err := b.rpc(ctx, "connect", b.target.Hex(), secret)
	return err
}

func (b *bunkerSigner) GetPublicKey(ctx context.Context) (nostr.PubKey, error) {
	b.mu.Lock()
	pk := b.pubkey
	b.mu.Unlock()
	if pk != nostr.ZeroPK {
		return pk, nil
	}
	res, err := b.rpc(ctx, "get_public_key")
	if err != nil {
		return nostr.ZeroPK, err
	}
	pk, err = nostr.PubKeyFromHex(res)
	if err != nil {
		return nostr.ZeroPK, fmt.Errorf("signer sent a bad pubkey: %w", err)
	}
	b.mu.Lock()
	b.pubkey = pk
	b.mu.Unlock()
	return pk, nil
}

func (b *bunkerSigner) SignEvent(ctx context.Context, evt *nostr.Event) error {
	res, err := b.rpc(ctx, "sign_event", evt.String())
	if err != nil {
		return err
	}
	var signed nostr.Event
	if err := json.Unmarshal([]byte(res), &signed); err != nil {
		return fmt.Errorf("signer sent an unreadable event: %w", err)
	}
	if !signed.CheckID() || !signed.VerifySignature() {
		return errors.New("signer sent an event with a bad id or signature")
	}
	*evt = signed
	return nil
}

func (b *bunkerSigner) Encrypt(ctx context.Context, plaintext string, recipient nostr.PubKey) (string, error) {
	return b.rpc(ctx, "nip44_encrypt", recipient.Hex(), plaintext)
}

func (b *bunkerSigner) Decrypt(ctx context.Context, ciphertext string, sender nostr.PubKey) (string, error) {
	return b.rpc(ctx, "nip44_decrypt", sender.Hex(), ciphertext)
}

func (b *bunkerSigner) Nip04Encrypt(ctx context.Context, plaintext string, recipient nostr.PubKey) (string, error) {
	return b.rpc(ctx, "nip04_encrypt", recipient.Hex(), plaintext)
}

func (b *bunkerSigner) Nip04Decrypt(ctx context.Context, ciphertext string, sender nostr.PubKey) (string, error) {
	return b.rpc(ctx, "nip04_decrypt", sender.Hex(), ciphertext)
}
