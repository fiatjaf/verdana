//go:build !dev

package main

import (
	_ "embed"
	"fmt"
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

// extractShell writes a shell to a user-private cache directory (0700) and
// returns its path, verified by full SHA-256 and written atomically so no
// other local user can pre-create or tamper with the extracted binary.
func extractShell(name string) (string, error) {
	data, ok := shellBinaries[name]
	if !ok {
		return "", fmt.Errorf("no such napp shell: %s", name)
	}
	dir, err := userPrivateCacheDir("bin", name)
	if err != nil {
		return "", err
	}
	return extractBinary(dir, data)
}
