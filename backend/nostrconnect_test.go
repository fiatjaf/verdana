package backend

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/khatru"
	"fiatjaf.com/nostr/nip04"
	"fiatjaf.com/nostr/nip44"
	"fiatjaf.com/nostr/nip46"
)

// connectAnswer is the event a signer sends to accept a nostrconnect uri.
func connectAnswer(t *testing.T, signerKey nostr.SecretKey, client nostr.PubKey, result string, useNip04 bool) nostr.Event {
	t.Helper()
	plain, _ := json.Marshal(nip46.Response{ID: "x", Result: result})
	var content string
	if useNip04 {
		shared, err := nip04.ComputeSharedSecret(client, signerKey)
		if err != nil {
			t.Fatal(err)
		}
		if content, err = nip04.Encrypt(string(plain), shared); err != nil {
			t.Fatal(err)
		}
	} else {
		conv, err := nip44.GenerateConversationKey(client, signerKey)
		if err != nil {
			t.Fatal(err)
		}
		if content, err = nip44.Encrypt(string(plain), conv); err != nil {
			t.Fatal(err)
		}
	}
	evt := nostr.Event{
		Kind:      nostr.KindNostrConnect,
		CreatedAt: nostr.Now(),
		Tags:      nostr.Tags{{"p", client.Hex()}},
		Content:   content,
	}
	if err := evt.Sign(signerKey); err != nil {
		t.Fatal(err)
	}
	return evt
}

func TestBuildNostrConnectURIRoundTrip(t *testing.T) {
	client := nostr.Generate().Public()
	uri := buildNostrConnectURI(client, []string{"wss://bucket.coracle.social"}, "s3cret")

	u, err := url.Parse(uri)
	if err != nil {
		t.Fatal(err)
	}
	if u.Scheme != "nostrconnect" || u.Host != client.Hex() {
		t.Fatalf("uri %q doesn't name the client key", uri)
	}
	q := u.Query()
	if got := q["relay"]; len(got) != 1 || got[0] != "wss://bucket.coracle.social" {
		t.Fatalf("relay = %v", got)
	}
	if q.Get("secret") != "s3cret" {
		t.Fatalf("secret = %q", q.Get("secret"))
	}
	if q.Get("name") != "Verdana" {
		t.Fatalf("name = %q", q.Get("name"))
	}
	if !strings.Contains(q.Get("perms"), "sign_event") {
		t.Fatalf("perms = %q", q.Get("perms"))
	}
}

func TestIsNostrConnectAnswer(t *testing.T) {
	clientKey, signerKey := nostr.Generate(), nostr.Generate()
	client := clientKey.Public()

	if !isNostrConnectAnswer(clientKey, connectAnswer(t, signerKey, client, "s3cret", false), "s3cret") {
		t.Error("NIP-44 answer with the secret was rejected")
	}
	if !isNostrConnectAnswer(clientKey, connectAnswer(t, signerKey, client, "s3cret", true), "s3cret") {
		t.Error("NIP-04 answer with the secret was rejected")
	}
	if isNostrConnectAnswer(clientKey, connectAnswer(t, signerKey, client, "wrong", false), "s3cret") {
		t.Error("answer with the wrong secret was accepted")
	}
	// "ack" is what signers answer a bunker:// connect with; taking it here
	// would let anyone who saw an earlier uri (same client key) log us in
	if isNostrConnectAnswer(clientKey, connectAnswer(t, signerKey, client, "ack", false), "s3cret") {
		t.Error("plain ack was accepted")
	}
	if isNostrConnectAnswer(clientKey, connectAnswer(t, clientKey, client, "s3cret", false), "s3cret") {
		t.Error("our own event was taken for a signer's")
	}
}

func TestWaitNostrConnectFindsSigner(t *testing.T) {
	srv := httptest.NewServer(khatru.NewRelay())
	defer srv.Close()
	relay := "ws" + strings.TrimPrefix(srv.URL, "http")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	clientKey, signerKey, otherKey := nostr.Generate(), nostr.Generate(), nostr.Generate()
	client := clientKey.Public()

	type result struct {
		pk  nostr.PubKey
		err error
	}
	done := make(chan result, 1)
	go func() {
		pk, err := waitNostrConnect(ctx, nostr.NewPool(), clientKey, []string{relay}, "s3cret")
		done <- result{pk, err}
	}()
	time.Sleep(200 * time.Millisecond) // let the subscription open

	pool := nostr.NewPool()
	r, err := pool.EnsureRelay(relay)
	if err != nil {
		t.Fatal(err)
	}
	// a stale answer first, then the right one from a signer whose clock
	// is behind ours: no "since" must filter it out
	if err := r.Publish(ctx, connectAnswer(t, otherKey, client, "old", false)); err != nil {
		t.Fatal(err)
	}
	answer := connectAnswer(t, signerKey, client, "s3cret", false)
	answer.CreatedAt -= 30
	if err := answer.Sign(signerKey); err != nil {
		t.Fatal(err)
	}
	if err := r.Publish(ctx, answer); err != nil {
		t.Fatal(err)
	}

	res := <-done
	if res.err != nil {
		t.Fatalf("waitNostrConnect: %v", res.err)
	}
	if res.pk != signerKey.Public() {
		t.Fatalf("signer = %s, want %s", res.pk.Hex(), signerKey.Public().Hex())
	}
}

func TestNostrConnectBunkerURLResumes(t *testing.T) {
	signer := nostr.Generate().Public()
	stored := nostrConnectBunkerURL(signer, []string{"wss://bucket.coracle.social"})
	if !nip46.IsValidBunkerURL(stored) {
		t.Fatalf("%q is not a valid bunker url", stored)
	}
	parsed, err := nip46.ParseBunkerInput(context.Background(), stored)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.HostPubKey != signer {
		t.Fatalf("host = %s, want %s", parsed.HostPubKey.Hex(), signer.Hex())
	}
	if len(parsed.Relays) != 1 || parsed.Relays[0] != "wss://bucket.coracle.social" {
		t.Fatalf("relays = %v", parsed.Relays)
	}
	if parsed.Secret != "" {
		t.Fatalf("stored login carries a secret: %q", parsed.Secret)
	}
}

func TestCleanRelayURL(t *testing.T) {
	for in, want := range map[string]string{
		"relay.nsec.app":         "wss://relay.nsec.app",
		" wss://relay.nsec.app ": "wss://relay.nsec.app",
		"ws://localhost:7777":    "ws://localhost:7777",
	} {
		got, err := cleanRelayURL(in)
		if err != nil || got != want {
			t.Errorf("cleanRelayURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "https://example.com", "wss://"} {
		if got, err := cleanRelayURL(in); err == nil {
			t.Errorf("cleanRelayURL(%q) = %q, want an error", in, got)
		}
	}
}

func TestQRCodeHasQuietZone(t *testing.T) {
	img, err := QRCode("nostrconnect://" + nostr.Generate().Public().Hex() + "?relay=wss%3A%2F%2Fbucket.coracle.social&secret=0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	n := img.Rect.Dx()
	if n != img.Rect.Dy() || n < 21+2*qrQuietZone {
		t.Fatalf("unexpected size %v", img.Rect)
	}
	for i := 0; i < n; i++ {
		for _, p := range [][2]int{{i, 0}, {0, i}, {i, n - 1}, {n - 1, i}} {
			if img.GrayAt(p[0], p[1]).Y != 0xff {
				t.Fatalf("border pixel %v is not white", p)
			}
		}
	}
	// the top-left finder pattern starts right inside the quiet zone
	if img.GrayAt(qrQuietZone, qrQuietZone).Y != 0 {
		t.Fatal("no finder pattern where expected")
	}

	png, err := QRCodePNG("hello", 4)
	if err != nil || len(png) == 0 {
		t.Fatalf("QRCodePNG: %v", err)
	}
}

func TestNostrConnectOnlyOnRequest(t *testing.T) {
	ls.mu.Lock()
	oldPhase := ls.phase
	ls.mu.Unlock()
	t.Cleanup(func() {
		stopNostrConnect()
		ls.mu.Lock()
		ls.phase = oldPhase
		ls.mu.Unlock()
	})

	setPhase(PhaseLoading)
	setPhase(PhaseLogin)
	if uri := nostrConnectURI(); uri != "" {
		t.Fatalf("login screen put up %q before the user asked", uri)
	}

	StartNostrConnect()
	first := nostrConnectURI()
	if !strings.HasPrefix(first, "nostrconnect://") {
		t.Fatalf("StartNostrConnect offered %q", first)
	}
	StartNostrConnect()
	if again := nostrConnectURI(); again == first {
		t.Fatal("a new start reused the old secret")
	}

	CancelNostrConnect()
	if uri := nostrConnectURI(); uri != "" {
		t.Fatalf("cancel left %q on offer", uri)
	}

	StartNostrConnect()
	setPhase(PhaseLoading)
	if uri := nostrConnectURI(); uri != "" {
		t.Fatalf("leaving the login phase left %q on offer", uri)
	}
}
