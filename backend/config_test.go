package backend

import (
	"testing"

	"fiatjaf.com/nostr"
)

func TestConfigFromEvent(t *testing.T) {
	pk := nostr.Generate().Public()
	addr := "35130:" + pk.Hex() + ":notes"
	other := "35131:" + pk.Hex() + ":paint"
	id := pk.Hex()[:16] + "~notes"

	evt := nostr.Event{
		Kind:   KindConfig,
		PubKey: pk,
		Tags: nostr.Tags{
			{"d", "work"},
			{"title", "Work setup"},
			{"napp", addr, "wss://relay.example.com"},
			{"napp", "aa3c6e4fd4c2ea0ae6c69b14d0ef68a40ab3a7f78e6c55e2c4b6ab1e4ad4a2f3", "", pk.Hex()},
			{"napp", "not-a-ref"},
			{"action", "view:1", addr, "timed", "singleton"},
			{"action", "view:1", other, "forced"},
			{"action", "share", other, "sometimes"},
			{"action", "edit", other, "suggested", ""},
			{"bundle", "Desk", "https://example.com/desk.png",
				"napp " + addr + ` open {"path": "/a b", "n": [1, 2]} list null`,
				"napp " + other + " napp " + addr + " view null"},
			{"bundle", "Broken", "", "open null"},
			{"sign", "allow"},
			{"publish", "maybe"},
			{"auth-on-reads", "no"},
		},
	}
	c := configFromEvent(evt)

	if c.D != "work" || c.Label() != "Work setup" {
		t.Fatalf("d/name: %q %q", c.D, c.Label())
	}
	if len(c.Napps) != 2 || c.Napps[0].Addr != addr || c.Napps[0].Relay != "wss://relay.example.com" ||
		c.Napps[1].Author != pk {
		t.Fatalf("napps: %+v", c.Napps)
	}
	if len(c.Handlers) != 2 {
		t.Fatalf("handlers: %+v", c.Handlers)
	}
	if h := c.Handlers[0]; h.Action != "view:1" || h.Target != id || h.Mode != HandlerTimed || !h.Singleton {
		t.Fatalf("first handler: %+v", h)
	}
	if h := c.Handlers[1]; h.Action != "edit" || h.Mode != HandlerSuggested || h.Singleton {
		t.Fatalf("second handler: %+v", h)
	}
	if c.Sign != DecisionAllow || c.Publish != "" || c.ReadAuth == nil || *c.ReadAuth {
		t.Fatalf("permissions: sign %q publish %q readAuth %v", c.Sign, c.Publish, c.ReadAuth)
	}
	if len(c.Bundles) != 1 {
		t.Fatalf("bundles: %+v", c.Bundles)
	}
	b := c.Bundles[0]
	if b.Name != "Desk" || b.Icon != "https://example.com/desk.png" || len(b.Entries) != 3 {
		t.Fatalf("bundle: %+v", b)
	}
	if e := b.Entries[0]; e.NappID != id || len(e.Actions) != 2 ||
		e.Actions[0].Type != "open" || string(e.Actions[0].Payload) != `{"path": "/a b", "n": [1, 2]}` ||
		e.Actions[1].Type != "list" || e.Actions[1].Payload != nil {
		t.Fatalf("first entry: %+v", e)
	}
	if e := b.Entries[1]; e.NappID != pk.Hex()[:16]+"~paint" || len(e.Actions) != 0 {
		t.Fatalf("second entry: %+v", e)
	}
	if e := b.Entries[2]; e.NappID != id || len(e.Actions) != 1 || e.Actions[0].Type != "view" {
		t.Fatalf("third entry: %+v", e)
	}
}
