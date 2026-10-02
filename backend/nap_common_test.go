package backend

import (
	"encoding/json"
	"strings"
	"testing"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip19"
)

func TestNapCommonMergeFollowTags(t *testing.T) {
	a, b, c := nostr.Generate().Public(), nostr.Generate().Public(), nostr.Generate().Public()
	current := nostr.Tags{{"p", a.Hex(), "wss://r.example.com", "alice"}, {"t", "nostr"}, {"p", b.Hex()}}

	tags, changed := mergeFollowTags(current, []nostr.PubKey{a, c}, true)
	if !changed || len(tags) != 4 || tags[3][1] != c.Hex() {
		t.Fatalf("follow: changed=%v %v", changed, tags)
	}
	// the petname and relay hint on an existing follow survive
	if len(tags[0]) != 4 || tags[0][3] != "alice" {
		t.Errorf("existing entry rewritten: %v", tags[0])
	}
	if len(current) != 3 {
		t.Errorf("current list mutated: %v", current)
	}

	if _, changed := mergeFollowTags(current, []nostr.PubKey{a, b}, true); changed {
		t.Error("following people already followed changed the list")
	}
	if _, changed := mergeFollowTags(current, []nostr.PubKey{c}, false); changed {
		t.Error("unfollowing someone not followed changed the list")
	}
	if _, changed := mergeFollowTags(nil, []nostr.PubKey{c}, false); changed {
		t.Error("unfollowing with no list changed it")
	}

	dup := append(nostr.Tags{{"p", b.Hex()}}, current...)
	tags, changed = mergeFollowTags(dup, []nostr.PubKey{b}, false)
	if !changed || len(tags) != 2 || tags[0][0] != "p" || tags[0][1] != a.Hex() || tags[1][0] != "t" {
		t.Errorf("unfollow: changed=%v %v", changed, tags)
	}
}

func TestNapCommonReactionTemplate(t *testing.T) {
	author := nostr.Generate().Public()
	note := nostr.Event{ID: nostr.ID{1}, PubKey: author, Kind: 1}

	tmpl, code := reactionTemplate(note, "+", "")
	if code != "" || tmpl.Kind != 7 || tmpl.Content != "+" {
		t.Fatalf("+: %q %+v", code, tmpl)
	}
	if e := tmpl.Tags.Find("e"); e == nil || e[1] != note.ID.Hex() {
		t.Errorf("e tag: %v", tmpl.Tags)
	}
	if p := tmpl.Tags.Find("p"); p == nil || p[1] != author.Hex() {
		t.Errorf("p tag: %v", tmpl.Tags)
	}
	if k := tmpl.Tags.Find("k"); k == nil || k[1] != "1" {
		t.Errorf("k tag: %v", tmpl.Tags)
	}
	if tmpl.Tags.Find("a") != nil {
		t.Errorf("a tag on a regular note: %v", tmpl.Tags)
	}

	if _, code := reactionTemplate(note, "🤙", ""); code != "" {
		t.Errorf("emoji refused: %q", code)
	}

	tmpl, code = reactionTemplate(note, ":soapbox:", "https://example.com/soapbox.png")
	if emoji := tmpl.Tags.Find("emoji"); code != "" || emoji == nil ||
		emoji[1] != "soapbox" || emoji[2] != "https://example.com/soapbox.png" {
		t.Errorf("custom emoji: %q %v", code, tmpl.Tags)
	}

	for _, bad := range []struct{ reaction, href string }{
		{"", ""},
		{"two words", ""},
		{"line\nbreak", ""},
		{strings.Repeat("x", 65), ""},
		{":soapbox:", ""},                           // shortcode without its image
		{"+", "https://example.com/soapbox.png"},    // image without a shortcode
		{":soapbox:", "javascript:alert(1)"},        // not a web url
		{":soap box:", "https://example.com/s.png"}, // not a shortcode
	} {
		if _, code := reactionTemplate(note, bad.reaction, bad.href); code != "invalid-reaction" {
			t.Errorf("reaction %q href %q: got %q", bad.reaction, bad.href, code)
		}
	}

	article := nostr.Event{ID: nostr.ID{2}, PubKey: author, Kind: 30023, Tags: nostr.Tags{{"d", "post"}}}
	tmpl, _ = reactionTemplate(article, "+", "")
	if a := tmpl.Tags.Find("a"); a == nil || a[1] != "30023:"+author.Hex()+":post" {
		t.Errorf("a tag on an article: %v", tmpl.Tags)
	}
}

func TestNapCommonReportTarget(t *testing.T) {
	author := nostr.Generate().Public()
	id := nostr.ID{3}
	parse := func(v any) (reportTarget, string) {
		raw, _ := json.Marshal(v)
		return parseReportTarget(raw)
	}

	got, code := parse(map[string]any{"type": "pubkey", "pubkey": nip19.EncodeNpub(author)})
	if code != "" || got.Type != "pubkey" || got.Pubkey != author.Hex() {
		t.Errorf("pubkey target: %q %+v", code, got)
	}
	got, code = parse(map[string]any{"type": "event", "id": id.Hex()})
	if code != "" || got.Type != "event" || got.ID != id.Hex() || got.Pubkey != "" {
		t.Errorf("event target without author: %q %+v", code, got)
	}
	got, code = parse(nip19.EncodeNpub(author))
	if code != "" || got.Type != "pubkey" || got.Pubkey != author.Hex() {
		t.Errorf("npub shorthand: %q %+v", code, got)
	}
	got, code = parse("nostr:" + nip19.EncodeNevent(id, []string{"wss://r.example.com"}, author))
	if code != "" || got.Type != "event" || got.ID != id.Hex() || got.Pubkey != author.Hex() || got.Relay != "wss://r.example.com" {
		t.Errorf("nevent shorthand: %q %+v", code, got)
	}

	for _, bad := range []any{
		author.Hex(), // event id or pubkey? can't tell
		map[string]any{"type": "event", "id": "nope"},
		map[string]any{"type": "user", "pubkey": author.Hex()},
		nil,
	} {
		if _, code := parse(bad); code != "invalid-target" {
			t.Errorf("target %v: got %q", bad, code)
		}
	}
	if _, code := parse(map[string]any{"type": "pubkey", "pubkey": "nope"}); code != "invalid-pubkey" {
		t.Errorf("bad pubkey: got %q", code)
	}

	ev := reportTemplate(reportTarget{Type: "event", ID: id.Hex(), Pubkey: author.Hex()}, "spam", "bot")
	if ev.Kind != 1984 || ev.Content != "bot" || len(ev.Tags) != 2 ||
		ev.Tags[0][0] != "e" || ev.Tags[0][2] != "spam" || ev.Tags[1][0] != "p" || ev.Tags[1][1] != author.Hex() {
		t.Errorf("event report: %+v", ev)
	}
	pk := reportTemplate(reportTarget{Type: "pubkey", Pubkey: author.Hex()}, "impersonation", "")
	if len(pk.Tags) != 1 || pk.Tags[0][0] != "p" || pk.Tags[0][2] != "impersonation" {
		t.Errorf("pubkey report: %+v", pk)
	}
}

func TestNapCommonNip19Edges(t *testing.T) {
	setupNapTest(t)
	ci, rec := openNapplet(t, "nip19edges")
	ready(t, ci, rec, 1)
	pk := nostr.Generate().Public()
	id := nostr.ID{4}

	// nevent keeps its kind
	post(t, ci, map[string]any{"type": "common.encodeNip19", "id": "e1", "input": map[string]any{
		"type": "nevent", "eventId": id.Hex(), "author": pk.Hex(), "kind": 30023, "relays": []string{"wss://r.example.com"}}})
	nevent, _ := rec.wait(t, "common.encodeNip19.result", 1)["value"].(string)
	post(t, ci, map[string]any{"type": "common.decodeNip19", "id": "d1", "value": nevent})
	dec := rec.wait(t, "common.decodeNip19.result", 1)
	if dec["ok"] != true || dec["eventId"] != id.Hex() || dec["author"] != pk.Hex() || dec["kind"] != float64(30023) {
		t.Errorf("nevent round trip: %v", dec)
	}

	// a relay too long for its TLV is refused, not wrapped
	post(t, ci, map[string]any{"type": "common.encodeNip19", "id": "e2", "input": map[string]any{
		"type": "nprofile", "pubkey": pk.Hex(), "relays": []string{"wss://" + strings.Repeat("a", 300)}}})
	if got := rec.wait(t, "common.encodeNip19.result", 2); got["ok"] != false || got["error"] != "invalid-nip19" {
		t.Errorf("long relay: %v", got)
	}

	post(t, ci, map[string]any{"type": "common.decodeNip19", "id": "d2", "value": strings.Repeat("q", nip19MaxLen+1)})
	if got := rec.wait(t, "common.decodeNip19.result", 2); got["error"] != "invalid-nip19" {
		t.Errorf("oversized input: %v", got)
	}

	// unknown TLVs are skipped
	nrelay := bech32Encode("nrelay", append([]byte{9, 1, 'x', 0, 19}, "wss://r.example.com"...))
	post(t, ci, map[string]any{"type": "common.decodeNip19", "id": "d3", "value": nrelay})
	if got := rec.wait(t, "common.decodeNip19.result", 3); got["relay"] != "wss://r.example.com" {
		t.Errorf("nrelay with unknown TLV: %v", got)
	}

	post(t, ci, map[string]any{"type": "common.decodeNip19", "id": "d4", "value": bech32Encode("ncryptsec", []byte{1, 2, 3})})
	if got := rec.wait(t, "common.decodeNip19.result", 4); got["error"] != "unsupported-nip19-type" {
		t.Errorf("unknown prefix: %v", got)
	}
	post(t, ci, map[string]any{"type": "common.decodeNip19", "id": "d5", "value": "npub1notbech32"})
	if got := rec.wait(t, "common.decodeNip19.result", 5); got["error"] != "invalid-nip19" {
		t.Errorf("garbage: %v", got)
	}
}

func TestNapCommonSignedOut(t *testing.T) {
	setupNapTest(t)
	ci, rec := openNapplet(t, "signedout")
	ready(t, ci, rec, 1)
	npub := nip19.EncodeNpub(nostr.Generate().Public())

	for i, msg := range []map[string]any{
		{"type": "common.follows"},
		{"type": "common.follow", "pubkeys": []string{npub}},
		{"type": "common.unfollow", "pubkeys": []string{npub}},
		{"type": "common.react", "targetEventId": nostr.ID{5}.Hex(), "reaction": "+"},
		{"type": "common.report", "target": map[string]any{"type": "pubkey", "pubkey": npub}, "reason": "spam", "text": ""},
	} {
		msg["id"] = string(rune('a' + i))
		post(t, ci, msg)
		if got := rec.wait(t, msg["type"].(string)+".result", 1); got["ok"] != false || got["error"] != "not-signed-in" {
			t.Errorf("%s: %v", msg["type"], got)
		}
	}

	post(t, ci, map[string]any{"type": "common.getProfile", "id": "p", "target": "nope"})
	if got := rec.wait(t, "common.getProfile.result", 1); got["error"] != "invalid-profile-target" {
		t.Errorf("bad profile target: %v", got)
	}
}
