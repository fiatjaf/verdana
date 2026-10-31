package backend

import (
	"encoding/json"
	"testing"
)

func TestNormalizePayload(t *testing.T) {
	const hex = "3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d"
	const npub = "npub180cvv07tjdrrgpa0j7j7tmnyl2yr6yr7l8j4s3evf6u64th6gkwsyjh6w6"
	for _, in := range []string{npub, "nostr:" + npub} {
		raw, _ := json.Marshal(in)
		if got := string(normalizePayload("profile", raw)); got != `"`+hex+`"` {
			t.Errorf("normalizePayload(%s) = %s", in, got)
		}
	}
	raw, _ := json.Marshal(npub)
	if got := string(normalizePayload("view", raw)); got != string(raw) {
		t.Errorf("view payload was changed: %s", got)
	}
	if got := string(normalizePayload("profile", json.RawMessage(`{"a":1}`))); got != `{"a":1}` {
		t.Errorf("object payload was changed: %s", got)
	}
}

func TestParseLinkAction(t *testing.T) {
	cases := map[string]ShortcutAction{
		"open":                     {Type: "open"},
		"profile~npub1x":           {Type: "profile", Payload: json.RawMessage(`"npub1x"`)},
		`view~{"kind":1,"id":"a"}`: {Type: "view", Payload: json.RawMessage(`{"kind":1,"id":"a"}`)},
		"search~123":               {Type: "search", Payload: json.RawMessage(`"123"`)},
	}
	for in, want := range cases {
		got, ok := parseLinkAction(in)
		if !ok || got.Type != want.Type || string(got.Payload) != string(want.Payload) {
			t.Errorf("parseLinkAction(%q) = %+v, %v", in, got, ok)
		}
	}
	if _, ok := parseLinkAction("~x"); ok {
		t.Error("an action with no name was taken")
	}
}

func TestMatchesActionQuery(t *testing.T) {
	n := Napp{Name: "Notes", Actions: []string{"view", "Profile"}}
	for q, want := range map[string]bool{
		"":                               true,
		"#action=profile":                true,
		"#action=view:1":                 true,
		"#action=zap":                    false,
		"#action=profile notes":          true,
		"#action=profile other":          false,
		"#action=profile #action=view:0": true,
	} {
		if got := n.MatchesQuery(q); got != want {
			t.Errorf("MatchesQuery(%q) = %v", q, got)
		}
	}
}
