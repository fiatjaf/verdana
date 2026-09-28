package main

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"gioui.org/widget"
	"verdana/backend"
)

// editorWith is a textarea pre-filled with text, as the editor overlay builds.
func editorWith(text string) widget.Editor {
	var ed widget.Editor
	ed.SetText(text)
	return ed
}

func TestActionLinesFromNappActions(t *testing.T) {
	got := actionLines([]string{"profile", "view:1"})
	want := `{"type":"profile"}` + "\n" + `{"type":"view:1"}`
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

// A bundle suggests what the picked windows were actually sent, payloads
// included, and then the napp's own list for whatever is left.
func TestSuggestionLinesFromWhatTheWindowHandled(t *testing.T) {
	seen := []backend.ShortcutAction{
		{Type: "view:1", Payload: json.RawMessage(`"nostr1aaa"`)},
		{Type: "compose", Payload: json.RawMessage(`{"text":"hi"}`)},
		{Type: "view:1", Payload: json.RawMessage(`"nostr1bbb"`)},
	}
	got := splitLines(suggestionLines(seen, []string{"view:1", "profile"}))
	want := []string{
		`{"type":"view:1","payload":"nostr1bbb"}`,
		`{"type":"compose","payload":{"text":"hi"}}`,
		`{"type":"view:1","payload":"nostr1aaa"}`,
		`{"type":"profile"}`,
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("got %#v want %#v", got, want)
	}
}

// The same action twice is a window sitting still, not two suggestions.
func TestSuggestionLinesDropRepeats(t *testing.T) {
	seen := []backend.ShortcutAction{
		{Type: "compose", Payload: json.RawMessage(`{"text":"hi"}`)},
		{Type: "compose", Payload: json.RawMessage(`{"text":"hi"}`)},
	}
	if got, want := suggestionLines(seen, nil), `{"type":"compose","payload":{"text":"hi"}}`; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestSuggestionLinesCapAndFallback(t *testing.T) {
	var seen []backend.ShortcutAction
	for i := 0; i < maxSuggestedActions*2; i++ {
		seen = append(seen, backend.ShortcutAction{Type: fmt.Sprintf("a%d", i)})
	}
	if got := splitLines(suggestionLines(seen, []string{"profile"})); len(got) != maxSuggestedActions {
		t.Fatalf("suggested %d actions: %#v", len(got), got)
	}
	// most recent first, so the last one suggested is the oldest action
	if got := splitLines(suggestionLines(seen, nil)); got[0] != `{"type":"a15"}` {
		t.Fatalf("did not start at the most recent: %#v", got)
	}
	// a window that handled nothing still offers the napp's list
	if got, want := suggestionLines(nil, []string{"profile", "compose"}), actionLines([]string{"profile", "compose"}); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestHistoryLabel(t *testing.T) {
	cases := []struct {
		actions []backend.ShortcutAction
		want    string
	}{
		{nil, ""},
		{[]backend.ShortcutAction{{Type: "view:1"}}, "view:1"},
		{[]backend.ShortcutAction{{Type: "view:1"}, {Type: "view:1"}, {Type: "compose"}}, "view:1 → compose"},
		{[]backend.ShortcutAction{{Type: "a"}, {Type: "b"}, {Type: "a"}}, "a → b → a"},
	}
	for _, c := range cases {
		if got := historyLabel(c.actions); got != c.want {
			t.Fatalf("got %q want %q", got, c.want)
		}
	}
}

// The checked windows bring the actions they were sent into the bundle.
func TestPickedBundleWindowsCarryHistory(t *testing.T) {
	st := backend.State{ManagedWindows: []backend.WindowInfo{
		{Instance: "1", NappID: "npub1abc", History: []backend.ShortcutAction{{Type: "view:1"}}},
		{Instance: "2", NappID: "npub1abc", History: []backend.ShortcutAction{{Type: "compose"}}},
		{Instance: "3", NappID: "npub1def", History: []backend.ShortcutAction{{Type: "open"}}},
	}}
	bundleChecks = make(map[string]*widget.Bool)
	for _, i := range []string{"1", "2"} {
		cb := new(widget.Bool)
		cb.Value = true
		bundleChecks[i] = cb
	}

	// one entry per checked napp, carrying what every checked window of it
	// was sent; the unchecked window is nobody's business
	want := []backend.ShortcutEntry{
		{NappID: "npub1abc", Actions: []backend.ShortcutAction{{Type: "view:1"}, {Type: "compose"}}},
	}
	if got := pickedBundleWindows(st); !reflect.DeepEqual(want, got) {
		t.Fatalf("got %#v want %#v", got, want)
	}
}

func TestActionsToLinesKeepsPayload(t *testing.T) {
	in := []backend.ShortcutAction{
		{Type: "view:1", Payload: json.RawMessage(`"nostr1abc"`)},
		{Type: "compose", Payload: json.RawMessage(`{"text":"hi"}`)},
		{Type: "open"},
	}
	if got := actionsToLines(in); got != actionsToLines(mustParseActions(t, got)) {
		t.Fatalf("not a fixed point: %q", got)
	}
}

func TestShortcutSpecJSON(t *testing.T) {
	edit := &shortcutEditState{entries: []shortcutEditEntry{{
		nappID: "npub1abc",
		ed:     editorWith(`{"type": "view:1", "payload": "nostr1abc"}` + "\n\n" + `{"type":"compose","payload":{"text":"hi"}}`),
	}}}
	spec, err := shortcutSpecJSON(edit)
	if err != nil {
		t.Fatal(err)
	}
	var got []backend.ShortcutEntry
	if err := json.Unmarshal([]byte(spec), &got); err != nil {
		t.Fatal(err)
	}
	want := []backend.ShortcutEntry{{NappID: "npub1abc", Actions: []backend.ShortcutAction{
		{Type: "view:1", Payload: json.RawMessage(`"nostr1abc"`)},
		{Type: "compose", Payload: json.RawMessage(`{"text":"hi"}`)},
	}}}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("got %#v want %#v", got, want)
	}
}

func TestShortcutSpecJSONErrors(t *testing.T) {
	cases := map[string]string{
		"not json":     `type`,
		"no type":      `{"payload": 1}`,
		"spaced type":  `{"type": "view 1"}`,
		"plus type":    `{"type": "+view"}`,
		"bare string":  `"profile"`,
		"empty object": `{}`,
	}
	for name, line := range cases {
		edit := &shortcutEditState{entries: []shortcutEditEntry{{nappID: "n", ed: editorWith(line)}}}
		if _, err := shortcutSpecJSON(edit); err == nil {
			t.Fatalf("%s: expected an error for %q", name, line)
		}
	}
}

func mustParseActions(t *testing.T, lines string) []backend.ShortcutAction {
	t.Helper()
	var out []backend.ShortcutAction
	for _, line := range splitLines(lines) {
		var a backend.ShortcutAction
		if err := json.Unmarshal([]byte(line), &a); err != nil {
			t.Fatal(err)
		}
		out = append(out, a)
	}
	return out
}

func splitLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}
