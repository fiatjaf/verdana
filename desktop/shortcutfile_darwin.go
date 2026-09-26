//go:build darwin

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// writeShortcutFile writes a minimal .app bundle into ~/Applications: a
// stub Info.plist and a launcher script whose only line is calling verdana
// with the bundle token. LaunchServices picks bundles up on their own.
func writeShortcutFile(name, exe, token string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	slug := shortcutSlug(name)
	appDir := filepath.Join(home, "Applications", "Verdana-"+slug+".app")
	if err := os.MkdirAll(filepath.Join(appDir, "Contents", "MacOS"), 0755); err != nil {
		return "", err
	}

	id := "com.verdana.shortcut." + slug
	if err := os.WriteFile(filepath.Join(appDir, "Contents", "Info.plist"), []byte(fmt.Sprintf(
		`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleExecutable</key><string>%s</string>
	<key>CFBundleIdentifier</key><string>%s</string>
	<key>CFBundleName</key><string>%s</string>
	<key>CFBundlePackageType</key><string>APPL</string>
	<key>CFBundleShortVersionString</key><string>1.0</string>
</dict>
</plist>
`, slug, xmlEscape(id), xmlEscape(name))), 0644); err != nil {
		return "", err
	}

	script := "#!/bin/sh\nexec " + shellQuote(exe) + " " + shellQuote(token) + "\n"
	elem := filepath.Join(appDir, "Contents", "MacOS", slug)
	if err := os.WriteFile(elem, []byte(script), 0755); err != nil {
		return "", err
	}
	refreshShortcutParent("")
	return appDir, nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}
