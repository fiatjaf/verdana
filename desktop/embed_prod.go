//go:build !dev

package main

import (
	"crypto/sha256"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
)

// The two napp shells, one per kind of window. They go into the launcher
// binary so a release build is one file that can open any napp.
//
//go:embed child/child
var childBinary []byte

//go:embed wasmchild/wasmchild
var wasmChildBinary []byte

// shellBinaries maps a shell's directory (which is also its file name) to
// the bytes to unpack.
var shellBinaries = map[string][]byte{
	"child":     childBinary,
	"wasmchild": wasmChildBinary,
}

// extractShell writes a shell to a temp directory and returns its path,
// named after the contents so a new build replaces the old one and an
// unchanged one is unpacked once.
func extractShell(name string) (string, error) {
	data, ok := shellBinaries[name]
	if !ok {
		return "", fmt.Errorf("no such napp shell: %s", name)
	}
	hash := sha256.Sum256(data)

	dir := filepath.Join(os.TempDir(), "verdana-"+name)
	path := filepath.Join(dir, fmt.Sprintf("%x", hash[:4]))

	if _, err := os.Stat(path); err == nil {
		return path, nil
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}

	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	if err := f.Chmod(0755); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	f.Close()
	return path, nil
}
