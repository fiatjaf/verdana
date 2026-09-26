//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// writeShortcutFile writes a .lnk in the user's Start Menu Programs folder
// with WScript.Shell through powershell (every Windows has both). The link's
// Arguments is the bundle token as one argument.
func writeShortcutFile(name, exe, token string) (string, error) {
	startMenu, err := userStartMenuPrograms()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(startMenu, 0755); err != nil {
		return "", err
	}
	target := filepath.Join(startMenu, shortcutSlug(name)+".lnk")
	ps := fmt.Sprintf(
		`$ws = New-Object -ComObject WScript.Shell; $s = $ws.CreateShortcut('%s'); $s.TargetPath = '%s'; $s.Arguments = '%s'; $s.Save()`,
		target, psSingleQuote(exe), psSingleQuote(token),
	)
	if out, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", ps).CombinedOutput(); err != nil {
		return target, fmt.Errorf("creating shortcut failed: %v: %s", err, out)
	}
	return target, nil
}

// psSingleQuote wraps a string in the powershell single-quote literal form.
func psSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func userStartMenuPrograms() (string, error) {
	appdata, err := os.UserConfigDir() // %APPDATA%
	if err != nil {
		return "", err
	}
	return filepath.Join(appdata, "Microsoft", "Windows", "Start Menu", "Programs"), nil
}
