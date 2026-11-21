package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestExtractBinaryReplacesTamperedAndSymlink(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "bin")
	payload := []byte("#!/bin/sh\necho ok\n")

	p, err := extractBinary(dir, payload)
	if err != nil {
		t.Fatalf("extractBinary: %v", err)
	}
	st, err := os.Lstat(p)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && st.Mode().Perm() != 0700 {
		t.Fatalf("mode = %o, want 0700", st.Mode().Perm())
	}

	// Tamper with extracted file: next call must detect SHA-256 mismatch and restore
	if err := os.WriteFile(p, []byte("evil"), 0700); err != nil {
		t.Fatal(err)
	}
	p2, err := extractBinary(dir, payload)
	if err != nil || p2 != p {
		t.Fatalf("extractBinary after tamper: %q %v", p2, err)
	}
	got, _ := os.ReadFile(p2)
	if string(got) != string(payload) {
		t.Fatalf("tampered file was not restored: got %q", got)
	}

	// Replace with symlink: next call must atomically rename over symlink without following it
	target := filepath.Join(t.TempDir(), "victim")
	_ = os.WriteFile(target, []byte("keep"), 0600)
	_ = os.Remove(p)
	if err := os.Symlink(target, p); err == nil {
		if _, err := extractBinary(dir, payload); err != nil {
			t.Fatalf("extractBinary over symlink: %v", err)
		}
		victim, _ := os.ReadFile(target)
		if string(victim) != "keep" {
			t.Fatal("extractBinary followed symlink and overwrote victim file")
		}
	}
}

func TestWithPrivateTempEnv(t *testing.T) {
	env := withPrivateTempEnv([]string{"PATH=/usr/bin", "TMPDIR=/tmp", "TEMP=/tmp", "HOME=/home/u"}, "/home/u/.cache/verdana/tmp")
	joined := strings.Join(env, "\n")
	if strings.Contains(joined, "TMPDIR=/tmp\n") || strings.Contains(joined, "TEMP=/tmp\n") {
		t.Fatalf("old temp vars not replaced: %v", env)
	}
	if !strings.Contains(joined, "TMPDIR=/home/u/.cache/verdana/tmp") {
		t.Fatalf("missing private TMPDIR: %v", env)
	}
}

func TestBuildWindowsShortcutPSNoDoubleQuoting(t *testing.T) {
	ps := buildWindowsShortcutPS(
		`C:\Users\o'brien\Start\verdana-x.lnk`,
		`C:\Program Files\Verdana\verdana.exe`,
		`napp +action; calc.exe; 'whoami'`,
		`C:\Users\o'brien\verdana.ico`,
	)
	// Must never wrap psSingleQuote in extra single quotes (''...'' unquotes the argument!)
	if strings.Contains(ps, "CreateShortcut(''") || strings.Contains(ps, "= ''") {
		t.Fatalf("double-single-quote PowerShell injection bug present: %s", ps)
	}
	// Single quotes inside arguments must be doubled inside single-quoted strings
	if !strings.Contains(ps, `o''brien`) || !strings.Contains(ps, `''whoami''`) {
		t.Fatalf("embedded quotes not escaped properly: %s", ps)
	}
	if encodePSCommand(ps) == "" {
		t.Fatal("encodePSCommand returned empty")
	}
}
