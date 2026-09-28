//go:build linux

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"verdana/backend"
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
	path := filepath.Join(applicationsDir(), shortcutPrefix+shortcutSlug(name)+".desktop")
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		return "", err
	}
	refreshShortcutParent(applicationsDir())
	return path, nil
}

func deleteShortcutFile(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// listShortcutFiles reads back every verdana-*.desktop in the applications
// directory: ours to begin with, so nothing else is even opened.
func listShortcutFiles() []backend.ShortcutFile {
	dir := applicationsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		log.Warn().Err(err).Str("dir", dir).Msg("could not read the applications directory")
		return nil
	}
	var out []backend.ShortcutFile
	for _, e := range entries {
		name := e.Name()

		if e.IsDir() || !strings.HasPrefix(name, shortcutPrefix) || !strings.HasSuffix(name, ".desktop") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			log.Warn().Err(err).Str("path", name).Msg("could not read a shortcut file")
			continue
		}
		bundle, token, ok := parseDesktopShortcut(data)
		if !ok {
			log.Warn().Str("path", name).Msg("shortcut file has no bundle token")
			continue
		}
		out = append(out, backend.ShortcutFile{
			Name:  bundle,
			Path:  filepath.Join(dir, name),
			Token: token,
		})
	}
	return out
}

// parseDesktopShortcut pulls the bundle name and its token back out of a file
// writeShortcutFile wrote: the name off the "Verdana <name>" entry, the token
// as the second field of the Exec line (the first being the launcher itself).
func parseDesktopShortcut(data []byte) (bundle string, token string, ok bool) {
	var execLine string
	for _, line := range strings.Split(string(data), "\n") {
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		switch strings.TrimSpace(key) {
		case "Name":
			if bundle = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(value), "Verdana")); bundle != "" {
				bundle = strings.TrimSpace(bundle)
			}
		case "Exec":
			execLine = value
		}
	}
	fields := splitExecFields(execLine)
	if len(fields) < 2 || strings.TrimSpace(fields[len(fields)-1]) == "" {
		return bundle, "", false
	}
	return bundle, fields[len(fields)-1], true
}

// splitExecFields splits a desktop entry Exec line into its fields, undoing
// the quoting quoteExecField applies.
func splitExecFields(line string) []string {
	var fields []string
	var cur strings.Builder
	inQuotes := false
	escaped := false
	for _, r := range line {
		switch {
		case escaped:
			cur.WriteRune(r)
			escaped = false
		case r == '\\':
			escaped = true
		case r == '"':
			inQuotes = !inQuotes
		case (r == ' ' || r == '\t') && !inQuotes:
			if cur.Len() > 0 {
				fields = append(fields, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		fields = append(fields, cur.String())
	}
	return fields
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
