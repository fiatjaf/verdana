package backend

import "testing"

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
