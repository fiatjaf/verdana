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

// fakeShortcutHost is the OS shortcut folder: a file per shortcut, which is
// all a shortcut is now.
type fakeShortcutHost struct {
	noopHost
	lastToken string
	deleted   bool
	files     map[string]ShortcutFile
}

func (f *fakeShortcutHost) CreateShortcutFile(name, token string) (string, error) {
	f.lastToken = token
	path := "/tmp/verdana-" + name + ".desktop"
	if f.files == nil {
		f.files = make(map[string]ShortcutFile)
	}
	f.files[path] = ShortcutFile{Name: name, Path: path, Token: token}
	return path, nil
}

func (f *fakeShortcutHost) DeleteShortcutFile(path string) error {
	f.deleted = true
	delete(f.files, path)
	return nil
}

func (f *fakeShortcutHost) ListShortcutFiles() []ShortcutFile {
	out := make([]ShortcutFile, 0, len(f.files))
	for _, file := range f.files {
		out = append(out, file)
	}
	return out
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
	// the list comes from the files, so the entries must be the ones the
	// shortcut file's token carries
	if got := shortcuts(); len(got) == 1 && !reflect.DeepEqual(got[0].Entries, []ShortcutEntry{{
		NappID:  "npub1abc",
		Actions: []ShortcutAction{{Type: "view:1", Payload: json.RawMessage(`"nostr1abc"`)}},
	}}) {
		t.Fatalf("entries came back wrong: %#v", got[0].Entries)
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

// a shortcut the user made on another machine, or before, is listed as long as
// its file is there: nothing of ours has to remember it.
func TestShortcutsComeFromTheFiles(t *testing.T) {
	fake := &fakeShortcutHost{files: map[string]ShortcutFile{
		"/tmp/verdana-bundle.desktop": {
			Name:  "Zebra",
			Path:  "/tmp/verdana-bundle.desktop",
			Token: "npub1abc +view",
		},
		"/tmp/verdana-other.desktop": {
			Name:  "Apple",
			Path:  "/tmp/verdana-other.desktop",
			Token: "npub1def",
		},
		"/tmp/broken.desktop": {
			Name:  "Broken",
			Path:  "/tmp/broken.desktop",
			Token: "+leading-action",
		},
	}}
	dataDir = t.TempDir()
	loadState()
	host = fake

	got := shortcuts()
	if len(got) != 2 {
		t.Fatalf("expected the two readable ones, got %#v", got)
	}
	// listed by name, so the row order does not jump around
	if got[0].Name != "Apple" || got[1].Name != "Zebra" {
		t.Fatalf("wrong order: %#v", got)
	}
	if got[1].File != "/tmp/verdana-bundle.desktop" {
		t.Fatalf("lost the file it lives in: %#v", got[1])
	}
	if !reflect.DeepEqual(got[0].Entries, []ShortcutEntry{{NappID: "npub1def"}}) {
		t.Fatalf("entries wrong: %#v", got[0].Entries)
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
