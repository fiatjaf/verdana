package backend

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestBundleTokenRoundTrip(t *testing.T) {
	in := []ShortcutEntry{
		{
			NappID: "npub1abc",
			Actions: []ShortcutAction{
				{Type: "view:1", Payload: json.RawMessage(`"nostr1abc"`)},
				{Type: "profile", Payload: json.RawMessage(`{"id":7}`)},
				{Type: "compose"},
			},
		},
		{NappID: "npub1def", Actions: []ShortcutAction{{Type: "open"}}},
		{NappID: "npub1ghi"},
	}
	tok := bundleToken(in)
	t.Logf("token: %s", tok)
	for _, f := range strings.Fields(tok)[1:] {
		if strings.ContainsAny(f, " \t\"'") {
			t.Fatalf("token field needs quoting: %q", f)
		}
	}
	out, err := parseBundleToken(tok)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("round trip mismatch:\n in=%#v\nout=%#v", in, out)
	}
}

func TestParseAcceptsBareActionName(t *testing.T) {
	// tokens written before actions carried payloads: a bare name still runs
	out, err := parseBundleToken("npub1abc +view:1")
	if err != nil {
		t.Fatal(err)
	}
	want := []ShortcutEntry{{NappID: "npub1abc", Actions: []ShortcutAction{{Type: "view:1"}}}}
	if !reflect.DeepEqual(want, out) {
		t.Fatalf("got %#v", out)
	}
}

func TestParseRejectsBad(t *testing.T) {
	for _, tok := range []string{"", "+view:1", "npub1abc ++x", "npub1abc +"} {
		if _, err := parseBundleToken(tok); err == nil {
			t.Fatalf("expected error for %q", tok)
		}
	}
}

type fakeShortcutHost struct {
	noopHost
	lastToken string
	deleted   bool
}

func (f *fakeShortcutHost) CreateShortcutFile(name, token string) (string, error) {
	f.lastToken = token
	return "/tmp/" + name + ".desktop", nil
}

func (f *fakeShortcutHost) DeleteShortcutFile(string) error {
	f.deleted = true
	return nil
}

func TestCreateShortcutStoresAndDeletes(t *testing.T) {
	fake := &fakeShortcutHost{}
	dataDir = t.TempDir()
	loadState()
	host = fake

	entries, _ := json.Marshal([]ShortcutEntry{{
		NappID:  "npub1abc",
		Actions: []ShortcutAction{{Type: "view:1", Payload: json.RawMessage(`"nostr1abc"`)}},
	}})
	if err := CreateShortcut("My Bundle", string(entries)); err != nil {
		t.Fatal(err)
	}
	if got := shortcuts(); len(got) != 1 || got[0].Name != "My Bundle" {
		t.Fatalf("stored wrong: %#v", got)
	}
	// the token the host got must decode back to the very same action
	parsed, err := parseBundleToken(fake.lastToken)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(parsed, []ShortcutEntry{{
		NappID:  "npub1abc",
		Actions: []ShortcutAction{{Type: "view:1", Payload: json.RawMessage(`"nostr1abc"`)}},
	}}) {
		t.Fatalf("token lost the payload: %#v (token %q)", parsed, fake.lastToken)
	}

	if err := DeleteShortcut("My Bundle"); err != nil {
		t.Fatal(err)
	}
	if len(shortcuts()) != 0 || !fake.deleted {
		t.Fatalf("delete failed: %#v deleted=%v", shortcuts(), fake.deleted)
	}
}

func TestCreateShortcutValidation(t *testing.T) {
	fake := &fakeShortcutHost{}
	dataDir = t.TempDir()
	loadState()
	host = fake

	cases := []struct{ name, spec string }{
		{"", "[]"},
		{"x", "[]"},
		{"x", `[{"nappId":"a","actions":[{"type":"view 1"}]}]`},
		{"x", `[{"nappId":"a","actions":[{"type":"+view"}]}]`},
		{"x", `[{"nappId":"a","actions":[{"payload":1}]}]`},
		{"x", `[{"actions":[]}]`},
		{"x", "not json"},
	}
	for _, c := range cases {
		if err := CreateShortcut(c.name, c.spec); err == nil {
			t.Fatalf("expected error for name=%q spec=%q", c.name, c.spec)
		}
	}
}
