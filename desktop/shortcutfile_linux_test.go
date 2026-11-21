//go:build linux

package main

import (
	"strings"
	"testing"
)

func TestLinuxDesktopShortcutSanitization(t *testing.T) {
	evilName := "Notes\nExec=/bin/sh -c id\r\nHidden=false"
	clean := stripControlChars(evilName)
	if strings.ContainsAny(clean, "\r\n") {
		t.Fatalf("control chars survived in Name: %q", clean)
	}

	tok := "napp1 +view:%f\nExec=evil $HOME `id`"
	quoted := quoteExecField(tok)
	if strings.ContainsAny(quoted, "\r\n") {
		t.Fatalf("newlines survived in Exec field: %q", quoted)
	}
	if !strings.Contains(quoted, "%%f") {
		t.Fatalf("percent field code not escaped: %q", quoted)
	}
	entry := "[Desktop Entry]\nName=Verdana " + clean + "\nExec=/usr/bin/verdana " + quoted + "\n"
	bundle, parsedTok, ok := parseDesktopShortcut([]byte(entry))
	if !ok || bundle != clean || parsedTok != stripControlChars(tok) {
		t.Fatalf("round-trip mismatch: ok=%v bundle=%q tok=%q wantTok=%q", ok, bundle, parsedTok, stripControlChars(tok))
	}
}
