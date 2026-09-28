//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"verdana/backend"
)

// writeShortcutFile writes a .lnk in the user's Start Menu Programs folder
// with WScript.Shell through powershell (every Windows has both). The link's
// Arguments is the bundle token as one argument. COM writes the file, so the
// link is exactly what Windows expects; the bundle's name goes into its
// NAME_STRING afterwards, since what COM puts there is the launcher's own file
// name.
func writeShortcutFile(name, exe, token string) (string, error) {
	startMenu, err := userStartMenuPrograms()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(startMenu, 0755); err != nil {
		return "", err
	}
	target := filepath.Join(startMenu, shortcutPrefix+shortcutSlug(name)+".lnk")
	ps := fmt.Sprintf(
		`$ws = New-Object -ComObject WScript.Shell; $s = $ws.CreateShortcut('%s'); $s.TargetPath = '%s'; $s.Arguments = '%s'; $s.Save()`,
		target, psSingleQuote(exe), psSingleQuote(token),
	)
	if out, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", ps).CombinedOutput(); err != nil {
		return target, fmt.Errorf("creating shortcut failed: %v: %s", err, out)
	}
	if err := nameLnkFile(target, name); err != nil {
		log.Warn().Err(err).Str("path", target).Msg("could not name the shortcut file")
	}
	return target, nil
}

// nameLnkFile writes the bundle's name into a link COM just wrote. The Start
// Menu shows the file name, which is only a slug, so the real name lives in
// the file itself: that is what reading a shortcut back uses.
func nameLnkFile(path, name string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	named, err := withLnkName(data, name)
	if err != nil {
		return err
	}
	if string(named) == string(data) {
		return nil
	}
	return os.WriteFile(path, named, 0644)
}

func deleteShortcutFile(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// listShortcutFiles reads back every verdana-*.lnk in the Start Menu: the name
// off the link's NAME_STRING (or its file-name slug when it has none) and the
// token off the arguments the link runs.
func listShortcutFiles() []backend.ShortcutFile {
	startMenu, err := userStartMenuPrograms()
	if err != nil {
		return nil
	}
	entries, err := os.ReadDir(startMenu)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Warn().Err(err).Str("dir", startMenu).Msg("could not read the start menu")
		}
		return nil
	}
	var out []backend.ShortcutFile
	for _, e := range entries {
		fileName := e.Name()
		if e.IsDir() || !strings.HasPrefix(fileName, shortcutPrefix) || !strings.HasSuffix(fileName, ".lnk") {
			continue
		}
		path := filepath.Join(startMenu, fileName)
		data, err := os.ReadFile(path)
		if err != nil {
			log.Warn().Err(err).Str("path", fileName).Msg("could not read a shortcut file")
			continue
		}
		name, _ := lnkName(data)
		if name == "" {
			name = lnkSlugName(fileName)
		}
		token, err := lnkArguments(data)
		if err != nil {
			log.Warn().Err(err).Str("path", fileName).Msg("could not read a shortcut link")
			continue
		}
		if strings.TrimSpace(token) == "" {
			log.Warn().Str("path", fileName).Msg("shortcut link has no bundle token")
			continue
		}
		out = append(out, backend.ShortcutFile{Name: name, Path: path, Token: strings.TrimSpace(token)})
	}
	return out
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
