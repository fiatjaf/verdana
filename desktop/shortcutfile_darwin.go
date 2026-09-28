//go:build darwin

package main

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"verdana/backend"
)

// writeShortcutFile writes a minimal .app bundle into ~/Applications: a
// stub Info.plist and a launcher script whose only line is calling verdana
// with the bundle token. LaunchServices picks bundles up on their own.
func writeShortcutFile(name, exe, token string) (string, error) {
	slug := shortcutSlug(name)
	appDir, err := shortcutBundleDir(slug)
	if err != nil {
		return "", err
	}
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

func shortcutBundleDir(slug string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Applications", shortcutPrefix+slug+".app"), nil
}

// deleteShortcutFile takes the whole bundle away: an .app is a directory.
func deleteShortcutFile(path string) error {
	if err := os.RemoveAll(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// listShortcutFiles reads back every verdana-*.app in ~/Applications: the name
// out of the Info.plist, the token out of the stub script.
func listShortcutFiles() []backend.ShortcutFile {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	dir := filepath.Join(home, "Applications")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Warn().Err(err).Str("dir", dir).Msg("could not read the applications directory")
		}
		return nil
	}
	var out []backend.ShortcutFile
	for _, e := range entries {
		fileName := e.Name()
		if !e.IsDir() || !strings.HasPrefix(fileName, shortcutPrefix) || !strings.HasSuffix(fileName, ".app") {
			continue
		}
		appDir := filepath.Join(dir, fileName)
		slug := strings.TrimSuffix(strings.TrimPrefix(fileName, shortcutPrefix), ".app")
		bundle, err := plistBundleName(filepath.Join(appDir, "Contents", "Info.plist"))
		if err != nil {
			log.Warn().Err(err).Str("path", fileName).Msg("could not read a shortcut bundle name")
			continue
		}
		token, ok := scriptToken(filepath.Join(appDir, "Contents", "MacOS", slug))
		if !ok {
			log.Warn().Str("path", fileName).Msg("shortcut bundle has no token")
			continue
		}
		out = append(out, backend.ShortcutFile{Name: bundle, Path: appDir, Token: token})
	}
	return out
}

// plistBundleName pulls CFBundleName out of an Info.plist. The plist is ours
// and tiny, so a plain XML walk does the job.
func plistBundleName(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var plist struct {
		Dict struct {
			Keys   []string `xml:"key"`
			Values []string `xml:"string"`
		} `xml:"dict"`
	}
	if err := xml.Unmarshal(data, &plist); err != nil {
		return "", err
	}
	for i, k := range plist.Dict.Keys {
		if k == "CFBundleName" && i < len(plist.Dict.Values) {
			return plist.Dict.Values[i], nil
		}
	}
	return "", nil
}

// scriptToken pulls the bundle token back out of the stub script: "exec" and
// the launcher path, then the one argument that is ours.
func scriptToken(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	fields := shellFields(string(data))
	for i := len(fields) - 1; i >= 0; i-- {
		if fields[i] != "exec" {
			return fields[i], fields[i] != ""
		}
	}
	return "", false
}

// shellFields splits a shell line into its single-quoted words, undoing the
// quoting shellQuote applies.
func shellFields(line string) []string {
	var fields []string
	var cur strings.Builder
	inQuotes := false
	started := false
	for i := 0; i < len(line); i++ {
		switch c := line[i]; {
		case c == '\'':
			inQuotes = !inQuotes
			started = true
		case !inQuotes && (c == ' ' || c == '\n' || c == '\t' || c == '\r'):
			if started {
				fields = append(fields, cur.String())
				cur.Reset()
				started = false
			}
		case !inQuotes && c == '\\' && i+1 < len(line):
			i++
			cur.WriteByte(line[i])
			started = true
		default:
			cur.WriteByte(c)
			started = true
		}
	}
	if started {
		fields = append(fields, cur.String())
	}
	return fields
}

func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}
