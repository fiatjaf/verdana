//go:build linux

package main

import (
	"os"
	"strings"
	"testing"
)

func TestLinuxAutostart(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if autostartEnabled() {
		t.Fatal("autostart begins enabled")
	}
	if err := setAutostart(true, "/opt/Verdana App/verdana"); err != nil {
		t.Fatal(err)
	}
	if !autostartEnabled() {
		t.Fatal("autostart was not enabled")
	}
	raw, err := os.ReadFile(autostartPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `Exec="/opt/Verdana App/verdana" --background`) {
		t.Fatalf("unexpected desktop entry:\n%s", raw)
	}
	if err := setAutostart(false, ""); err != nil {
		t.Fatal(err)
	}
	if autostartEnabled() {
		t.Fatal("autostart remains enabled")
	}
}
