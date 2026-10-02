package backend

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

const (
	fsMaxPathBytes    = 1024
	fsMaxSegmentBytes = 255
	fsMaxDepth        = 32
)

var errFSInvalidPath = errors.New("invalid-path")

// fsPath is a normalized virtual path. rel is relative to the private backing
// root and uses slash separators on every platform; "." names that root.
type fsPath struct {
	virtual string
	rel     string
	parts   []string
}

func parseFSPath(value string) (fsPath, error) {
	if value == "/" {
		return fsPath{virtual: "/"}, nil
	}
	if value == "/private" || value == "/private/" {
		if value == "/private/" {
			return fsPath{}, errFSInvalidPath
		}
		return fsPath{virtual: "/private", rel: "."}, nil
	}
	if !strings.HasPrefix(value, "/private/") || len(value) > fsMaxPathBytes || !utf8.ValidString(value) {
		return fsPath{}, errFSInvalidPath
	}
	raw := strings.Split(strings.TrimPrefix(value, "/private/"), "/")
	if len(raw) == 0 || len(raw) > fsMaxDepth {
		return fsPath{}, errFSInvalidPath
	}
	parts := make([]string, len(raw))
	for i, segment := range raw {
		if segment == "" || segment == "." || segment == ".." || len(segment) > fsMaxSegmentBytes || strings.ContainsRune(segment, '\\') {
			return fsPath{}, errFSInvalidPath
		}
		if i == 0 && len(segment) == 2 && segment[1] == ':' &&
			(segment[0] >= 'a' && segment[0] <= 'z' || segment[0] >= 'A' && segment[0] <= 'Z') {
			return fsPath{}, errFSInvalidPath
		}
		for _, r := range segment {
			if unicode.IsControl(r) || isBidiFormatting(r) {
				return fsPath{}, errFSInvalidPath
			}
		}
		parts[i] = norm.NFC.String(segment)
		if parts[i] == "" || len(parts[i]) > fsMaxSegmentBytes {
			return fsPath{}, errFSInvalidPath
		}
	}
	rel := strings.Join(parts, "/")
	if len("/private/")+len(rel) > fsMaxPathBytes {
		return fsPath{}, errFSInvalidPath
	}
	return fsPath{virtual: "/private/" + rel, rel: rel, parts: parts}, nil
}

func isBidiFormatting(r rune) bool {
	switch r {
	case '\u061c', '\u200e', '\u200f', '\u202a', '\u202b', '\u202c', '\u202d', '\u202e',
		'\u2066', '\u2067', '\u2068', '\u2069':
		return true
	}
	return false
}
