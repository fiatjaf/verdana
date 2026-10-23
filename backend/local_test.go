package backend

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func TestInstallLocal(t *testing.T) {
	prevDataDir, prevHost := dataDir, host
	dataDir, host = t.TempDir(), noopHost{}
	defer func() { dataDir, host = prevDataDir, prevHost }()

	b64 := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	files := []installLocalFile{
		{Path: "metadata.json", Data: b64(`{"id":"made","title":"Made"}`)},
		{Path: "/index.html", Data: b64("<p>hi</p>")},
		{Path: "js/app.js", Data: b64("1")},
	}
	caller := &Instance{napp: Napp{ID: "nostr~maker", Requires: []string{"dev"}}}

	if _, err := installLocalForNapp(&Instance{napp: Napp{ID: "nostr~other"}}, files); err == nil {
		t.Error("a napp without `dev` should be refused")
	}
	for _, bad := range []string{"../x", "a/../../x", "/", "a\\b"} {
		_, err := installLocalForNapp(caller, append(files[:2:2], installLocalFile{Path: bad, Data: b64("x")}))
		if err == nil {
			t.Errorf("path %q should be refused", bad)
		}
	}

	key := RuleKey{Napp: caller.napp.ID, Permission: PermDevInstall}
	setSessionRule(key, Rule{Decision: DecisionDeny})
	if res, err := installLocalForNapp(caller, files); res != nil || err != nil {
		t.Errorf("a denied install should be null, got %v %v", res, err)
	}

	setSessionRule(key, Rule{Decision: DecisionAllow})
	// no platform here, so the window can't open; the install itself stands
	installLocalForNapp(caller, files)
	clearSessionRule(key)
	defer Uninstall("local~made")
	n, ok := InstalledNapp("local~made")
	if !ok || !n.IsLocal() || n.InstalledBy != caller.napp.ID || len(n.Paths) != 3 {
		t.Fatalf("the local napp should be installed with its 3 files, got %+v", n)
	}
	dir := nappBaseDir(n.ID)
	if got, _ := os.ReadFile(filepath.Join(dir, "js", "app.js")); string(got) != "1" {
		t.Errorf("js/app.js should be written, got %q", got)
	}
	if folder, err := publishFolder(n.ID); err != nil || folder != dir {
		t.Errorf("a local napp should publish from its install dir, got %q %v", folder, err)
	}

	// the napp that installed it replaces it without asking (no rule is
	// set now: a prompt would hang), and the old files go
	installLocalForNapp(caller, files[:2])
	if _, err := os.Stat(filepath.Join(dir, "js", "app.js")); !os.IsNotExist(err) {
		t.Error("a reinstall should replace the folder, not merge into it")
	}
}
