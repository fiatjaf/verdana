//go:build !dev

package main

import (
	"crypto/sha256"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed child/child
var childBinary []byte

func extractChild() (string, error) {
	hash := sha256.Sum256(childBinary)
	name := fmt.Sprintf("%x", hash[:4])

	dir := filepath.Join(os.TempDir(), "verdana-child")
	path := filepath.Join(dir, name)

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
	if _, err := f.Write(childBinary); err != nil {
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
