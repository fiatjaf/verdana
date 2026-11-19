package backend

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A state file written by an older build can carry raw key material in the
// login field: loading it must drop the secret, and write the file back
// without it.
func TestLoadStateDropsSecretLogin(t *testing.T) {
	dir := t.TempDir()
	nsec := "nsec1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqz"
	raw, _ := json.Marshal(map[string]any{
		"client_key": "0102030405060708090a0b0c0d0e0f100102030405060708090a0b0c0d0e0f10",
		"login":      nsec,
		"relays":     []string{"relay.example.com"},
	})
	statePath = filepath.Join(dir, "state.json")
	if err := os.WriteFile(statePath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { statePath = "" })

	state = AppState{}
	loadState()
	if StoredLogin() != "" {
		t.Fatalf("a secret login survived the load: %q", StoredLogin())
	}
	back, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(back) == string(raw) {
		t.Fatal("the state file was rewritten with the secret still in it")
	}
	if strings.Contains(string(back), nsec) {
		t.Fatal("the secret is still in the rewritten state file")
	}
}
