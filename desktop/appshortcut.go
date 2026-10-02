package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"strings"
	"verdana/backend"

	_ "golang.org/x/image/webp"
)

// This deliberately does not share the "verdana-" prefix used by user-made
// bundle shortcuts, whose Linux discovery scans that namespace.
const appShortcutPrefix = "com.verdana.napp."

var appShortcutDataDir = backend.DataDir

func appShortcutKey(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:8])
}

func appShortcutText(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func appShortcutIcon(data []byte) image.Image {
	if len(data) > 0 {
		if im, _, err := image.Decode(bytes.NewReader(data)); err == nil {
			return im
		}
	}
	im, _, _ := image.Decode(bytes.NewReader(trayIcon()))
	return im
}

func appShortcutIconDir() string {
	return filepath.Join(appShortcutDataDir(), "app-shortcut-icons")
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".verdana-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
