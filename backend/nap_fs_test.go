package backend

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseFSPath(t *testing.T) {
	tests := []struct {
		value string
		want  string
		ok    bool
	}{
		{"/", "", true},
		{"/private", ".", true},
		{"/private/notes/a.txt", "notes/a.txt", true},
		{"/private/cafe\u0301.txt", "caf\u00e9.txt", true},
		{"private/a", "", false},
		{"/private/", "", false},
		{"/private//a", "", false},
		{"/private/../a", "", false},
		{"/private/./a", "", false},
		{"/private/a\\b", "", false},
		{"/private/C:/secret", "", false},
		{"/private/%2e%2e/a", "%2e%2e/a", true},
		{"/private/a\u202eb", "", false},
		{"/private/a\x00b", "", false},
		{"/private-other/a", "", false},
	}
	for _, test := range tests {
		t.Run(test.value, func(t *testing.T) {
			got, err := parseFSPath(test.value)
			if (err == nil) != test.ok {
				t.Fatalf("parseFSPath(%q) error = %v", test.value, err)
			}
			if err == nil && got.rel != test.want {
				t.Fatalf("parseFSPath(%q).rel = %q, want %q", test.value, got.rel, test.want)
			}
		})
	}
}

func TestNapFSPrivateRoundTripAndIsolation(t *testing.T) {
	setupNapTest(t)
	a, recA := openNapplet(t, "fs-a")
	b, recB := openNapplet(t, "fs-b")
	ready(t, a, recA, 1)
	ready(t, b, recB, 1)

	post(t, a, map[string]any{"type": "fs.info", "id": "info"})
	info := recA.wait(t, "fs.info.result", 1)
	if info["error"] != nil || info["info"] == nil {
		t.Fatalf("fs.info = %#v", info)
	}
	post(t, a, map[string]any{"type": "fs.mkdir", "id": "mkdir", "path": "/private/notes"})
	if got := recA.wait(t, "fs.mkdir.result", 1); got["error"] != nil {
		t.Fatalf("mkdir = %#v", got)
	}
	payload := base64.StdEncoding.EncodeToString([]byte("hello"))
	post(t, a, map[string]any{"type": "fs.write", "id": "write", "path": "/private/notes/a.txt", "data": payload})
	if got := recA.wait(t, "fs.write.result", 1); got["error"] != nil {
		t.Fatalf("write = %#v", got)
	}
	post(t, a, map[string]any{"type": "fs.read", "id": "read", "path": "/private/notes/a.txt"})
	read := recA.wait(t, "fs.read.result", 1)
	result := read["result"].(map[string]any)
	if result["data"] != payload || result["bytesRead"] != float64(5) {
		t.Fatalf("read = %#v", read)
	}

	post(t, b, map[string]any{"type": "fs.read", "id": "isolated", "path": "/private/notes/a.txt"})
	if got := recB.wait(t, "fs.read.result", 1); got["error"] != "not-found" {
		t.Fatalf("other napplet read = %#v", got)
	}
	if _, err := os.Stat(filepath.Join(fsPrivateDir(a.napp.ID), "notes", "a.txt")); err != nil {
		t.Fatalf("private backing file missing: %v", err)
	}
}

func TestNapFSWritePreconditionsAndCanonicalBase64(t *testing.T) {
	setupNapTest(t)
	ci, rec := openNapplet(t, "fs-write")
	ready(t, ci, rec, 1)

	post(t, ci, map[string]any{"type": "fs.write", "id": "bad", "path": "/private/a", "data": "SGVsbG8"})
	if got := rec.wait(t, "fs.write.result", 1); got["error"] != "invalid-data" {
		t.Fatalf("bad base64 = %#v", got)
	}
	data := base64.StdEncoding.EncodeToString([]byte("first"))
	post(t, ci, map[string]any{"type": "fs.write", "id": "create", "path": "/private/a", "data": data, "options": map[string]any{"ifAbsent": true}})
	if got := rec.wait(t, "fs.write.result", 2); got["error"] != nil {
		t.Fatalf("create = %#v", got)
	}
	post(t, ci, map[string]any{"type": "fs.write", "id": "again", "path": "/private/a", "data": data, "options": map[string]any{"ifAbsent": true}})
	if got := rec.wait(t, "fs.write.result", 3); got["error"] != "conflict" {
		t.Fatalf("ifAbsent = %#v", got)
	}
	post(t, ci, map[string]any{"type": "fs.stat", "id": "stat", "path": "/private/a"})
	stat := rec.wait(t, "fs.stat.result", 1)
	revision := stat["metadata"].(map[string]any)["revision"].(string)
	post(t, ci, map[string]any{"type": "fs.write", "id": "revision", "path": "/private/a", "data": base64.StdEncoding.EncodeToString([]byte("second")), "options": map[string]any{"ifRevision": revision}})
	if got := rec.wait(t, "fs.write.result", 4); got["error"] != nil {
		t.Fatalf("revision write = %#v", got)
	}
	post(t, ci, map[string]any{"type": "fs.write", "id": "stale", "path": "/private/a", "data": data, "options": map[string]any{"ifRevision": revision}})
	if got := rec.wait(t, "fs.write.result", 5); got["error"] != "conflict" {
		t.Fatalf("stale revision = %#v", got)
	}
}

func TestNapFSAppendPatchMoveListAndRemove(t *testing.T) {
	setupNapTest(t)
	ci, rec := openNapplet(t, "fs-mutations")
	ready(t, ci, rec, 1)
	post(t, ci, map[string]any{"type": "fs.write", "id": "create", "path": "/private/a", "data": base64.StdEncoding.EncodeToString([]byte("abc"))})
	rec.wait(t, "fs.write.result", 1)
	post(t, ci, map[string]any{"type": "fs.write", "id": "append", "path": "/private/a", "data": base64.StdEncoding.EncodeToString([]byte("def")), "options": map[string]any{"mode": "append"}})
	rec.wait(t, "fs.write.result", 2)
	post(t, ci, map[string]any{"type": "fs.write", "id": "patch", "path": "/private/a", "data": base64.StdEncoding.EncodeToString([]byte("Z")), "options": map[string]any{"mode": "patch", "offset": 1}})
	rec.wait(t, "fs.write.result", 3)
	post(t, ci, map[string]any{"type": "fs.move", "id": "move", "fromPath": "/private/a", "toPath": "/private/b"})
	if got := rec.wait(t, "fs.move.result", 1); got["error"] != nil {
		t.Fatalf("move = %#v", got)
	}
	post(t, ci, map[string]any{"type": "fs.list", "id": "list", "path": "/private"})
	listed := rec.wait(t, "fs.list.result", 1)["entries"].([]any)
	if len(listed) != 1 || listed[0].(map[string]any)["path"] != "/private/b" {
		t.Fatalf("list = %#v", listed)
	}
	post(t, ci, map[string]any{"type": "fs.read", "id": "read", "path": "/private/b"})
	read := rec.wait(t, "fs.read.result", 1)["result"].(map[string]any)
	want := base64.StdEncoding.EncodeToString([]byte("aZcdef"))
	if read["data"] != want {
		t.Fatalf("read data = %v, want %s", read["data"], want)
	}
	post(t, ci, map[string]any{"type": "fs.remove", "id": "remove", "path": "/private/b"})
	if got := rec.wait(t, "fs.remove.result", 1); got["error"] != nil {
		t.Fatalf("remove = %#v", got)
	}
	post(t, ci, map[string]any{"type": "fs.remove", "id": "missing", "path": "/private/b"})
	if got := rec.wait(t, "fs.remove.result", 2); got["error"] != "not-found" {
		t.Fatalf("missing remove = %#v", got)
	}
}

func TestNapFSVisibleRoot(t *testing.T) {
	setupNapTest(t)
	ci, rec := openNapplet(t, "fs-root")
	ready(t, ci, rec, 1)
	post(t, ci, map[string]any{"type": "fs.stat", "id": "stat", "path": "/"})
	metadata := rec.wait(t, "fs.stat.result", 1)["metadata"].(map[string]any)
	if metadata["kind"] != "directory" {
		t.Fatalf("root stat = %#v", metadata)
	}
	post(t, ci, map[string]any{"type": "fs.list", "id": "list", "path": "/"})
	entries := rec.wait(t, "fs.list.result", 1)["entries"].([]any)
	if len(entries) != 1 || entries[0].(map[string]any)["path"] != "/private" {
		t.Fatalf("root list = %#v", entries)
	}
}

func TestNapFSUnsupportedOperationsReply(t *testing.T) {
	setupNapTest(t)
	ci, rec := openNapplet(t, "fs-unsupported")
	ready(t, ci, rec, 1)
	for _, typ := range []string{"fs.pickFile", "fs.pickFiles", "fs.pickDirectory", "fs.pickSaveFile", "fs.watch", "fs.unwatch"} {
		post(t, ci, map[string]any{"type": typ, "id": typ})
		if got := rec.wait(t, typ+".result", 1); got["error"] != "unsupported" {
			t.Fatalf("%s = %#v", typ, got)
		}
	}
}

func TestNapFSRejectsOversizedWriteBeforeDecode(t *testing.T) {
	setupNapTest(t)
	ci, rec := openNapplet(t, "fs-large")
	ready(t, ci, rec, 1)
	data := strings.Repeat("A", base64.StdEncoding.EncodedLen(fsMaxWriteBytes+1))
	post(t, ci, map[string]any{"type": "fs.write", "id": "large", "path": "/private/a", "data": data})
	if got := rec.wait(t, "fs.write.result", 1); got["error"] != "too-large" {
		t.Fatalf("large write = %#v", got)
	}
}
