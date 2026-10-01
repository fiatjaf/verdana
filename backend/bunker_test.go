package backend

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/khatru"
	"fiatjaf.com/nostr/nip46"
)

// runTestSigner answers NIP-46 requests on relay with signerKey, stamping
// each response skew off the current time (a signer whose clock is behind
// ours has a negative skew).
func runTestSigner(t *testing.T, ctx context.Context, relay string, signerKey nostr.SecretKey, skew time.Duration) {
	t.Helper()
	signer := nip46.NewStaticKeySigner(signerKey)
	pool := nostr.NewPool()
	requests := pool.SubscribeMany(ctx, []string{relay}, nostr.Filter{
		Kinds: []nostr.Kind{nostr.KindNostrConnect},
		Tags:  nostr.TagMap{"p": []string{signerKey.Public().Hex()}},
		Since: nostr.Now() - 5,
	}, nostr.SubscriptionOptions{})
	go func() {
		for ie := range requests {
			_, _, out, err := signer.HandleRequest(ctx, ie.Event)
			if err != nil {
				continue
			}
			out.CreatedAt = nostr.Timestamp(time.Now().Add(skew).Unix())
			if err := out.Sign(signerKey); err != nil {
				continue
			}
			r, err := pool.EnsureRelay(relay)
			if err == nil {
				r.Publish(ctx, out)
			}
		}
	}()
	time.Sleep(200 * time.Millisecond) // let the signer's subscription open
}

func TestBunkerSignerToleratesSignerClockBehind(t *testing.T) {
	srv := httptest.NewServer(khatru.NewRelay())
	defer srv.Close()
	relay := "ws" + strings.TrimPrefix(srv.URL, "http")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	signerKey := nostr.Generate()
	runTestSigner(t, ctx, relay, signerKey, -30*time.Second)

	b, err := newBunkerSigner(ctx, nostr.NewPool(), nostr.Generate(), signerKey.Public(), []string{relay}, nil)
	if err != nil {
		t.Fatal(err)
	}

	rctx, rcancel := context.WithTimeout(ctx, 5*time.Second)
	defer rcancel()
	if err := b.connect(rctx, ""); err != nil {
		t.Fatalf("connect: %v", err)
	}
	pk, err := b.GetPublicKey(rctx)
	if err != nil {
		t.Fatalf("get_public_key: %v", err)
	}
	if pk != signerKey.Public() {
		t.Fatalf("get_public_key = %s, want %s", pk.Hex(), signerKey.Public().Hex())
	}

	evt := nostr.Event{Kind: 1, Content: "hi", CreatedAt: nostr.Now()}
	if err := b.SignEvent(rctx, &evt); err != nil {
		t.Fatalf("sign_event: %v", err)
	}
	if evt.PubKey != pk || !evt.VerifySignature() {
		t.Fatalf("sign_event returned a badly signed event: %+v", evt)
	}
}

func TestBunkerSignerFailsWithoutSigner(t *testing.T) {
	srv := httptest.NewServer(khatru.NewRelay())
	defer srv.Close()
	relay := "ws" + strings.TrimPrefix(srv.URL, "http")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	b, err := newBunkerSigner(ctx, nostr.NewPool(), nostr.Generate(), nostr.Generate().Public(), []string{relay}, nil)
	if err != nil {
		t.Fatal(err)
	}
	rctx, rcancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer rcancel()
	// khatru refuses an ephemeral event nobody listens for, others just
	// let the request go unanswered: either way it must end, not hang
	if _, err := b.GetPublicKey(rctx); err == nil {
		t.Fatal("get_public_key with no signer succeeded")
	}
}
