package main

import (
	"os"
	"testing"
)

func TestStopInstanceListenerOnlyRemovesOwnPortFile(t *testing.T) {
	dir := t.TempDir()
	startInstanceListener(dir)
	p := portFilePath(dir)
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("expected port file to exist: %v", err)
	}
	stopInstanceListener()
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("expected own port file to be removed, err=%v", err)
	}

	// If another launcher overwrites launcher.port before we exit, we must
	// not delete the newer launcher's file.
	startInstanceListener(dir)
	if err := os.WriteFile(p, []byte("55555"), 0600); err != nil {
		t.Fatal(err)
	}
	stopInstanceListener()
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("newer launcher port file should not have been deleted: %v", err)
	}
	if string(got) != "55555" {
		t.Fatalf("port file content = %q, want 55555", string(got))
	}
}
