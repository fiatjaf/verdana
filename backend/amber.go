package backend

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	mrand "math/rand"

	"fiatjaf.com/nostr"
	"github.com/puzpuzpuz/xsync/v3"
)

// NIP-55 support, the client side: a signer app on the phone (Amber and the
// like) holds the key, and every private-key operation goes out to it
// through the platform host as a request the user answers there.
//
// The keyer is part of the backend so the bridge and the napps never know
// the difference between an NIP-55 signer and any other one: only the Host
// can reach the phone, and only here does the answer come back.

// AmberSigner is a nostr.Keyer that outsources every private-key operation
// to the phone's signer app. Package is the signer's package name; blank is
// only usable before there is a key, so the request path refuses it.
type AmberSigner struct {
	PubKey  nostr.PubKey
	Package string
}

var _ nostr.Keyer = AmberSigner{}

func (a AmberSigner) GetPublicKey(context.Context) (nostr.PubKey, error) {
	return a.PubKey, nil
}

func (a AmberSigner) SignEvent(ctx context.Context, evt *nostr.Event) error {
	if evt.PubKey == (nostr.PubKey{}) {
		evt.PubKey = a.PubKey
	}
	evt.SetID()

	answer, err := a.request(ctx, "sign_event", evt.String(), "")
	if err != nil {
		return err
	}
	if strings.HasPrefix(strings.TrimSpace(answer), "{") {
		var signed nostr.Event
		if err := signed.UnmarshalJSON([]byte(answer)); err != nil {
			return fmt.Errorf("the signer gave an unreadable event: %w", err)
		}
		if signed.ID != evt.ID {
			return errors.New("the signer answered with a different event")
		}
		evt.Sig = signed.Sig
		return nil
	}
	// the background (content-provider) path answers with a bare signature
	sig, err := sigFromHex(answer)
	if err != nil {
		return fmt.Errorf("the signer gave an unreadable signature: %w", err)
	}
	if !evt.CheckID() {
		return errors.New("the signer answered with a wrong event id")
	}
	evt.Sig = sig
	return nil
}

func (a AmberSigner) Encrypt(ctx context.Context, plaintext string, recipient nostr.PubKey) (string, error) {
	return a.cipher(ctx, "nip44_encrypt", plaintext, recipient)
}

func (a AmberSigner) Decrypt(ctx context.Context, ciphertext string, sender nostr.PubKey) (string, error) {
	return a.cipher(ctx, "nip44_decrypt", ciphertext, sender)
}

func (a AmberSigner) Nip04Encrypt(ctx context.Context, plaintext string, recipient nostr.PubKey) (string, error) {
	return a.cipher(ctx, "nip04_encrypt", plaintext, recipient)
}

func (a AmberSigner) Nip04Decrypt(ctx context.Context, ciphertext string, sender nostr.PubKey) (string, error) {
	return a.cipher(ctx, "nip04_decrypt", ciphertext, sender)
}

// cipher is the shared body of the four encrypt/decrypt cases: the payload
// rides in the request, the counterparty pubkey in the op.
func (a AmberSigner) cipher(ctx context.Context, op string, payload string, counterpart nostr.PubKey) (string, error) {
	if payload == "" {
		return "", nil
	}
	return a.request(ctx, op, payload, counterpart.Hex())
}

// request hands a NIP-55 operation to the host and blocks until either the
// answer comes back (AnswerAmber) or the ctx runs out. The answer is
// whatever the operation produces: an event JSON, a bare signature, a
// ciphertext, a plaintext.
func (a AmberSigner) request(ctx context.Context, op string, payload string, counterpart string) (string, error) {
	if a.Package == "" {
		return "", errors.New("no NIP-55 signer is configured")
	}
	id := shortID()
	ch := make(chan string, 1)
	amberWaiters.Store(id, ch)
	defer amberWaiters.Delete(id)

	if !host.AmberRequest(id, op, payload, a.PubKey.Hex(), counterpart, a.Package) {
		return "", fmt.Errorf("the NIP-55 signer could not be reached for %s", op)
	}

	select {
	case answer := <-ch:
		return answer, nil
	case <-ctx.Done():
		return "", fmt.Errorf("the NIP-55 signer did not answer for %s: %w", op, ctx.Err())
	}
}

func sigFromHex(s string) ([64]byte, error) {
	var sig [64]byte
	b, err := hex.DecodeString(strings.TrimSpace(s))
	if err != nil || len(b) != 64 {
		return sig, errors.New("not a 64-byte hex signature")
	}
	copy(sig[:], b)
	return sig, nil
}

// shortID is the per-request token the answer comes back under.
func shortID() string {
	var b [8]byte
	mrand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// amberWaiters holds the pipes the answers come back through, keyed by the
// request id the Android side carries with the request.
var amberWaiters = xsync.NewMapOf[string, chan string]()

// AnswerAmber files a signer's answer in for the request with that id.
// Late answers to abandoned requests are dropped here.
func AnswerAmber(id string, answer string, ok bool) {
	ch, ok2 := amberWaiters.Load(id)
	if !ok2 {
		return
	}
	if answer == "" {
		answer = ""
	}
	select {
	case ch <- answer:
	default:
		close(ch) // signal: the id was answered... twice?
	}
}
