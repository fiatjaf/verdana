package backend

import (
	"os"
	"path/filepath"
	"testing"
)

// A wasm napp is a directory: metadata.json and an app.wasm beside it, no
// index.html. What the folder is is its files — there is nothing to name, so
// a folder that says nothing is still unambiguous.
func TestDevFolderKinds(t *testing.T) {
	wasm, err := readDevFolder("testdata/wasmnapp")
	if err != nil {
		t.Fatalf("a metadata.json + app.wasm folder should load: %v", err)
	}
	if wasm.KindOf() != KindWasm {
		t.Errorf("a metadata.json + app.wasm folder should be kind 35131, got %d", wasm.KindOf())
	}
	devNapps.Store(wasm.ID, &devNapp{napp: wasm, source: "folder", dir: "testdata/wasmnapp"})
	defer devNapps.Delete(wasm.ID)
	if f, ok := wasm.WasmFile(); !ok || f == "" {
		t.Error("WasmFile should resolve to the folder's app.wasm")
	}

	web, err := readDevFolder("testdata/webnapp")
	if err != nil {
		t.Fatalf("a metadata.json + index.html folder should load: %v", err)
	}
	if web.KindOf() != KindWeb {
		t.Errorf("a folder with index.html should stay a page, got %d", web.KindOf())
	}
}

// Symlinked files and folders are part of the napp they appear in, under the
// link's own path; a link that loops back must not trap the walk.
func TestDevFolderFollowsSymlinks(t *testing.T) {
	dir := t.TempDir()
	external := t.TempDir()
	write := func(p, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(dir, "metadata.json"), `{"id":"links","title":"Links"}`)
	write(filepath.Join(dir, "index.html"), "index")
	write(filepath.Join(external, "linked.txt"), "linked file")
	write(filepath.Join(external, "sub", "nested.txt"), "nested")

	link := func(target, name string) {
		t.Helper()
		if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
			t.Skipf("symlinks are not available: %v", err)
		}
	}
	link(filepath.Join(external, "linked.txt"), "linked.txt")
	link(filepath.Join(external, "sub"), "linked-dir")
	link(dir, "loop")

	napp, err := readDevFolder(dir)
	if err != nil {
		t.Fatalf("a folder with symlinks should load: %v", err)
	}
	got := make(map[string]bool, len(napp.Paths))
	for _, p := range napp.Paths {
		got[p.Path] = true
	}
	for _, want := range []string{"/index.html", "/linked.txt", "/linked-dir/nested.txt"} {
		if !got[want] {
			t.Errorf("expected %s to be indexed, got %v", want, got)
		}
	}
	if got["/loop"] || got["/loop/index.html"] {
		t.Errorf("a looping symlink should be skipped, got %v", got)
	}
}
