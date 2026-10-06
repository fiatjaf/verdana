package main

import (
	"bytes"
	_ "embed"
	"os"
	"path/filepath"
	"runtime"
)

// The app icon, embedded in the launcher binary.
//
// Gio has no window-icon call, so nothing in the running window can use these:
// what needs a picture of the app is the OS, in the shortcut files we write for
// a bundle (see shortcutfile_<goos>.go), and those can only name a file on
// disk. So the icon travels inside the binary and is unpacked on demand, the
// same way the napp shells are (embed_prod.go) -- except to a fixed path rather
// than a content-hashed one, because a shortcut written last week still has to
// find its icon today.
//
// Regenerate all three with `just icons`.

var (
	//go:embed assets/icon.png
	iconPNG []byte
	//go:embed assets/icon.ico
	iconICO []byte
	//go:embed assets/icon.icns
	iconICNS []byte
)

// iconFile writes the icon in this platform's format to the user's cache
// directory and returns its path, or "" when there is nowhere to put it -- in
// which case the caller writes a shortcut with no icon rather than none at all.
//
// The write is skipped when the file is already there with the same bytes, so
// this is cheap enough to call for every shortcut.
func iconFile() string {
	name, data := iconForPlatform()
	if name == "" {
		return ""
	}

	dir, err := os.UserCacheDir()
	if err != nil {
		log.Warn().Err(err).Msg("no cache directory, shortcuts will have no icon")
		return ""
	}
	dir = filepath.Join(dir, "verdana")
	if err := os.MkdirAll(dir, 0755); err != nil {
		log.Warn().Err(err).Str("dir", dir).Msg("could not create the icon directory")
		return ""
	}

	path := filepath.Join(dir, name)
	if existing, err := os.ReadFile(path); err != nil || !bytes.Equal(existing, data) {
		if err := os.WriteFile(path, data, 0644); err != nil {
			log.Warn().Err(err).Str("path", path).Msg("could not write the app icon")
			return ""
		}
	}
	return path
}

// iconForPlatform picks the format this OS reads an icon from: an .ico on
// Windows, an .icns in a macOS bundle, a plain .png everywhere else.
func iconForPlatform() (name string, data []byte) {
	switch runtime.GOOS {
	case "windows":
		return "verdana.ico", iconICO
	case "darwin":
		return "verdana.icns", iconICNS
	default:
		return "verdana.png", iconPNG
	}
}
