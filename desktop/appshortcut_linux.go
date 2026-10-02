//go:build linux

package main

import (
	"bytes"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"verdana/backend"
)

func syncAppShortcuts(shortcuts []backend.AppShortcut, exe string) error {
	dir := applicationsDir()
	icons := appShortcutIconDir()
	desiredFiles := make(map[string]bool, len(shortcuts))
	desiredIcons := make(map[string]bool, len(shortcuts))
	for _, shortcut := range shortcuts {
		key := appShortcutKey(shortcut.ID)
		iconPath := filepath.Join(icons, key+".png")
		var icon bytes.Buffer
		if err := png.Encode(&icon, appShortcutIcon(shortcut.Icon)); err != nil {
			return err
		}
		if err := writeAtomic(iconPath, icon.Bytes(), 0644); err != nil {
			return err
		}
		path := filepath.Join(dir, appShortcutPrefix+key+".desktop")
		data := fmt.Sprintf(`[Desktop Entry]
Version=1.0
Type=Application
Name=%s
Comment=%s
Exec=%s --background --launch-napp %s
Icon=%s
Terminal=false
Categories=Network;
X-Verdana-Napp-ID=%s
`, appShortcutText(shortcut.Name), appShortcutText(shortcut.Description), quoteExecField(exe), quoteExecField(shortcut.ID), iconPath, shortcut.ID)
		if err := writeAtomic(path, []byte(data), 0644); err != nil {
			return err
		}
		desiredFiles[path] = true
		desiredIcons[iconPath] = true
	}
	if err := removeStaleAppShortcutFiles(dir, ".desktop", desiredFiles); err != nil {
		return err
	}
	if err := removeStaleAppShortcutFiles(icons, ".png", desiredIcons); err != nil {
		return err
	}
	refreshShortcutParent(dir)
	return nil
}

func removeStaleAppShortcutFiles(dir, suffix string, desired map[string]bool) error {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	icons := dir == appShortcutIconDir()
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), suffix) {
			continue
		}
		if !icons && !strings.HasPrefix(entry.Name(), appShortcutPrefix) {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if !desired[path] {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}
