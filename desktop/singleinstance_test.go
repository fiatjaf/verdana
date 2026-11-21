package main

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"strings"
	"testing"
)

func TestReadForwardTarget(t *testing.T) {
	dir := t.TempDir()
	// legacy port-only file without secret is unusable
	if err := os.WriteFile(portFilePath(dir), []byte("41234"), 0600); err != nil {
		t.Fatal(err)
	}
	port, secret := readForwardTarget(dir)
	if port != 41234 || secret != "" {
		t.Fatalf("legacy file: got (%d, %q), want (41234, \"\")", port, secret)
	}

	if err := os.WriteFile(portFilePath(dir), []byte("41234:deadbeef\n"), 0600); err != nil {
		t.Fatal(err)
	}
	port, secret = readForwardTarget(dir)
	if port != 41234 || secret != "deadbeef" {
		t.Fatalf("valid file: got (%d, %q), want (41234, \"deadbeef\")", port, secret)
	}
}

func TestServeForwardRequiresSecretAndCapsSize(t *testing.T) {
	oldSecret := forwardSecret
	forwardSecret = "0123456789abcdef0123456789abcdef"
	t.Cleanup(func() { forwardSecret = oldSecret })

	send := func(payload string) string {
		c1, c2 := net.Pipe()
		done := make(chan struct{})
		go func() {
			defer close(done)
			serveForward(c2)
		}()
		_, _ = c1.Write([]byte(payload))
		ans, _ := bufio.NewReader(c1).ReadString('\n')
		c1.Close()
		<-done
		return strings.TrimSpace(ans)
	}

	if got := send(`{"token":"","secret":"wrong"}` + "\n"); got != "forbidden" {
		t.Fatalf("wrong secret got %q, want forbidden", got)
	}
	if got := send(`{"token":""}` + "\n"); got != "forbidden" {
		t.Fatalf("missing secret got %q, want forbidden", got)
	}
	okPayload, _ := json.Marshal(forwarded{Secret: forwardSecret})
	if got := send(string(okPayload) + "\n"); got != "ok" {
		t.Fatalf("valid secret got %q, want ok", got)
	}
	// oversized JSON payload (> maxForwardBytes) must fail to decode cleanly
	big := `{"secret":"` + forwardSecret + `","token":"` + strings.Repeat("a", maxForwardBytes+16) + `"}`
	if got := send(big); got != "" {
		t.Fatalf("oversized payload got %q, want closed without ok", got)
	}
}
