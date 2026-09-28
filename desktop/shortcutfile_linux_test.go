//go:build linux

package main

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"verdana/backend"
)

func TestWriteShortcutFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))

	path, err := writeShortcutFile("My Bundle", "/usr/bin/verdana", "npub1abc +view:1 npub1def +compose")
	if err != nil {
		t.Fatal(err)
	}
	wantDir := filepath.Join(home, ".local", "share", "applications")
	if filepath.Dir(path) != wantDir {
		t.Fatalf("wrote to %s, want %s", path, wantDir)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, want := range []string{
		"Name=Verdana My Bundle\n",
		`Exec="/usr/bin/verdana" "npub1abc +view:1 npub1def +compose"`,
		"Type=Application\n",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("desktop file missing %q:\n%s", want, content)
		}
	}
}

// a token carrying a payload is base64url words: no quoting or escaping of
// its own may be needed in the Exec line.
func TestWriteShortcutFileWithPayloadToken(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))

	raw, err := json.Marshal(backend.ShortcutAction{Type: "view:1", Payload: json.RawMessage(`"nostr1abc"`)})
	if err != nil {
		t.Fatal(err)
	}
	token := "npub1abc +" + base64.RawURLEncoding.EncodeToString(raw)

	path, err := writeShortcutFile("Bundle", "/usr/bin/verdana", token)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `Exec="/usr/bin/verdana" "`+token+`"`) {
		t.Fatalf("bad exec line:\n%s", data)
	}
}

func TestShortcutSlugStable(t *testing.T) {
	if a, b := shortcutSlug("My Bundle"), shortcutSlug("My Bundle"); a != b {
		t.Fatalf("slug not stable: %q %q", a, b)
	}
	if a, b := shortcutSlug("My Bundle"), shortcutSlug("Other Bundle"); a == b {
		t.Fatalf("slugs collide: %q", a)
	}
}

// what is written has to read back as the same shortcut: the name off the
// Name= entry, the token off the Exec line.
func TestListShortcutFilesRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))

	raw, _ := json.Marshal(backend.ShortcutAction{Type: "view:1", Payload: json.RawMessage(`"nostr1abc"`)})
	token := "npub1abc +" + base64.RawURLEncoding.EncodeToString(raw)

	if _, err := writeShortcutFile("My Bundle", "/usr/bin/verdana", token); err != nil {
		t.Fatal(err)
	}
	// a stranger's file in the same folder is none of our business
	foreign := filepath.Join(home, ".local", "share", "applications", "firefox.desktop")
	if err := os.WriteFile(foreign, []byte("[Desktop Entry]\nExec=firefox\n"), 0644); err != nil {
		t.Fatal(err)
	}

	files := listShortcutFiles()
	if len(files) != 1 {
		t.Fatalf("expected one shortcut, got %#v", files)
	}
	if files[0].Name != "My Bundle" {
		t.Fatalf("name came back as %q", files[0].Name)
	}
	if files[0].Token != token {
		t.Fatalf("token came back as %q, want %q", files[0].Token, token)
	}
	if !strings.HasPrefix(filepath.Base(files[0].Path), shortcutPrefix) {
		t.Fatalf("file is not ours: %q", files[0].Path)
	}

	// and deleting it takes it off the list
	if err := deleteShortcutFile(files[0].Path); err != nil {
		t.Fatal(err)
	}
	if len(listShortcutFiles()) != 0 {
		t.Fatalf("still there: %#v", listShortcutFiles())
	}
}

// a token with a quote or a backslash in it must survive the Exec line.
func TestListShortcutFilesAwkwardToken(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))

	token := `npub1abc +view:1 "quoted" $HOME \back`
	if _, err := writeShortcutFile("Odd", "/opt/my apps/verdana", token); err != nil {
		t.Fatal(err)
	}
	files := listShortcutFiles()
	if len(files) != 1 {
		t.Fatalf("expected one shortcut, got %#v", files)
	}
	if files[0].Token != token {
		t.Fatalf("token came back as %q, want %q", files[0].Token, token)
	}
	if files[0].Name != "Odd" {
		t.Fatalf("name came back as %q", files[0].Name)
	}
}
