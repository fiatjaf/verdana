package backend

import (
	"context"
	"errors"
	"slices"
	"testing"

	"fiatjaf.com/nostr"
)

// fakeLists is a relayListFunc over fixed NIP-65 lists.
type fakeList struct{ write, read []string }

func fakeLists(m map[nostr.PubKey]fakeList) relayListFunc {
	return func(_ context.Context, pk nostr.PubKey) ([]string, []string, bool) {
		l, ok := m[pk]
		return l.write, l.read, ok
	}
}

func TestDirectFilterGroupsAuthors(t *testing.T) {
	a, b, c := nostr.Generate().Public(), nostr.Generate().Public(), nostr.Generate().Public()
	f := nostr.Filter{Authors: []nostr.PubKey{a, b, c}, Kinds: []nostr.Kind{1}, Limit: 20}
	outboxes := map[nostr.PubKey][]string{
		a: {"wss://shared.example.com", "wss://a.example.com"},
		b: {"wss://shared.example.com/"},
		// c has no relays known
	}
	dfs := directFilter(f, outboxes, []string{"wss://hint.example.com"}, []string{"wss://fallback.example.com"})

	got := map[string][]nostr.PubKey{}
	for _, df := range dfs {
		if _, dup := got[df.Relay]; dup {
			t.Errorf("%s asked twice", df.Relay)
		}
		got[df.Relay] = df.Authors
		if df.Limit != 20 || len(df.Kinds) != 1 {
			t.Errorf("%s lost the rest of the filter: %v", df.Relay, df.Filter)
		}
	}
	want := map[string][]nostr.PubKey{
		"wss://shared.example.com":   {a, b},
		"wss://a.example.com":        {a},
		"wss://hint.example.com":     {a, b, c}, // hints get the whole filter
		"wss://fallback.example.com": {c},       // only who nobody else covers
	}
	if len(got) != len(want) {
		t.Fatalf("relays: got %v", got)
	}
	for u, authors := range want {
		if !slices.Equal(got[u], authors) {
			t.Errorf("%s: got %v, want %v", u, got[u], authors)
		}
	}
	if len(f.Authors) != 3 {
		t.Error("the napplet's filter was changed")
	}

	// nothing named and nowhere to go: the fallback relays, whole filter
	dfs = directFilter(nostr.Filter{Kinds: []nostr.Kind{1}}, nil, nil, []string{"wss://fallback.example.com"})
	if len(dfs) != 1 || dfs[0].Relay != "wss://fallback.example.com" {
		t.Errorf("fallback: %v", dfs)
	}
	// a full-filter relay is enough, no fallback needed
	dfs = directFilter(nostr.Filter{Kinds: []nostr.Kind{1}}, nil, []string{"wss://hint.example.com"}, []string{"wss://fallback.example.com"})
	if len(dfs) != 1 || dfs[0].Relay != "wss://hint.example.com" {
		t.Errorf("hint only: %v", dfs)
	}
}

func TestDirectFilterCapsRelays(t *testing.T) {
	var authors []nostr.PubKey
	outboxes := map[nostr.PubKey][]string{}
	for i := range outboxMaxDirected + 10 {
		pk := nostr.Generate().Public()
		authors = append(authors, pk)
		outboxes[pk] = []string{"wss://r" + string(rune('a'+i%26)) + string(rune('a'+i/26)) + ".example.com"}
	}
	dfs := directFilter(nostr.Filter{Authors: authors}, outboxes, nil, []string{"wss://fallback.example.com"})
	if len(dfs) > outboxMaxDirected+1 {
		t.Fatalf("%d directed filters", len(dfs))
	}
	covered := map[nostr.PubKey]bool{}
	for _, df := range dfs {
		for _, pk := range df.Authors {
			covered[pk] = true
		}
	}
	if len(covered) != len(authors) {
		t.Errorf("%d of %d authors asked anywhere", len(covered), len(authors))
	}
}

func TestOutboxPlan(t *testing.T) {
	a, b := nostr.Generate().Public(), nostr.Generate().Public()
	lists := fakeLists(map[nostr.PubKey]fakeList{
		a: {write: []string{"wss://w.example.com"}, read: []string{"wss://r.example.com"}},
	})
	ctx := context.Background()
	fallback := []string{"wss://fallback.example.com"}

	plan := outboxPlan(ctx, lists, []nostr.PubKey{a, b}, "write", fallback)
	if plan.Source != "nip65" || !slices.Equal(plan.Relays, []string{"wss://w.example.com"}) ||
		!slices.Equal(plan.MissingAuthors, []string{b.Hex()}) {
		t.Errorf("write plan: %+v", plan)
	}
	plan = outboxPlan(ctx, lists, []nostr.PubKey{a}, "read", fallback)
	if !slices.Equal(plan.Relays, []string{"wss://r.example.com"}) {
		t.Errorf("read plan: %+v", plan)
	}
	plan = outboxPlan(ctx, lists, []nostr.PubKey{b}, "write", fallback)
	if plan.Source != "fallback" || !slices.Equal(plan.Relays, fallback) {
		t.Errorf("fallback plan: %+v", plan)
	}
}

func TestOutboxFanout(t *testing.T) {
	user, bob, nolist := nostr.Generate().Public(), nostr.Generate().Public(), nostr.Generate().Public()
	lists := fakeLists(map[nostr.PubKey]fakeList{
		user: {write: []string{"wss://1.1.1.1", "wss://8.8.8.8"}, read: []string{"wss://9.9.9.9"}},
		// bob's inbox list names a relay on the user's own network: skipped
		bob: {write: []string{"wss://4.4.4.4"}, read: []string{"wss://8.8.8.8", "ws://192.168.1.2"}},
	})
	ctx := context.Background()

	targets, inboxes, err := outboxFanout(ctx, lists, napExplicitRelay, user, true, []nostr.PubKey{bob}, []string{"wss://1.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(targets, []string{"wss://1.1.1.1", "wss://8.8.8.8", "wss://1.0.0.1"}) {
		t.Errorf("targets: %v", targets)
	}
	if !slices.Equal(inboxes[bob], []string{"wss://8.8.8.8"}) {
		t.Errorf("bob's inboxes: %v", inboxes[bob])
	}

	// inbox only: the user's own outbox stays out of it
	targets, _, err = outboxFanout(ctx, lists, napExplicitRelay, user, false, []nostr.PubKey{bob}, nil)
	if err != nil || !slices.Equal(targets, []string{"wss://8.8.8.8"}) {
		t.Errorf("inbox only: %v %v", targets, err)
	}

	if _, _, err := outboxFanout(ctx, lists, napExplicitRelay, user, true, []nostr.PubKey{nolist}, nil); !errors.Is(err, errNoInbox) {
		t.Errorf("a recipient with no relay list: %v", err)
	}
	for _, bad := range []string{"ws://127.0.0.1:7777", "wss://localhost", "https://1.1.1.1"} {
		if _, _, err := outboxFanout(ctx, lists, napExplicitRelay, user, true, nil, []string{bad}); err == nil {
			t.Errorf("explicit relay %s accepted", bad)
		}
	}
	if _, _, err := outboxFanout(ctx, lists, napExplicitRelay, nolist, true, nil, nil); err == nil {
		t.Error("published to nowhere")
	}
}

// withSystem gives a test an sdk system on a fresh store, with no relays
// configured, so nothing dials out.
func withSystem(t *testing.T) {
	t.Helper()
	closeStore, err := initSystem(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sys.Pool.Close("test over")
		closeStore()
		sys = nil
	})
}

func signedNote(t *testing.T, content string) nostr.Event {
	t.Helper()
	sk := nostr.Generate()
	evt := nostr.Event{Kind: 1, Content: content, CreatedAt: nostr.Now(), Tags: nostr.Tags{}}
	if err := evt.Sign(sk); err != nil {
		t.Fatal(err)
	}
	return evt
}

func TestNapOutboxGetEvent(t *testing.T) {
	setupNapTest(t)
	withSystem(t)
	evt := signedNote(t, "hello")
	if err := sys.Store.SaveEvent(evt); err != nil {
		t.Fatal(err)
	}
	ci, rec := openNapplet(t, "reader")
	ready(t, ci, rec, 1)

	post(t, ci, map[string]any{"type": "outbox.getEvent", "id": "1", "eventId": evt.ID.Hex()})
	got := rec.wait(t, "outbox.getEvent.result", 1)
	res, _ := got["result"].(map[string]any)
	ev, _ := res["event"].(map[string]any)
	if got["id"] != "1" || ev["content"] != "hello" || got["error"] != nil {
		t.Errorf("stored event: %v", got)
	}

	other := signedNote(t, "elsewhere")
	post(t, ci, map[string]any{"type": "outbox.getEvent", "id": "2", "eventId": other.ID.Hex(),
		"options": map[string]any{"timeoutMs": 1000}})
	if got := rec.wait(t, "outbox.getEvent.result", 2); got["error"] != "not found" || got["result"] != nil {
		t.Errorf("unknown event: %v", got)
	}

	post(t, ci, map[string]any{"type": "outbox.getEvent", "id": "3", "eventId": "nope"})
	if got := rec.wait(t, "outbox.getEvent.result", 3); got["error"] != "invalid filter" {
		t.Errorf("bad id: %v", got)
	}
}

func TestNapOutboxSubscribe(t *testing.T) {
	setupNapTest(t)
	withSystem(t)
	evt := signedNote(t, "stored")
	if err := sys.Store.SaveEvent(evt); err != nil {
		t.Fatal(err)
	}
	ci, rec := openNapplet(t, "feed")
	ready(t, ci, rec, 1)

	post(t, ci, map[string]any{"type": "outbox.subscribe", "id": "s", "subId": "feed",
		"filters": []any{map[string]any{"kinds": []int{1}}}})
	got := rec.wait(t, "outbox.event", 1)
	res, _ := got["result"].(map[string]any)
	if ev, _ := res["event"].(map[string]any); got["subId"] != "feed" || ev["content"] != "stored" {
		t.Errorf("event: %v", got)
	}
	// no relays configured: the shell ends the stream, and says so
	if got := rec.wait(t, "outbox.closed", 1); got["subId"] != "feed" {
		t.Errorf("closed: %v", got)
	}

	post(t, ci, map[string]any{"type": "outbox.subscribe", "id": "s2", "subId": "bad", "filters": "nope"})
	if got := rec.wait(t, "outbox.closed", 2); got["subId"] != "bad" || got["reason"] != "invalid filter" {
		t.Errorf("invalid filters: %v", got)
	}
	for _, typ := range rec.types() {
		if typ == "outbox.eose" || typ == "relay.eose" {
			t.Errorf("an outbox subscription sent %s", typ)
		}
	}
}

func TestNapOutboxResolveRelaysNeedsAuthors(t *testing.T) {
	setupNapTest(t)
	ci, rec := openNapplet(t, "planner")
	ready(t, ci, rec, 1)

	post(t, ci, map[string]any{"type": "outbox.resolveRelays", "id": "1", "target": map[string]any{}})
	if got := rec.wait(t, "outbox.resolveRelays.result", 1); got["error"] != "no authors" {
		t.Errorf("no target, signed out: %v", got)
	}
	post(t, ci, map[string]any{"type": "outbox.resolveRelays", "id": "2",
		"target": map[string]any{"pubkey": nostr.Generate().Public().Hex(), "direction": "sideways"}})
	if got := rec.wait(t, "outbox.resolveRelays.result", 2); got["error"] != "invalid target" {
		t.Errorf("bad direction: %v", got)
	}
}

func TestNapDomainsAdvertiseOutbox(t *testing.T) {
	setupNapTest(t)
	ci, rec := openNapplet(t, "caps")
	ready(t, ci, rec, 1)
	init := rec.wait(t, "shell.init", 1)
	caps, _ := init["capabilities"].(map[string]any)
	domains, _ := caps["domains"].([]any)
	if !slices.Contains(domains, any("outbox")) {
		t.Errorf("shell.init domains: %v", domains)
	}
}

func TestNapOutboxQueryLimitAndOrder(t *testing.T) {
	setupNapTest(t)
	withSystem(t)
	for i, content := range []string{"old", "mid", "new"} {
		evt := signedNote(t, content)
		evt.CreatedAt = nostr.Timestamp(1000 + i)
		sk := nostr.Generate()
		if err := evt.Sign(sk); err != nil {
			t.Fatal(err)
		}
		if err := sys.Store.SaveEvent(evt); err != nil {
			t.Fatal(err)
		}
	}
	ci, rec := openNapplet(t, "lister")
	ready(t, ci, rec, 1)

	post(t, ci, map[string]any{"type": "outbox.query", "id": "q",
		"filters": []any{map[string]any{"kinds": []int{1}, "limit": 2}}})
	got := rec.wait(t, "outbox.query.result", 1)
	evs, _ := got["events"].([]any)
	var contents []string
	for _, e := range evs {
		contents = append(contents, e.(map[string]any)["event"].(map[string]any)["content"].(string))
	}
	if !slices.Equal(contents, []string{"new", "mid"}) {
		t.Errorf("query: %v (%v)", contents, got)
	}
}
