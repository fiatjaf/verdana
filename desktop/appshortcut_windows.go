//go:build windows

package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"verdana/backend"

	"github.com/sergeymakinen/go-ico"
)

func syncAppShortcuts(shortcuts []backend.AppShortcut, exe string) error {
	programs, err := userStartMenuPrograms()
	if err != nil {
		return err
	}
	dir := filepath.Join(programs, "Verdana Apps")
	icons := appShortcutIconDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	desiredLinks := make(map[string]bool, len(shortcuts))
	desiredIcons := make(map[string]bool, len(shortcuts))
	nameCounts := make(map[string]int, len(shortcuts))
	for _, shortcut := range shortcuts {
		nameCounts[strings.ToLower(windowsShortcutName(shortcut.Name))]++
	}
	for _, shortcut := range shortcuts {
		key := appShortcutKey(shortcut.ID)
		iconPath := filepath.Join(icons, key+".ico")
		var icon bytes.Buffer
		if err := ico.Encode(&icon, appShortcutIcon(shortcut.Icon)); err != nil {
			return err
		}
		if err := writeAtomic(iconPath, icon.Bytes(), 0644); err != nil {
			return err
		}
		name := windowsShortcutName(shortcut.Name)
		if nameCounts[strings.ToLower(name)] > 1 {
			name += " (" + key[:6] + ")"
		}
		path := filepath.Join(dir, name+".lnk")
		arguments := `--background --launch-napp "` + strings.ReplaceAll(shortcut.ID, `"`, `\"`) + `"`
		ps := fmt.Sprintf(
			`$ws = New-Object -ComObject WScript.Shell; $s = $ws.CreateShortcut(%s); $s.TargetPath = %s; $s.Arguments = %s; $s.Description = %s; $s.IconLocation = %s; $s.Save()`,
			psSingleQuote(path), psSingleQuote(exe), psSingleQuote(arguments),
			psSingleQuote(appShortcutText(shortcut.Description)), psSingleQuote(iconPath+",0"),
		)
		if out, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", ps).CombinedOutput(); err != nil {
			return fmt.Errorf("creating app shortcut failed: %v: %s", err, out)
		}
		desiredLinks[path] = true
		desiredIcons[iconPath] = true
	}
	if err := removeStaleWindowsFiles(dir, ".lnk", desiredLinks); err != nil {
		return err
	}
	return removeStaleWindowsFiles(icons, ".ico", desiredIcons)
}

func windowsShortcutName(name string) string {
	name = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`<>:"/\|?*`, r) || r < 32 {
			return '-'
		}
		return r
	}, appShortcutText(name))
	name = strings.Trim(name, " .")
	if name == "" {
		return "Verdana App"
	}
	stem := strings.ToUpper(strings.TrimSuffix(name, filepath.Ext(name)))
	reserved := stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL" ||
		len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && stem[3] >= '1' && stem[3] <= '9'
	if reserved {
		name = "Verdana " + name
	}
	return name
}

func removeStaleWindowsFiles(dir, suffix string, desired map[string]bool) error {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		if !entry.IsDir() && strings.HasSuffix(strings.ToLower(entry.Name()), suffix) && !desired[path] {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}
