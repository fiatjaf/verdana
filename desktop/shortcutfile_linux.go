//go:build linux

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// writeShortcutFile writes a freedesktop .desktop entry into the user's
// applications directory. Its only job is Exec="<verdana> <token>": running
// the launcher, which forwards the token to the already-running instance (or
// handles it itself when there is none).
func writeShortcutFile(name, exe, token string) (string, error) {
	if !strings.HasPrefix(exe, "/") {
		// some launchers install by symlink into PATH dirs; absolute or not,
		// the .desktop file only understands what it can run as-is.
		if resolved, err := filepath.Abs(exe); err == nil {
			exe = resolved
		}
	}
	data := fmt.Sprintf(desktopTemplate, name, quoteExecField(exe), quoteExecField(token))
	if err := os.MkdirAll(applicationsDir(), 0755); err != nil {
		return "", err
	}
	path := filepath.Join(applicationsDir(), "verdana-"+shortcutSlug(name)+".desktop")
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		return "", err
	}
	refreshShortcutParent(applicationsDir())
	return path, nil
}

func applicationsDir() string {
	if base := os.Getenv("XDG_DATA_HOME"); base != "" {
		return filepath.Join(base, "applications")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "applications"
	}
	return filepath.Join(home, ".local", "share", "applications")
}

// quoteExecField wraps one value of an Exec= line, per the desktop entry
// spec: double quotes with backslash escapes on the special characters.
func quoteExecField(value string) string {
	escaped := strings.NewReplacer(
		`\`, `\\`,
		`"`, `\"`,
		"`", "\\`",
		`$`, `\$`,
	).Replace(value)
	return `"` + escaped + `"`
}

const desktopTemplate = `[Desktop Entry]
Type=Application
Name=Verdana %s
Comment=Verdana bundle shortcut
Exec=%s %s
Terminal=false
Categories=Network;
StartupWMClass=Verdana
`
