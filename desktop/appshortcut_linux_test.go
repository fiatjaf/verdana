//go:build linux

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"verdana/backend"
)

func TestSyncAppShortcutsCreatesAndReconcilesDesktopEntries(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", filepath.Join(dataDir, "xdg"))
	oldDataDir := appShortcutDataDir
	appShortcutDataDir = func() string { return filepath.Join(dataDir, "verdana") }
	t.Cleanup(func() { appShortcutDataDir = oldDataDir })

	first := backend.AppShortcut{ID: "napp-one", Name: "One", Description: "First app"}
	second := backend.AppShortcut{ID: "napplet-two", Name: "Two"}
	if err := syncAppShortcuts([]backend.AppShortcut{first, second}, "/opt/Verdana App/verdana"); err != nil {
		t.Fatal(err)
	}
	firstPath := filepath.Join(applicationsDir(), appShortcutPrefix+appShortcutKey(first.ID)+".desktop")
	raw, err := os.ReadFile(firstPath)
	if err != nil {
		t.Fatal(err)
	}
	entry := string(raw)
	if !strings.Contains(entry, `Exec="/opt/Verdana App/verdana" --background --launch-napp "napp-one"`) ||
		!strings.Contains(entry, "Name=One\n") {
		t.Fatalf("unexpected desktop entry:\n%s", entry)
	}

	if err := syncAppShortcuts([]backend.AppShortcut{second}, "/opt/verdana"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(firstPath); !os.IsNotExist(err) {
		t.Fatalf("stale desktop entry remains: %v", err)
	}
	if _, err := os.Stat(filepath.Join(appShortcutIconDir(), appShortcutKey(first.ID)+".png")); !os.IsNotExist(err) {
		t.Fatalf("stale icon remains: %v", err)
	}

	if err := syncAppShortcuts(nil, "/opt/verdana"); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(applicationsDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), appShortcutPrefix) {
			t.Fatalf("managed entry remains after disable: %s", entry.Name())
		}
	}
}
