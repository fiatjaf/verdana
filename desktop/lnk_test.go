package main

import (
	"encoding/binary"
	"strings"
	"testing"
	"unicode/utf16"
)

// buildLnk lays out a .lnk the way COM does: the fixed header, an optional
// target id list, an optional link info block, one extra data block, then the
// strings, each with a character count. tests build links with it and the
// parser has to find the same strings in them.
func buildLnk(flags uint32, idList []byte, linkInfo []byte, extra []byte, strings_ ...string) []byte {
	out := make([]byte, 0, 0x4C)
	header := make([]byte, 0x4C)
	binary.LittleEndian.PutUint32(header[0:4], 0x4C)
	binary.LittleEndian.PutUint32(header[4:8], flags)
	binary.LittleEndian.PutUint32(header[0x10:], 0x13000000) // a file size of the sort
	out = append(out, header...)

	if idList != nil {
		var size [2]byte
		binary.LittleEndian.PutUint16(size[:], uint16(len(idList)))
		out = append(out, size[:]...)
		out = append(out, idList...)
	}
	if linkInfo != nil {
		var size [4]byte
		binary.LittleEndian.PutUint32(size[:], uint32(len(linkInfo)+4)) // the size counts itself
		out = append(out, size[:]...)
		out = append(out, linkInfo...)
	}
	if extra != nil {
		var size [4]byte
		binary.LittleEndian.PutUint32(size[:], uint32(len(extra)+4))
		out = append(out, size[:]...)
		out = append(out, extra...)
	}
	var zero [4]byte
	out = append(out, zero[:]...) // end of the extra data blocks

	for _, s := range strings_ {
		if flags&lnkIsUnicode == 0 {
			var count [2]byte
			binary.LittleEndian.PutUint16(count[:], uint16(len(s)))
			out = append(out, count[:]...)
			out = append(out, s...)
			continue
		}
		out = appendLnkString(out, s)
	}
	return out
}

// a link as WScript.Shell writes one: no name, arguments and an icon, plus
// the id list and link info blocks before the strings.
func sampleLnk() []byte {
	flags := uint32(lnkHasLinkTargetIDList | lnkHasLinkInfo | lnkHasArguments | lnkHasIconLocation | lnkIsUnicode)
	return buildLnk(flags,
		[]byte{0x01, 0x02, 0x03, 0x04},
		[]byte("linkinfo"),
		[]byte("extra"),
		"npub1abc +view:1",
		"C:\\verdana.exe,0",
	)
}

func TestLnkArguments(t *testing.T) {
	args, err := lnkArguments(sampleLnk())
	if err != nil {
		t.Fatal(err)
	}
	if args != "npub1abc +view:1" {
		t.Fatalf("arguments came back as %q", args)
	}
}

// a link COM wrote has no name (it would be the launcher's own file name), so
// naming it must add one without losing the arguments, the icon or the blocks
// in front of them.
func TestWithLnkNameAddsName(t *testing.T) {
	original := sampleLnk()
	named, err := withLnkName(original, "My Bundle")
	if err != nil {
		t.Fatal(err)
	}
	if string(named) == string(original) {
		t.Fatal("nothing changed")
	}
	name, err := lnkName(named)
	if err != nil {
		t.Fatal(err)
	}
	if name != "My Bundle" {
		t.Fatalf("name came back as %q", name)
	}
	args, err := lnkArguments(named)
	if err != nil {
		t.Fatal(err)
	}
	if args != "npub1abc +view:1" {
		t.Fatalf("arguments came back as %q", args)
	}
	// the icon location is after the arguments, so it is the proof the tail
	// survived too
	link, err := parseLnk(named)
	if err != nil {
		t.Fatal(err)
	}
	if link.strings.IconLoc != "C:\\verdana.exe,0" {
		t.Fatalf("icon came back as %q", link.strings.IconLoc)
	}
	// and a second pass changes nothing
	again, err := withLnkName(named, "My Bundle")
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(named) {
		t.Fatal("renaming to the same name rewrote the file")
	}
}

// naming a link replaces the name COM put there (the target's), whatever its
// length.
func TestWithLnkNameReplacesExisting(t *testing.T) {
	flags := uint32(lnkHasName | lnkHasArguments | lnkHasWorkingDir | lnkIsUnicode)
	data := buildLnk(flags, nil, nil, nil, "", "verdana.exe", `C:\app`, "npub1abc +view")

	named, err := withLnkName(data, "My Bundle")
	if err != nil {
		t.Fatal(err)
	}
	if name, _ := lnkName(named); name != "My Bundle" {
		t.Fatalf("name came back as %q", name)
	}
	link, err := parseLnk(named)
	if err != nil {
		t.Fatal(err)
	}
	if link.strings.Arguments != "npub1abc +view" || link.strings.WorkingDir != `C:\app` {
		t.Fatalf("lost the other strings: %#v", link.strings)
	}
	if link.strings.Environment != "" {
		t.Fatalf("environment block should be emptied, got %q", link.strings.Environment)
	}
}

// a non-unicode link (an old one, or a writer that does not do wide) has to
// read back too.
func TestLnkNonUnicode(t *testing.T) {
	flags := uint32(lnkHasArguments | lnkHasName)
	data := buildLnk(flags, nil, nil, nil, "", "", "npub1abc +view")
	if args, err := lnkArguments(data); err != nil || args != "npub1abc +view" {
		t.Fatalf("args=%q err=%v", args, err)
	}
	if name, err := lnkName(data); err != nil || name != "" {
		t.Fatalf("name=%q err=%v", name, err)
	}
}

func TestLnkRejectsGarbage(t *testing.T) {
	if _, err := parseLnk([]byte("short")); err == nil {
		t.Fatal("expected an error on a too-short file")
	}
	if _, err := parseLnk(make([]byte, 0x4C)); err == nil {
		t.Fatal("expected an error on a zeroed header")
	}
	bad := sampleLnk()
	// a link info block claiming to be enormous
	binary.LittleEndian.PutUint32(bad[0x4C+2+4:], 0xFFFF)
	if _, err := parseLnk(bad); err == nil {
		t.Fatal("expected an error on a link info size past the end")
	}
}

func TestLnkUnicodeNames(t *testing.T) {
	// anything the user types, including characters outside latin-1
	name := "Café ☕ bundle"
	data := buildLnk(lnkHasName|lnkHasArguments|lnkIsUnicode, nil, nil, nil, "", "", "npub1abc")
	named, err := withLnkName(data, name)
	if err != nil {
		t.Fatal(err)
	}
	got, err := lnkName(named)
	if err != nil {
		t.Fatal(err)
	}
	if got != name {
		t.Fatalf("name came back as %q, want %q", got, name)
	}
	if len(utf16.Encode([]rune(name))) == 0 || !strings.Contains(got, "☕") {
		t.Fatalf("wide name lost characters: %q", got)
	}
}

func TestLnkSlugName(t *testing.T) {
	if got := lnkSlugName(`C:\Users\x\AppData\verdana-my-bundle-1a2b3c4d.lnk`); got != "my-bundle-1a2b3c4d" {
		t.Fatalf("got %q", got)
	}
}
