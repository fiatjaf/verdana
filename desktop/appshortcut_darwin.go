//go:build darwin

package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"verdana/backend"

	"github.com/jackmordaunt/icns/v3"
)

func syncAppShortcuts(shortcuts []backend.AppShortcut, exe string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(home, "Applications", "Verdana Apps")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	desired := make(map[string]bool, len(shortcuts))
	nameCounts := make(map[string]int, len(shortcuts))
	for _, shortcut := range shortcuts {
		nameCounts[strings.ToLower(darwinShortcutName(shortcut.Name))]++
	}
	for _, shortcut := range shortcuts {
		key := appShortcutKey(shortcut.ID)
		name := darwinShortcutName(shortcut.Name)
		if nameCounts[strings.ToLower(name)] > 1 {
			name += " (" + key[:6] + ")"
		}
		appDir := filepath.Join(dir, name+".app")
		macOSDir := filepath.Join(appDir, "Contents", "MacOS")
		resourcesDir := filepath.Join(appDir, "Contents", "Resources")
		if err := os.MkdirAll(macOSDir, 0755); err != nil {
			return err
		}
		if err := os.MkdirAll(resourcesDir, 0755); err != nil {
			return err
		}
		var icon bytes.Buffer
		if err := icns.Encode(&icon, appShortcutIcon(shortcut.Icon)); err != nil {
			return err
		}
		if err := writeAtomic(filepath.Join(resourcesDir, "AppIcon.icns"), icon.Bytes(), 0644); err != nil {
			return err
		}
		plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleExecutable</key><string>launch</string>
<key>CFBundleIdentifier</key><string>com.verdana.napp.%s</string>
<key>CFBundleName</key><string>%s</string>
<key>CFBundleDisplayName</key><string>%s</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>CFBundleIconFile</key><string>AppIcon.icns</string>
<key>LSUIElement</key><true/>
</dict></plist>
`, key, xmlEscape(name), xmlEscape(name))
		if err := writeAtomic(filepath.Join(appDir, "Contents", "Info.plist"), []byte(plist), 0644); err != nil {
			return err
		}
		script := "#!/bin/sh\nexec " + shellQuote(exe) + " --background --launch-napp " + shellQuote(shortcut.ID) + "\n"
		if err := writeAtomic(filepath.Join(macOSDir, "launch"), []byte(script), 0755); err != nil {
			return err
		}
		desired[appDir] = true
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		if entry.IsDir() && strings.HasSuffix(entry.Name(), ".app") && !desired[path] {
			if err := os.RemoveAll(path); err != nil {
				return err
			}
		}
	}
	return nil
}

func darwinShortcutName(name string) string {
	name = strings.Map(func(r rune) rune {
		if r == '/' || r == ':' || r == 0 {
			return '-'
		}
		return r
	}, appShortcutText(name))
	if strings.TrimSpace(name) == "" {
		return "Verdana App"
	}
	return name
}
