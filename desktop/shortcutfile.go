package main

import (
	"fmt"
	"hash/fnv"
	"os/exec"
	"strings"
)

// Shared scratch for the OS shortcut file writers and readers
// (shortcutfile_<goos>.go): the prefix every file we write carries, a
// predictable filename slug — the bundle name may be any string — and whatever
// purified-air pokes the desktop environments need.

// shortcutPrefix is the first thing every file name we write starts with, so
// reading them back is "everything in the shortcut folder that starts with
// this" and a stranger's file is never mistaken for ours.
const shortcutPrefix = "verdana-"

// shortcutSlug turns a bundle name into a filename-friendly, lowercase slug
// with a short hash suffix, so a renamed shortcut writes a fresh file while
// the same name updates the very same file.
func shortcutSlug(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		case r == ' ':
			b.WriteByte('-')
		}
	}
	if b.Len() == 0 {
		b.WriteString("bundle")
	}
	h := fnv.New32a()
	fmt.Fprint(h, name)
	return b.String() + "-" + fmt.Sprintf("%x", h.Sum32())
}

// refreshShortcutParent tells desktop environments to re-read a directory of
// shortcut files, when that is a thing it does.
func refreshShortcutParent(dir string) {
	if dir == "" {
		return
	}
	if _, err := exec.LookPath("update-desktop-database"); err == nil {
		exec.Command("update-desktop-database", dir).Start()
	}
}
