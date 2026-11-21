package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// userPrivateCacheDir returns a user-private (0700) subdirectory under the
// user's cache dir (falling back to a private dir under UserHomeDir), never a
// shared world-writable /tmp directory.
func userPrivateCacheDir(parts ...string) (string, error) {
	base, err := os.UserCacheDir()
	if err != nil || strings.TrimSpace(base) == "" {
		home, herr := os.UserHomeDir()
		if herr != nil {
			return "", fmt.Errorf("cannot locate user cache or home directory: %w", err)
		}
		base = filepath.Join(home, ".cache")
	}
	root := filepath.Join(base, "verdana")
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", err
	}
	_ = os.Chmod(root, 0700)
	dir := filepath.Join(append([]string{root}, parts...)...)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	_ = os.Chmod(dir, 0700)
	return dir, nil
}

// extractBinary unpacks an embedded executable into dir/<sha256>, verifying
// full SHA-256 and regular-file status before reusing an existing copy, and
// writing atomically via CreateTemp + Chmod(0700) + Rename otherwise.
func extractBinary(dir string, data []byte) (string, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	_ = os.Chmod(dir, 0700)

	want := sha256.Sum256(data)
	path := filepath.Join(dir, fmt.Sprintf("%x", want))

	if st, err := os.Lstat(path); err == nil && st.Mode().IsRegular() {
		if existing, rerr := os.ReadFile(path); rerr == nil && sha256.Sum256(existing) == want {
			_ = os.Chmod(path, 0700)
			return path, nil
		}
	}

	tmp, err := os.CreateTemp(dir, ".extract-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}
	if err := tmp.Chmod(0700); err != nil {
		cleanup()
		return "", err
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return "", err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		// Another instance may have extracted the identical binary concurrently
		// (notably on Windows where Rename fails if destination already exists).
		if st, serr := os.Lstat(path); serr == nil && st.Mode().IsRegular() {
			if existing, rerr := os.ReadFile(path); rerr == nil && sha256.Sum256(existing) == want {
				return path, nil
			}
		}
		return "", err
	}
	return path, nil
}

// withPrivateTempEnv replaces TMPDIR/TEMP/TMP in env with a user-private 0700
// directory so third-party libraries that unpack shared libraries into
// os.TempDir() (notably abemedia/go-webview/embedded) never touch world-writable
// /tmp directories.
func withPrivateTempEnv(env []string, tmpDir string) []string {
	out := make([]string, 0, len(env)+3)
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(k) {
		case "TMPDIR", "TEMP", "TMP":
			continue
		}
		out = append(out, kv)
	}
	return append(out,
		"TMPDIR="+tmpDir,
		"TEMP="+tmpDir,
		"TMP="+tmpDir,
	)
}
