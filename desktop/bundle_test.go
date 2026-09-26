package main

import (
	"encoding/json"
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
