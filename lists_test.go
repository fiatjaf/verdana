package main

import (
	"context"
	"encoding/json"
	"testing"

	"fiatjaf.com/nostr"
)

const testPubkey = "3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d"

func TestRelayListItems(t *testing.T) {
	evt := nostr.Event{Tags: nostr.Tags{
		{"r", "wss://both.example.com"},
		{"r", "wss://read.example.com", "read"},
		{"r", "wss://write.example.com", "write"},
		{"r", ""},
		{"p", testPubkey},
	}}

	items := relayListItems(evt)
	if len(items) != 3 {
		t.Fatalf("expected 3 relay items, got %d: %v", len(items), items)
	}

	want := []map[string]any{
		{"url": "wss://both.example.com", "read": true, "write": true},
		{"url": "wss://read.example.com", "read": true, "write": false},
		{"url": "wss://write.example.com", "read": false, "write": true},
	}
	for i, w := range want {
		got := items[i].(map[string]any)
		for k, v := range w {
			if got[k] != v {
				t.Errorf("item %d: %s = %v, want %v", i, k, got[k], v)
			}
		}
	}
}

func TestPubkeyItemsSkipGarbage(t *testing.T) {
	evt := nostr.Event{Tags: nostr.Tags{
		{"p", testPubkey},
		{"p", "not-a-pubkey"},
		{"e", testPubkey},
		{"p"},
	}}
	items := itemsFromTags(evt, pubkeyItem("p"))
	if len(items) != 1 || items[0] != testPubkey {
		t.Fatalf("expected only the valid pubkey, got %v", items)
	}
}

func TestAddressPointer(t *testing.T) {
	item, ok := addressPointer("30002:"+testPubkey+":my:set", "wss://hint.example.com", 30002)
	if !ok {
		t.Fatal("expected the coordinate to parse")
	}
	m := item.(map[string]any)
	if m["identifier"] != "my:set" {
		t.Errorf("identifier = %v, want my:set", m["identifier"])
	}
	if m["kind"] != 30002 {
		t.Errorf("kind = %v, want 30002", m["kind"])
	}
	if relays := m["relays"].([]string); len(relays) != 1 || relays[0] != "wss://hint.example.com" {
		t.Errorf("relays = %v", relays)
	}

	if _, ok := addressPointer("30000:"+testPubkey+":x", "", 30002); ok {
		t.Error("a mismatching kind should be rejected")
	}
	if _, ok := addressPointer("30002:nothex:x", "", 30002); ok {
		t.Error("a bad pubkey should be rejected")
	}
}

func TestMuteListLabels(t *testing.T) {
	evt := nostr.Event{Kind: 10000, Tags: nostr.Tags{
		{"p", testPubkey},
		{"t", "spam"},
		{"word", "airdrop"},
	}}
	items := itemsFromTags(evt, func(tag nostr.Tag) (any, bool) {
		// same parser loadMuteList uses
		switch tag[0] {
		case "p":
			return map[string]any{"label": "pubkey", "value": tag[1]}, true
		case "t":
			return map[string]any{"label": "hashtag", "value": tag[1]}, true
		case "word":
			return map[string]any{"label": "word", "value": tag[1]}, true
		}
		return nil, false
	})
	if len(items) != 3 {
		t.Fatalf("expected 3 muted entities, got %d", len(items))
	}
}

func TestListResultMarshalsEmptyItemsAsArray(t *testing.T) {
	data, err := json.Marshal(emptyListResult())
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"event":null,"items":[]}` {
		t.Fatalf("unexpected empty list result: %s", data)
	}
}

func TestSanitizeFilename(t *testing.T) {
	for input, want := range map[string]string{
		"note.json":              "note.json",
		"../../etc/passwd":       "passwd",
		`C:\Windows\evil.exe`:    "evil.exe",
		"..hidden":               "hidden",
		"":                       "download",
		"with:bad*chars?.txt":    "withbadchars.txt",
		"/tmp/nested/thing.webp": "thing.webp",
	} {
		if got := sanitizeFilename(input); got != want {
			t.Errorf("sanitizeFilename(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestResolveViewPayloadAcceptsInlineEvent(t *testing.T) {
	evt := nostr.Event{Kind: 1, Content: "hello"}
	evt.SetID()
	inline, err := json.Marshal(evt)
	if err != nil {
		t.Fatal(err)
	}

	// the event as an object passes straight through
	out, ok := resolveViewPayload(context.Background(), inline)
	if !ok || string(out) != string(inline) {
		t.Fatalf("an event object should pass through unchanged, got ok=%v", ok)
	}

	// and as a JSON string it gets unwrapped into the object
	asString, err := json.Marshal(string(inline))
	if err != nil {
		t.Fatal(err)
	}
	out, ok = resolveViewPayload(context.Background(), asString)
	if !ok {
		t.Fatal("an event serialized as a string should resolve")
	}
	var round nostr.Event
	if err := json.Unmarshal(out, &round); err != nil {
		t.Fatal(err)
	}
	if round.ID != evt.ID {
		t.Errorf("resolved a different event: %s vs %s", round.ID, evt.ID)
	}
}

func TestIsHex64(t *testing.T) {
	if !isHex64(testPubkey) {
		t.Error("a 64-char hex string should pass")
	}
	if isHex64(testPubkey[:63]) || isHex64(testPubkey+"a") || isHex64("z"+testPubkey[1:]) {
		t.Error("bad lengths and non-hex characters should fail")
	}
}
