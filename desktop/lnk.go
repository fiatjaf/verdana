package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf16"
)

// Reading and naming Windows .lnk shortcut files.
//
// A .lnk is a binary file (MS-SHLLINK) and everything we need is in it as
// plain text: the arguments the shortcut runs, in COMMAND_LINE_ARGUMENTS, and
// a NAME_STRING we can write the bundle's name into. Only writing the file
// itself needs COM (see shortcutfile_windows.go), since the shell owns how a
// link is serialized — so reading and naming are this file's job, plain bytes,
// and nothing here is windows-only code.
//
// The layout, in order: a fixed header whose LinkFlags say which parts follow,
// an optional target id list, an optional LinkInfo block (which says its own
// size), a run of size-prefixed extra data blocks, then StringData: the
// optional strings, each a 16-bit character count followed by the characters
// (UTF-16 when the unicode flag is set, else the system code page).

const (
	lnkHasLinkTargetIDList = 0x00000001
	lnkHasLinkInfo         = 0x00000002
	lnkHasName             = 0x00000004
	lnkHasRelativePath     = 0x00000008
	lnkHasWorkingDir       = 0x00000010
	lnkHasArguments        = 0x00000020
	lnkHasIconLocation     = 0x00000040
	lnkIsUnicode           = 0x00000080
)

// lnkStrings are the StringData parts of a link, in the order they live in
// the file. Environment is the environment-variable block, which comes with
// the name; name is the one we put the bundle's name in.
type lnkStrings struct {
	Environment string
	Name        string
	Relative    string
	WorkingDir  string
	Arguments   string
	IconLoc     string
}

// lnkLink is a parsed .lnk: the strings it carries plus the bytes around them,
// so a link can be rebuilt with a different name without losing the rest.
type lnkLink struct {
	strings lnkStrings
	// before is everything up to StringData (header, id list, link info, extra
	// data), with HasName cleared when the file had no name: a rebuilt file
	// adds one.
	before []byte
	// after is what follows StringData (the terminal code block).
	after []byte
	// flags are the LinkFlags we build the file back with.
	flags uint32
}

// lnkName and lnkArguments read a link's name and its arguments, the two
// things a bundle shortcut is made of.
func lnkName(data []byte) (string, error) {
	link, err := parseLnk(data)
	if err != nil {
		return "", err
	}
	return link.strings.Name, nil
}

func lnkArguments(data []byte) (string, error) {
	link, err := parseLnk(data)
	if err != nil {
		return "", err
	}
	return link.strings.Arguments, nil
}

// withLnkName returns the link with name as its NAME_STRING: what COM derives
// from the link target is replaced by the bundle's own name, so a shortcut
// can be listed under the name the user gave it. A link that already has the
// right name comes back untouched.
func withLnkName(data []byte, name string) ([]byte, error) {
	link, err := parseLnk(data)
	if err != nil {
		return nil, err
	}
	if link.strings.Name == name {
		return data, nil
	}
	link.strings.Name = name
	return link.build()
}

func parseLnk(data []byte) (*lnkLink, error) {
	if len(data) < 0x4C {
		return nil, errors.New("not a .lnk file: too short")
	}
	if size := binary.LittleEndian.Uint32(data[0:4]); size != 0x4C {
		return nil, fmt.Errorf("not a .lnk file: header says %d", size)
	}
	flags := binary.LittleEndian.Uint32(data[4:8])
	unicode := flags&lnkIsUnicode != 0
	pos := 0x4C

	if flags&lnkHasLinkTargetIDList != 0 {
		if pos+2 > len(data) {
			return nil, errors.New("truncated .lnk: id list header")
		}
		size := int(binary.LittleEndian.Uint16(data[pos : pos+2]))
		pos += 2 + size
	}
	if flags&lnkHasLinkInfo != 0 {
		if pos+4 > len(data) {
			return nil, errors.New("truncated .lnk: link info header")
		}
		size := int(binary.LittleEndian.Uint32(data[pos : pos+4]))
		pos += size
	}
	// extra data: blocks of a size each, a zero size ending the run
	for {
		if pos+4 > len(data) {
			return nil, errors.New("truncated .lnk: extra data")
		}
		size := int(binary.LittleEndian.Uint32(data[pos : pos+4]))
		if size == 0 {
			pos += 4
			break
		}
		pos += size
	}
	if pos > len(data) {
		return nil, errors.New("truncated .lnk: extra data overruns the file")
	}

	link := &lnkLink{before: append([]byte(nil), data[:pos]...), flags: flags | lnkHasName}
	var s lnkStrings
	var err error
	read := func() (string, error) {
		v, err := readLnkString(data, &pos, unicode)
		return v, err
	}
	if flags&lnkHasName != 0 {
		if s.Environment, err = read(); err != nil {
			return nil, err
		}
		if s.Name, err = read(); err != nil {
			return nil, err
		}
	}
	if flags&lnkHasRelativePath != 0 {
		if s.Relative, err = read(); err != nil {
			return nil, err
		}
	}
	if flags&lnkHasWorkingDir != 0 {
		if s.WorkingDir, err = read(); err != nil {
			return nil, err
		}
	}
	if flags&lnkHasArguments != 0 {
		if s.Arguments, err = read(); err != nil {
			return nil, err
		}
	}
	if flags&lnkHasIconLocation != 0 {
		if s.IconLoc, err = read(); err != nil {
			return nil, err
		}
	}
	if pos > len(data) {
		return nil, errors.New("truncated .lnk: string data overruns the file")
	}
	link.strings = s
	link.after = append([]byte(nil), data[pos:]...)
	return link, nil
}

// build lays the file out again: the header and blocks, then the strings the
// flags say the file has — with the environment block emptied, since its count
// is ours to write — then whatever trailed them.
func (l *lnkLink) build() ([]byte, error) {
	s := l.strings
	s.Environment = ""
	unicode := l.flags&lnkIsUnicode != 0
	if !unicode && !isASCII(s.Name) {
		// a link that stores narrow strings cannot carry this name; the slug
		// its file name gives is what the launcher falls back to
		return nil, fmt.Errorf("link stores narrow strings, cannot name it %q", s.Name)
	}

	out := append([]byte(nil), l.before...)
	binary.LittleEndian.PutUint32(out[4:8], l.flags)
	appendStr := func(str string) {
		if unicode {
			out = appendLnkString(out, str)
			return
		}
		out = appendLnkNarrowString(out, str)
	}
	// HasName is ours now, so the name and the environment block that comes
	// with it are always written
	appendStr(s.Environment)
	appendStr(s.Name)
	if l.flags&lnkHasRelativePath != 0 {
		appendStr(s.Relative)
	}
	if l.flags&lnkHasWorkingDir != 0 {
		appendStr(s.WorkingDir)
	}
	if l.flags&lnkHasArguments != 0 {
		appendStr(s.Arguments)
	}
	if l.flags&lnkHasIconLocation != 0 {
		appendStr(s.IconLoc)
	}
	return append(out, l.after...), nil
}

func isASCII(s string) bool {
	for _, r := range s {
		if r > unicode.MaxASCII {
			return false
		}
	}
	return true
}

// readLnkString reads one StringData string at *pos, advancing it.
func readLnkString(data []byte, pos *int, unicode bool) (string, error) {
	if *pos+2 > len(data) {
		return "", errors.New("truncated .lnk: string header")
	}
	count := int(binary.LittleEndian.Uint16(data[*pos : *pos+2]))
	*pos += 2
	// an empty string still has its two count bytes and nothing else
	if count == 0 {
		return "", nil
	}
	if unicode {
		if *pos+count*2 > len(data) {
			return "", errors.New("truncated .lnk: string body")
		}
		units := make([]uint16, count)
		for i := range units {
			units[i] = binary.LittleEndian.Uint16(data[*pos+i*2:])
		}
		*pos += count * 2
		return string(utf16.Decode(units)), nil
	}
	if *pos+count > len(data) {
		return "", errors.New("truncated .lnk: string body")
	}
	s := string(data[*pos : *pos+count])
	*pos += count
	return s, nil
}

// appendLnkString writes one StringData string, wide, as COM writes them.
func appendLnkString(out []byte, s string) []byte {
	var buf [2]byte
	binary.LittleEndian.PutUint16(buf[:], uint16(len([]rune(s))))
	out = append(out, buf[:]...)
	for _, u := range utf16.Encode([]rune(s)) {
		var c [2]byte
		binary.LittleEndian.PutUint16(c[:], u)
		out = append(out, c[:]...)
	}
	return out
}

// appendLnkNarrowString writes one StringData string the old way: a character
// count and the bytes themselves, in whatever code page the reader assumes.
func appendLnkNarrowString(out []byte, s string) []byte {
	var buf [2]byte
	binary.LittleEndian.PutUint16(buf[:], uint16(len(s)))
	out = append(out, buf[:]...)
	return append(out, s...)
}

// lnkSlugName is the name to show for a link that carries none: the slug its
// file name was built from, which is all the shell had to go by.
func lnkSlugName(path string) string {
	base := path
	if i := strings.LastIndexAny(base, `/\`); i >= 0 {
		base = base[i+1:]
	}
	base = strings.TrimPrefix(base, shortcutPrefix)
	return strings.TrimSuffix(base, ".lnk")
}
