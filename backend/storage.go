package backend

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// localStorageQuota caps one napp's localStorage, like browsers do (~5MB).
// Both the JS shim (which throws synchronously) and the Go side enforce it.
const localStorageQuota = 5 * 1024 * 1024

// LocalStorageQuota is the per-napp localStorage cap in bytes.
func LocalStorageQuota() int { return localStorageQuota }

type nappStorage struct {
	mu   sync.Mutex
	data map[string]string
	size int // sum of len(key)+len(value) in bytes, utf-8
}

var (
	storagesMu sync.Mutex
	storages   = make(map[string]*nappStorage)
)

// storageFileFor maps a napp id to its storage file. Napp ids are
// "<16hex>~<d-tag>" or "dev~<id>": the d-tag is author-controlled and may
// contain slashes, so anything outside a safe alphabet is escaped.
func storageFileFor(nappID string) string {
	return filepath.Join(dataDir, "storage", safeFileName(nappID)+".json")
}

// safeFileName is a napp id as a file name, without any path tricks.
func safeFileName(nappID string) string {
	var b strings.Builder
	for _, r := range nappID {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
			r == '-' || r == '_' || r == '.' || r == '~' {
			b.WriteRune(r)
		} else {
			b.WriteString("_")
		}
	}
	name := b.String()
	if name == "" {
		name = "_"
	}
	// belt and suspenders against ".." tricks: filepath.Base strips separators
	return filepath.Base(name)
}

// StorageFile is the path of a napp's localStorage file, so platforms that
// read the snapshot themselves (the desktop child process) can find it.
func StorageFile(nappID string) string { return storageFileFor(nappID) }

func storageFor(nappID string) *nappStorage {
	storagesMu.Lock()
	defer storagesMu.Unlock()
	if s, ok := storages[nappID]; ok {
		return s
	}
	s := &nappStorage{data: make(map[string]string)}
	// read the JSON file into memory on first access; missing/corrupt
	// means start empty rather than fail the window
	if raw, err := os.ReadFile(storageFileFor(nappID)); err == nil && len(raw) > 0 {
		var disk map[string]string
		if err := json.Unmarshal(raw, &disk); err == nil {
			for k, v := range disk {
				if s.size+len(k)+len(v) > localStorageQuota {
					break
				}
				s.data[k] = v
				s.size += len(k) + len(v)
			}
		}
	}
	storages[nappID] = s
	return s
}

// storageSnapshot returns a copy of a napp's store for injection at
// document-start. The JS shim runs synchronously from this snapshot; writes
// go back through the storageSet/Remove/Clear rpcs.
func storageSnapshot(nappID string) map[string]string {
	s := storageFor(nappID)
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]string, len(s.data))
	for k, v := range s.data {
		out[k] = v
	}
	return out
}

// storageSeed is what a window's localStorage shim starts from: the napp's
// snapshot, or nothing at all for a napplet, which has no localStorage (its
// sandboxed frame has no origin to keep one) and goes through NAP-STORAGE.
func storageSeed(napp Napp) string {
	if napp.IsNapplet() {
		return ""
	}
	return StorageSnapshotJSON(napp.ID)
}

// StorageSnapshotJSON is the snapshot as a JSON object string, for WindowSpec.
func StorageSnapshotJSON(nappID string) string {
	raw, err := json.Marshal(storageSnapshot(nappID))
	if err != nil {
		return "{}"
	}
	return string(raw)
}

func storagePersistLocked(nappID string, data map[string]string) error {
	dir := filepath.Join(dataDir, "storage")
	if err := os.MkdirAll(dir, 0755); err != nil {
		log.Error().Err(err).Msg("could not create storage dir")
		return err
	}
	raw, err := json.Marshal(data)
	if err != nil {
		log.Error().Err(err).Msg("could not marshal napp storage")
		return err
	}
	// write to disk on writes, atomically: temp file + rename
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		log.Error().Err(err).Msg("could not write napp storage")
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, storageFileFor(nappID)); err != nil {
		os.Remove(tmpName)
		log.Error().Err(err).Msg("could not persist napp storage")
		return err
	}
	return nil
}

var errQuotaExceeded = errors.New("localStorage quota exceeded (5MB)")

func storageSet(nappID, key, value string) error {
	return storageSetQuota(nappID, key, value, localStorageQuota, errQuotaExceeded)
}

// storageSetQuota is storageSet under a quota of the caller's choosing: napps
// get localStorage's 5MB, napplets NAP-STORAGE's smaller one.
func storageSetQuota(nappID, key, value string, quota int, errQuota error) error {
	s := storageFor(nappID)
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.data[key]
	delta := len(key) + len(value)
	if ok {
		delta -= len(key) + len(old)
	}
	if s.size+delta > quota {
		return errQuota
	}
	next := make(map[string]string, len(s.data)+1)
	for k, v := range s.data {
		next[k] = v
	}
	next[key] = value
	if err := storagePersistLocked(nappID, next); err != nil {
		return err
	}
	s.data = next
	s.size += delta
	return nil
}

func storageGet(nappID, key string) (string, bool) {
	s := storageFor(nappID)
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.data[key]
	return v, ok
}

// storageKeys lists a store's keys, sorted so the answer is stable.
func storageKeys(nappID string) []string {
	s := storageFor(nappID)
	s.mu.Lock()
	keys := make([]string, 0, len(s.data))
	for k := range s.data {
		keys = append(keys, k)
	}
	s.mu.Unlock()
	sort.Strings(keys)
	return keys
}

func storageRemove(nappID, key string) (bool, error) {
	s := storageFor(nappID)
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.data[key]; ok {
		next := make(map[string]string, len(s.data)-1)
		for k, v := range s.data {
			if k != key {
				next[k] = v
			}
		}
		if err := storagePersistLocked(nappID, next); err != nil {
			return false, err
		}
		s.data = next
		s.size -= len(key) + len(old)
		return true, nil
	}
	return false, nil
}

func storageClear(nappID string) (bool, error) {
	s := storageFor(nappID)
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.data) == 0 {
		return false, nil
	}
	next := make(map[string]string)
	if err := storagePersistLocked(nappID, next); err != nil {
		return false, err
	}
	s.data = next
	s.size = 0
	return true, nil
}

// broadcastStorage tells every other open window of the same napp about a
// mutation, so its shim applies it and fires the storage event browsers
// fire in every document sharing a store except the one that wrote.
// It rides the existing eval transport both shells already run, so no shell
// changes are needed. When the target page hasn't installed the hook yet
// (mid-boot) the snippet falls back to patching its __nappStorage seed,
// which the shim then starts from.
func broadcastStorage(nappID, exceptInstance, op, key, value string) {
	code := storageApplyCode(op, key, value)
	for _, ci := range runningForNapp(nappID) {
		if ci.instance == exceptInstance {
			continue
		}
		ci.eval(code)
	}
}

func storageApplyCode(op, key, value string) string {
	kb, err := json.Marshal(key)
	if err != nil {
		kb = []byte("null")
	}
	vb, err := json.Marshal(value)
	if err != nil {
		vb = []byte("null")
	}
	if op == "clear" {
		kb, vb = []byte("null"), []byte("null")
	}
	return `(function(op,k,v){var f=window.__bridge_storage_apply;` +
		`if(f){try{f(op,k,v)}catch(e){}return;}` +
		`try{var s=window.__nappStorage;` +
		`if(!s||typeof s!=="object")s=window.__nappStorage={};` +
		`if(op==="set")s[k]=v;` +
		`else if(op==="remove")delete s[k];` +
		`else if(op==="clear")window.__nappStorage={};` +
		`}catch(e){}})(` + jsonString(op) + `,` + string(kb) + `,` + string(vb) + `)`
}

func jsonString(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(b)
}
