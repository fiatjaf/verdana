package backend

import (
	"context"
	"errors"
	"testing"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/keyer"
)

func TestBolt11Msats(t *testing.T) {
	for _, tc := range []struct {
		invoice string
		want    int64
		ok      bool
	}{
		// the BOLT-11 spec's examples
		{"lnbc2500u1pvjluezsp5zyg3zyg3zyg3zyg3zyg3zyg3zyg3zyg3zyg3zyg3zyg3zyg3zygs", 250_000_000, true},
		{"lnbc20m1pvjluezsp5zyg3zyg3zyg3zyg3zyg3zyg3zyg3zyg3zyg3zyg3zyg3zyg3zygs", 2_000_000_000, true},
		{"LNBC9678785340P1PWMNA7LPP5GC3XFM08U9QY06DJF8DFFLHUGL6P", 967_878_534, true},
		{"lightning:lnbc1u1pqqqqqq", 100_000, true},
		{"lntb10n1pqqqqqq", 1_000, true},
		{"lnbcrt5m1pqqqqqq", 500_000_000, true},
		{"lnbc11pqqqqqq", 100_000_000_000, true},
		// no amount, bad pico amount, bad multiplier, not an invoice
		{"lnbc1pvjluezpp5qqqsyqcyq5rqwzqfqqqsyqcyq5rqwzqfqqqsyqcyq5rqwzqfqypq", 0, false},
		{"lnbc15p1pqqqqqq", 0, false},
		{"lnbc10x1pqqqqqq", 0, false},
		{"bitcoin:bc1qxyz", 0, false},
		{"", 0, false},
	} {
		got, ok := bolt11Msats(tc.invoice)
		if got != tc.want || ok != tc.ok {
			t.Errorf("bolt11Msats(%q) = %d, %v; want %d, %v", tc.invoice, got, ok, tc.want, tc.ok)
		}
	}
}

func TestLnurlpURL(t *testing.T) {
	if got, ok := lnurlpURL("Alice@Wallet.example.com"); !ok || got != "https://wallet.example.com/.well-known/lnurlp/alice" {
		t.Fatalf("lnurlpURL = %q, %v", got, ok)
	}
	for _, bad := range []string{"", "alice", "@example.com", "alice@", "alice@evil.com/x", "a@b@c"} {
		if got, ok := lnurlpURL(bad); ok {
			t.Errorf("lnurlpURL(%q) = %q, want rejected", bad, got)
		}
	}
}

// zapFixture is a receipt from provider for a zap request sender → recipient.
func zapFixture(t *testing.T, provider, sender nostr.SecretKey, recipient nostr.PubKey, bolt11 string) nostr.Event {
	t.Helper()
	req := nostr.Event{
		Kind:      9734,
		CreatedAt: nostr.Now(),
		Content:   "great post",
		Tags:      nostr.Tags{{"p", recipient.Hex()}, {"amount", "1000"}, {"relays", "wss://relay.example.com"}},
	}
	if err := req.Sign(sender); err != nil {
		t.Fatal(err)
	}
	receipt := nostr.Event{
		Kind:      9735,
		CreatedAt: nostr.Now(),
		Tags: nostr.Tags{
			{"p", recipient.Hex()},
			{"e", "aa" + req.ID.Hex()[2:]},
			{"bolt11", bolt11},
			{"description", req.String()},
		},
	}
	if err := receipt.Sign(provider); err != nil {
		t.Fatal(err)
	}
	return receipt
}

func TestZapReceipt(t *testing.T) {
	provider, sender, user := nostr.Generate(), nostr.Generate(), nostr.Generate()

	evt := zapFixture(t, provider, sender, user.Public(), "lnbc210n1pqqqqqq")
	z, ok := zapReceipt(evt, provider.Public(), user.Public())
	if !ok {
		t.Fatal("valid receipt rejected")
	}
	// the invoice's amount wins over the request's; eventId is the receipt
	// even when it points at a zapped note
	if z["amount"] != int64(21_000) || z["sender"] != sender.Public().Hex() ||
		z["eventId"] != evt.ID.Hex() || z["content"] != "great post" {
		t.Fatalf("receipt = %v", z)
	}

	// no usable invoice amount: fall back to the request's
	evt = zapFixture(t, provider, sender, user.Public(), "lnbc1pqqqqqq")
	if z, _ := zapReceipt(evt, provider.Public(), user.Public()); z["amount"] != int64(1000) {
		t.Fatalf("fallback amount = %v", z["amount"])
	}

	// a receipt anyone else signed is a forgery
	forged := zapFixture(t, nostr.Generate(), sender, user.Public(), "lnbc210n1pqqqqqq")
	if _, ok := zapReceipt(forged, provider.Public(), user.Public()); ok {
		t.Fatal("receipt from the wrong provider accepted")
	}
	// a request for someone else
	other := zapFixture(t, provider, sender, nostr.Generate().Public(), "lnbc210n1pqqqqqq")
	if _, ok := zapReceipt(other, provider.Public(), user.Public()); ok {
		t.Fatal("receipt for another recipient accepted")
	}
	// a tampered request no longer verifies
	tampered := zapFixture(t, provider, sender, user.Public(), "lnbc210n1pqqqqqq")
	desc := tampered.Tags.Find("description")
	var req nostr.Event
	_ = req.UnmarshalJSON([]byte(desc[1]))
	req.Content = "forged"
	desc[1] = req.String()
	if _, ok := zapReceipt(tampered, provider.Public(), user.Public()); ok {
		t.Fatal("receipt with a tampered request accepted")
	}
}

func TestListKind(t *testing.T) {
	for in, want := range map[string]nostr.Kind{"bookmarks": 10003, " Interests ": 10015, "pins": 10001, "10030": 10030} {
		if got, ok := listKind(in); !ok || got != want {
			t.Errorf("listKind(%q) = %d, %v; want %d", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "nope", "30003", "1"} {
		if _, ok := listKind(bad); ok {
			t.Errorf("listKind(%q) accepted", bad)
		}
	}
}

func TestIdentityFetchPanicStillAnswers(t *testing.T) {
	v, err := identityFetch(context.Background(), nostr.ZeroPK, func(context.Context, nostr.PubKey) (any, error) {
		panic("boom")
	})
	got := identityResult("pubkeys", []string{}, v, err)
	if !errors.Is(err, errIdentityInternal) || got["error"] != "internal error" || len(got["pubkeys"].([]string)) != 0 {
		t.Fatalf("result = %v", got)
	}
	if got := identityResult("profile", nil, nil, nil); len(got) != 1 || got["profile"] != nil {
		t.Fatalf("null profile result = %v", got)
	}
}

func signOut(t *testing.T) {
	t.Helper()
	prevKeyer, prevPK := userKeyer, userPubkey
	userKeyer, userPubkey = nil, nostr.ZeroPK
	t.Cleanup(func() { userKeyer, userPubkey = prevKeyer, prevPK })
}

func TestIdentitySignedOut(t *testing.T) {
	setupNapTest(t)
	signOut(t)
	ci, rec := openNapplet(t, "identity-signed-out")
	ready(t, ci, rec, 1)

	post(t, ci, map[string]any{"type": "identity.getPublicKey", "id": "pk"})
	if got := rec.wait(t, "identity.getPublicKey.result", 1); got["pubkey"] != "" || got["id"] != "pk" {
		t.Fatalf("getPublicKey = %v", got)
	} else if _, has := got["error"]; has {
		t.Fatalf("getPublicKey carries an error: %v", got)
	}

	post(t, ci, map[string]any{"type": "identity.getProfile", "id": "profile"})
	if got := rec.wait(t, "identity.getProfile.result", 1); got["profile"] != nil || got["error"] != nil {
		t.Fatalf("getProfile = %v", got)
	} else if _, has := got["profile"]; !has {
		t.Fatalf("getProfile has no profile field: %v", got)
	}

	post(t, ci, map[string]any{"type": "identity.getList", "id": "list", "listType": "bookmarks"})
	if got := rec.wait(t, "identity.getList.result", 1); got["error"] != nil || len(got["entries"].([]any)) != 0 {
		t.Fatalf("getList = %v", got)
	}

	// an unknown list type is an error whether or not anyone is signed in
	post(t, ci, map[string]any{"type": "identity.getList", "id": "bad", "listType": "nope"})
	if got := rec.wait(t, "identity.getList.result", 2); got["error"] != "unsupported list type" || got["id"] != "bad" {
		t.Fatalf("unknown getList = %v", got)
	}
}

func TestPushIdentityChanged(t *testing.T) {
	setupNapTest(t)
	signOut(t)
	ci, rec := openNapplet(t, "identity-changed")
	ready(t, ci, rec, 1)

	sk := nostr.Generate()
	userKeyer, userPubkey = keyer.NewPlainKeySigner(sk), sk.Public()
	pushIdentityChanged()
	if got := rec.wait(t, "identity.changed", 1); got["pubkey"] != sk.Public().Hex() {
		t.Fatalf("signed-in push = %v", got)
	} else if _, has := got["id"]; has {
		t.Fatalf("identity.changed carries an id: %v", got)
	}

	// what a new login does to the old key before it tries the new one
	userKeyer = nil
	pushIdentityChanged()
	if got := rec.wait(t, "identity.changed", 2); got["pubkey"] != "" {
		t.Fatalf("cleared push = %v", got)
	}
}
