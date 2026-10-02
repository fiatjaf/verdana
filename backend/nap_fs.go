package backend

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"math"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
)

const (
	fsMaxReadBytes  = 1024 * 1024
	fsMaxWriteBytes = 1024 * 1024
	fsQuotaBytes    = 64 * 1024 * 1024
	fsMaxListItems  = 1000
)

var fsRootLocks sync.Map // map[string]*sync.Mutex

func init() {
	handleNap(map[string]napHandler{
		"fs.info":          napFSInfo,
		"fs.pickFile":      napFSUnsupported,
		"fs.pickFiles":     napFSUnsupported,
		"fs.pickDirectory": napFSUnsupported,
		"fs.pickSaveFile":  napFSUnsupported,
		"fs.stat":          napFSStat,
		"fs.list":          napFSList,
		"fs.read":          napFSRead,
		"fs.write":         napFSWrite,
		"fs.mkdir":         napFSMkdir,
		"fs.remove":        napFSRemove,
		"fs.move":          napFSMove,
		"fs.watch":         napFSUnsupported,
		"fs.unwatch":       napFSUnsupported,
	})
}

type fsReadOptions struct {
	Offset *int64 `json:"offset"`
	Length *int64 `json:"length"`
}

type fsWriteOptions struct {
	Mode       string  `json:"mode"`
	Offset     *int64  `json:"offset"`
	IfRevision *string `json:"ifRevision"`
	IfAbsent   bool    `json:"ifAbsent"`
}

type fsMkdirOptions struct {
	Recursive bool `json:"recursive"`
}

type fsPathRequest struct {
	Path      *string         `json:"path"`
	Options   json.RawMessage `json:"options"`
	Data      *string         `json:"data"`
	Recursive bool            `json:"recursive"`
	FromPath  *string         `json:"fromPath"`
	ToPath    *string         `json:"toPath"`
}

func napFSInfo(c *napCall) {
	c.reply(map[string]any{"info": map[string]any{
		"roots": []any{map[string]any{
			"path":        "/private",
			"name":        "Private files",
			"description": "Files private to this napplet",
			"permissions": []string{"read", "write", "create", "delete", "list"},
		}},
		"limits": map[string]any{
			"maxReadBytes":  fsMaxReadBytes,
			"maxWriteBytes": fsMaxWriteBytes,
			"maxWatchCount": 0,
		},
	}})
}

func napFSUnsupported(c *napCall) { c.reply(map[string]any{"error": "unsupported"}) }

func fsPrivateDir(nappID string) string {
	sum := sha256.Sum256([]byte("nap-fs-private\x00" + nappID))
	return filepath.Join(dataDir, "filesystem", hex.EncodeToString(sum[:]))
}

func fsLock(nappID string) *sync.Mutex {
	v, _ := fsRootLocks.LoadOrStore(nappID, new(sync.Mutex))
	return v.(*sync.Mutex)
}

func openFSRoot(nappID string) (*os.Root, error) {
	dir := fsPrivateDir(nappID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	return os.OpenRoot(dir)
}

func fsReplyError(c *napCall, err error) {
	c.reply(map[string]any{"error": fsErrorName(err)})
}

func fsErrorName(err error) string {
	if err == nil {
		return ""
	}
	for _, name := range []string{"invalid-path", "invalid-data", "not-found", "already-exists", "not-a-file", "not-a-directory", "permission-denied", "policy-denied", "quota-exceeded", "too-large", "conflict", "unsupported"} {
		if err.Error() == name {
			return name
		}
	}
	if errors.Is(err, fs.ErrNotExist) {
		return "not-found"
	}
	if errors.Is(err, fs.ErrExist) {
		return "already-exists"
	}
	if errors.Is(err, fs.ErrPermission) {
		return "permission-denied"
	}
	if errors.Is(err, syscall.ENOTEMPTY) {
		return "conflict"
	}
	return "io-error"
}

func decodeFSPathRequest(c *napCall) (fsPathRequest, error) {
	var req fsPathRequest
	if err := c.decode(&req); err != nil {
		return req, errors.New("invalid-path")
	}
	return req, nil
}

func napFSStat(c *napCall) {
	req, err := decodeFSPathRequest(c)
	if err != nil || req.Path == nil {
		fsReplyError(c, errFSInvalidPath)
		return
	}
	p, err := parseFSPath(*req.Path)
	if err != nil {
		fsReplyError(c, err)
		return
	}
	if p.virtual == "/" {
		c.reply(map[string]any{"metadata": map[string]any{
			"path": "/", "kind": "directory", "permissions": []string{"list"},
		}})
		return
	}
	c.async(func(ctx context.Context) {
		lock := fsLock(c.ci.napp.ID)
		lock.Lock()
		defer lock.Unlock()
		root, err := openFSRoot(c.ci.napp.ID)
		if err != nil {
			fsReplyError(c, err)
			return
		}
		defer root.Close()
		info, err := root.Lstat(p.rel)
		if err != nil {
			fsReplyError(c, err)
			return
		}
		metadata, err := fsMetadata(root, p, info)
		if err != nil {
			fsReplyError(c, err)
			return
		}
		c.reply(map[string]any{"metadata": metadata})
	})
}

func fsMetadata(root *os.Root, p fsPath, info fs.FileInfo) (map[string]any, error) {
	kind := "unknown"
	if info.Mode().IsRegular() {
		kind = "file"
	} else if info.IsDir() {
		kind = "directory"
	}
	out := map[string]any{
		"path":       p.virtual,
		"kind":       kind,
		"modifiedAt": info.ModTime().UnixMilli(),
	}
	if info.Mode().IsRegular() {
		out["permissions"] = []string{"read", "write", "delete"}
		out["size"] = info.Size()
		rev, err := fsRevision(root, p.rel)
		if err != nil {
			return nil, err
		}
		out["revision"] = rev
	} else if info.IsDir() {
		out["permissions"] = []string{"create", "delete", "list"}
	}
	return out, nil
}

func fsRevision(root *os.Root, rel string) (string, error) {
	f, err := root.Open(rel)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func napFSList(c *napCall) {
	req, err := decodeFSPathRequest(c)
	if err != nil || req.Path == nil {
		fsReplyError(c, errFSInvalidPath)
		return
	}
	p, err := parseFSPath(*req.Path)
	if err != nil {
		fsReplyError(c, err)
		return
	}
	if p.virtual == "/" {
		c.reply(map[string]any{"entries": []any{map[string]any{
			"name": "private", "path": "/private", "kind": "directory",
		}}})
		return
	}
	c.async(func(ctx context.Context) {
		lock := fsLock(c.ci.napp.ID)
		lock.Lock()
		defer lock.Unlock()
		root, err := openFSRoot(c.ci.napp.ID)
		if err != nil {
			fsReplyError(c, err)
			return
		}
		defer root.Close()
		dir, err := root.Open(p.rel)
		if err != nil {
			fsReplyError(c, err)
			return
		}
		defer dir.Close()
		entries, err := dir.ReadDir(fsMaxListItems + 1)
		if err != nil {
			if info, statErr := dir.Stat(); statErr == nil && !info.IsDir() {
				err = errors.New("not-a-directory")
			}
			fsReplyError(c, err)
			return
		}
		if len(entries) > fsMaxListItems {
			fsReplyError(c, errors.New("too-large"))
			return
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		out := make([]any, 0, len(entries))
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".verdana-tmp-") {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				fsReplyError(c, err)
				return
			}
			kind := "unknown"
			if info.Mode().IsRegular() {
				kind = "file"
			} else if info.IsDir() {
				kind = "directory"
			}
			item := map[string]any{"name": entry.Name(), "path": path.Join(p.virtual, entry.Name()), "kind": kind, "modifiedAt": info.ModTime().UnixMilli()}
			if info.Mode().IsRegular() {
				item["size"] = info.Size()
			}
			out = append(out, item)
		}
		c.reply(map[string]any{"entries": out})
	})
}

func napFSRead(c *napCall) {
	req, err := decodeFSPathRequest(c)
	if err != nil || req.Path == nil {
		fsReplyError(c, errFSInvalidPath)
		return
	}
	p, err := parseFSPath(*req.Path)
	if err != nil {
		fsReplyError(c, err)
		return
	}
	if p.virtual == "/" {
		fsReplyError(c, errors.New("not-a-file"))
		return
	}
	var opts fsReadOptions
	if len(req.Options) > 0 && string(req.Options) != "null" {
		if err := json.Unmarshal(req.Options, &opts); err != nil {
			fsReplyError(c, errors.New("invalid-data"))
			return
		}
	}
	offset, length := int64(0), int64(fsMaxReadBytes)
	if opts.Offset != nil {
		offset = *opts.Offset
	}
	if opts.Length != nil {
		length = *opts.Length
	}
	if offset < 0 || length < 0 {
		fsReplyError(c, errors.New("invalid-data"))
		return
	}
	if length > fsMaxReadBytes {
		fsReplyError(c, errors.New("too-large"))
		return
	}
	c.async(func(ctx context.Context) {
		lock := fsLock(c.ci.napp.ID)
		lock.Lock()
		defer lock.Unlock()
		root, err := openFSRoot(c.ci.napp.ID)
		if err != nil {
			fsReplyError(c, err)
			return
		}
		defer root.Close()
		f, err := root.Open(p.rel)
		if err != nil {
			fsReplyError(c, err)
			return
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() {
			if err == nil {
				err = errors.New("not-a-file")
			}
			fsReplyError(c, err)
			return
		}
		buf := make([]byte, length)
		n, readErr := f.ReadAt(buf, offset)
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			fsReplyError(c, readErr)
			return
		}
		buf = buf[:n]
		c.reply(map[string]any{"result": map[string]any{
			"data": base64.StdEncoding.EncodeToString(buf), "offset": offset,
			"bytesRead": n, "eof": offset+int64(n) >= info.Size(), "size": info.Size(),
		}})
	})
}

func napFSWrite(c *napCall) {
	req, err := decodeFSPathRequest(c)
	if err != nil || req.Path == nil {
		fsReplyError(c, errFSInvalidPath)
		return
	}
	if req.Data == nil {
		fsReplyError(c, errors.New("invalid-data"))
		return
	}
	p, err := parseFSPath(*req.Path)
	if err != nil || p.rel == "." || p.rel == "" {
		fsReplyError(c, errFSInvalidPath)
		return
	}
	if len(*req.Data) > base64.StdEncoding.EncodedLen(fsMaxWriteBytes) {
		fsReplyError(c, errors.New("too-large"))
		return
	}
	data, err := base64.StdEncoding.Strict().DecodeString(*req.Data)
	if err != nil || base64.StdEncoding.EncodeToString(data) != *req.Data {
		fsReplyError(c, errors.New("invalid-data"))
		return
	}
	if len(data) > fsMaxWriteBytes {
		fsReplyError(c, errors.New("too-large"))
		return
	}
	var opts fsWriteOptions
	if len(req.Options) > 0 && string(req.Options) != "null" {
		if err := json.Unmarshal(req.Options, &opts); err != nil {
			fsReplyError(c, errors.New("invalid-data"))
			return
		}
	}
	if opts.Mode == "" {
		opts.Mode = "replace"
	}
	if (opts.Mode == "replace" || opts.Mode == "append") && opts.Offset != nil || opts.Mode == "patch" && opts.Offset == nil || opts.Mode != "replace" && opts.Mode != "append" && opts.Mode != "patch" {
		fsReplyError(c, errors.New("invalid-data"))
		return
	}
	c.async(func(ctx context.Context) {
		result, err := fsWritePrivate(c.ci.napp.ID, p, data, opts)
		if err != nil {
			fsReplyError(c, err)
			return
		}
		c.reply(map[string]any{"result": result})
	})
}

func fsWritePrivate(nappID string, p fsPath, incoming []byte, opts fsWriteOptions) (map[string]any, error) {
	lock := fsLock(nappID)
	lock.Lock()
	defer lock.Unlock()
	root, err := openFSRoot(nappID)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	info, statErr := root.Lstat(p.rel)
	exists := statErr == nil
	if statErr != nil && !errors.Is(statErr, fs.ErrNotExist) {
		return nil, statErr
	}
	if exists && !info.Mode().IsRegular() {
		return nil, errors.New("not-a-file")
	}
	var existing []byte
	if exists {
		existing, err = root.ReadFile(p.rel)
		if err != nil {
			return nil, err
		}
	}
	if opts.IfAbsent && exists {
		return nil, errors.New("conflict")
	}
	if opts.IfRevision != nil {
		if !exists {
			return nil, errors.New("conflict")
		}
		sum := sha256.Sum256(existing)
		if hex.EncodeToString(sum[:]) != *opts.IfRevision {
			return nil, errors.New("conflict")
		}
	}
	var next []byte
	switch opts.Mode {
	case "replace":
		next = append([]byte(nil), incoming...)
	case "append":
		next = append(append([]byte(nil), existing...), incoming...)
	case "patch":
		if *opts.Offset < 0 || *opts.Offset > math.MaxInt || *opts.Offset+int64(len(incoming)) < *opts.Offset {
			return nil, errors.New("invalid-data")
		}
		end := int(*opts.Offset) + len(incoming)
		if end > fsQuotaBytes {
			return nil, errors.New("quota-exceeded")
		}
		next = append([]byte(nil), existing...)
		if end > len(next) {
			next = append(next, make([]byte, end-len(next))...)
		}
		copy(next[int(*opts.Offset):], incoming)
	}
	used, err := fsUsage(root)
	if err != nil {
		return nil, err
	}
	oldSize := int64(0)
	if exists {
		oldSize = int64(len(existing))
	}
	if used-oldSize+int64(len(next)) > fsQuotaBytes {
		return nil, errors.New("quota-exceeded")
	}
	parent := path.Dir(p.rel)
	if info, err := root.Stat(parent); err != nil || !info.IsDir() {
		if err == nil {
			err = errors.New("not-a-directory")
		}
		return nil, err
	}
	tmp, err := fsTempName(parent)
	if err != nil {
		return nil, err
	}
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		f.Close()
		if !ok {
			_ = root.Remove(tmp)
		}
	}()
	if _, err := f.Write(next); err != nil {
		return nil, err
	}
	if err := f.Sync(); err != nil {
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	if err := fsCommitReplace(root, tmp, p.rel); err != nil {
		return nil, err
	}
	ok = true
	return map[string]any{"bytesWritten": len(incoming), "size": len(next)}, nil
}

func fsTempName(parent string) (string, error) {
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", err
	}
	return path.Join(parent, ".verdana-tmp-"+hex.EncodeToString(token[:])), nil
}

func fsUsage(root *os.Root) (int64, error) {
	var total int64
	err := fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return errors.New("policy-denied")
		}
		if entry.Type().IsRegular() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			total += info.Size()
		}
		return nil
	})
	return total, err
}

func napFSMkdir(c *napCall) {
	req, err := decodeFSPathRequest(c)
	if err != nil || req.Path == nil {
		fsReplyError(c, errFSInvalidPath)
		return
	}
	p, err := parseFSPath(*req.Path)
	if err != nil || p.rel == "." || p.rel == "" {
		fsReplyError(c, errFSInvalidPath)
		return
	}
	var opts fsMkdirOptions
	if len(req.Options) > 0 && string(req.Options) != "null" {
		if err := json.Unmarshal(req.Options, &opts); err != nil {
			fsReplyError(c, errors.New("invalid-data"))
			return
		}
	}
	c.async(func(ctx context.Context) {
		lock := fsLock(c.ci.napp.ID)
		lock.Lock()
		defer lock.Unlock()
		root, err := openFSRoot(c.ci.napp.ID)
		if err == nil {
			defer root.Close()
			if opts.Recursive {
				err = root.MkdirAll(p.rel, 0700)
			} else {
				err = root.Mkdir(p.rel, 0700)
			}
		}
		if err != nil {
			fsReplyError(c, err)
			return
		}
		c.reply(nil)
	})
}

func napFSRemove(c *napCall) {
	req, err := decodeFSPathRequest(c)
	if err != nil || req.Path == nil {
		fsReplyError(c, errFSInvalidPath)
		return
	}
	p, err := parseFSPath(*req.Path)
	if err != nil || p.rel == "." || p.rel == "" {
		fsReplyError(c, errFSInvalidPath)
		return
	}
	c.async(func(ctx context.Context) {
		lock := fsLock(c.ci.napp.ID)
		lock.Lock()
		defer lock.Unlock()
		root, err := openFSRoot(c.ci.napp.ID)
		if err == nil {
			defer root.Close()
			if _, statErr := root.Lstat(p.rel); statErr != nil {
				err = statErr
			} else if req.Recursive {
				err = root.RemoveAll(p.rel)
			} else {
				err = root.Remove(p.rel)
			}
		}
		if err != nil {
			fsReplyError(c, err)
			return
		}
		c.reply(nil)
	})
}

func napFSMove(c *napCall) {
	req, err := decodeFSPathRequest(c)
	if err != nil || req.FromPath == nil || req.ToPath == nil {
		fsReplyError(c, errFSInvalidPath)
		return
	}
	from, errFrom := parseFSPath(*req.FromPath)
	to, errTo := parseFSPath(*req.ToPath)
	if errFrom != nil || errTo != nil || from.rel == "." || to.rel == "." || from.rel == "" || to.rel == "" {
		fsReplyError(c, errFSInvalidPath)
		return
	}
	c.async(func(ctx context.Context) {
		lock := fsLock(c.ci.napp.ID)
		lock.Lock()
		defer lock.Unlock()
		root, err := openFSRoot(c.ci.napp.ID)
		if err == nil {
			defer root.Close()
			if _, statErr := root.Lstat(to.rel); statErr == nil {
				err = errors.New("already-exists")
			} else if !errors.Is(statErr, fs.ErrNotExist) {
				err = statErr
			} else {
				err = root.Rename(from.rel, to.rel)
			}
		}
		if err != nil {
			fsReplyError(c, err)
			return
		}
		c.reply(nil)
	})
}
