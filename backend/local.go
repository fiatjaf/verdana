package backend

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"verdana/backend/wasm"

	"fiatjaf.com/nostr"
)

// Local napps are installed napps whose files came from this device instead
// of from an event: a folder the user installed, or the files a napp that
// declares `requires: ["dev"]` handed over with napp.dev.installLocal. Their id
// is "local~" + their metadata.json id, and they live where installed napps
// do (state.json, nappBaseDir) with all their files — metadata.json among
// them, so the folder can be read back and published like a dev folder.
//
// Unlike a dev napp they stay installed across restarts and their files are a
// copy: nothing reads from where they came from again. They have no author
// and no updates; installing the same id again replaces them.

// localInstallMaxFiles caps how many files napp.dev.installLocal takes.
const localInstallMaxFiles = 5000

// localInstallMu keeps two installs from replacing the same napp at once.
var localInstallMu sync.Mutex

// InstallLocalFolder copies a napp folder in as a local napp and opens it (or
// reloads its open windows). Blocking: call it from a goroutine. Errors land
// in DevErr: the folder comes from the dev tab.
func InstallLocalFolder(dir string) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		setDevErr("pick a napp folder first")
		return
	}
	setDevLoading(true)
	defer setDevLoading(false)

	files, err := readLocalFolder(dir)
	if err == nil {
		var napp Napp
		if napp, err = installLocalNapp(files, ""); err == nil {
			err = openLocalNapp(napp)
		}
	}
	if err != nil {
		setDevErr(err.Error())
		return
	}
	setDevErr("")
}

// readLocalFolder reads every file of a napp folder, as readDevFolder indexes
// them (no hidden files, the same size caps).
func readLocalFolder(dir string) (map[string][]byte, error) {
	indexed, err := readDevFolder(dir)
	if err != nil {
		return nil, err
	}
	files := make(map[string][]byte, len(indexed.Paths))
	for _, p := range indexed.Paths {
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(p.Path[1:])))
		if err != nil {
			return nil, err
		}
		files[p.Path] = data
	}
	return files, nil
}

// installLocalFile is one file napp.dev.installLocal hands over.
type installLocalFile struct {
	Path string `json:"path"`
	Data string `json:"data"` // base64
	Type string `json:"type"`
}

// installLocalForNapp answers napp.dev.installLocal: {nappId}, or nil when the
// user said no.
func installLocalForNapp(ci *Instance, raw []installLocalFile) (any, error) {
	if !slices.Contains(ci.napp.Requires, "dev") {
		return nil, errors.New("dev access not granted: napp.dev.installLocal")
	}
	if len(raw) == 0 {
		return nil, errors.New("installLocal: no files")
	}
	if len(raw) > localInstallMaxFiles {
		return nil, fmt.Errorf("installLocal: at most %d files", localInstallMaxFiles)
	}

	files := make(map[string][]byte, len(raw))
	var total int64
	for _, f := range raw {
		segments := strings.FieldsFunc(f.Path, func(r rune) bool { return r == '/' })
		if len(segments) == 0 || slices.ContainsFunc(segments, func(s string) bool {
			return s == "." || s == ".." || strings.ContainsAny(s, "\\:\x00")
		}) {
			return nil, fmt.Errorf("installLocal: bad path %q", f.Path)
		}
		key := "/" + strings.Join(segments, "/")
		if _, dup := files[key]; dup {
			return nil, fmt.Errorf("installLocal: %s given twice", key)
		}
		data, err := base64.StdEncoding.DecodeString(f.Data)
		if err != nil {
			return nil, fmt.Errorf("installLocal: %s: data must be base64", key)
		}
		if len(data) > devFileCap {
			return nil, fmt.Errorf("installLocal: %s is bigger than %dMB", key, devFileCap>>20)
		}
		total += int64(len(data))
		if total > devTotalCap {
			return nil, fmt.Errorf("installLocal: more than %dMB in all", devTotalCap>>20)
		}
		files[key] = data
	}

	meta, err := localMetadata(files)
	if err != nil {
		return nil, fmt.Errorf("installLocal: %w", err)
	}
	id := LocalPrefix + meta.ID
	if id == ci.napp.ID {
		return nil, errors.New("installLocal: a napp can't replace itself")
	}

	// The first install asks; the napp that installed it may replace it
	// again without asking, which is what a napp building another one does
	// on every change.
	prev, installed := InstalledNapp(id)
	if !installed || prev.InstalledBy != ci.napp.ID {
		name := meta.Title
		if name == "" {
			name = meta.ID
		}
		detail := "“" + name + "” (" + strconv.Itoa(len(files)) + " files) gets installed on this device"
		if installed {
			detail += ", replacing the one installed now"
		}
		if len(meta.Requires) > 0 {
			detail += ". It requires " + strings.Join(meta.Requires, ", ")
		}
		if !askApproval(ci, PermDevInstall, "install a local napp", PromptBody{Detail: detail + "."}) {
			return nil, nil
		}
	}

	napp, err := installLocalNapp(files, ci.napp.ID)
	if err != nil {
		return nil, fmt.Errorf("installLocal: %w", err)
	}
	if err := openLocalNapp(napp); err != nil {
		return nil, fmt.Errorf("installLocal: installed, but %w", err)
	}
	return map[string]any{"nappId": napp.ID}, nil
}

// localMetadata reads and checks the metadata.json among a local napp's files,
// and that there is something to run beside it.
func localMetadata(files map[string][]byte) (devMetadata, error) {
	raw, ok := files["/metadata.json"]
	if !ok {
		return devMetadata{}, errors.New("missing metadata.json")
	}
	var meta devMetadata
	if err := json.Unmarshal(raw, &meta); err != nil {
		return devMetadata{}, fmt.Errorf("unreadable metadata.json: %w", err)
	}
	if !devIDPattern.MatchString(meta.ID) {
		return devMetadata{}, fmt.Errorf("bad napp id %q in metadata.json", meta.ID)
	}
	_, hasIndex := files["/index.html"]
	_, hasModule := files["/"+wasm.DefaultEntry]
	if !hasIndex && !hasModule {
		return devMetadata{}, fmt.Errorf("no index.html (nor %s)", wasm.DefaultEntry)
	}
	return meta, nil
}

// installLocalNapp writes a local napp's files in place of whatever was
// installed under its id and records it as installed. by is the napp that
// handed the files over, if one did.
func installLocalNapp(files map[string][]byte, by string) (Napp, error) {
	meta, err := localMetadata(files)
	if err != nil {
		return Napp{}, err
	}
	id := LocalPrefix + meta.ID

	localInstallMu.Lock()
	defer localInstallMu.Unlock()
	setBusy(id, true)
	defer setBusy(id, false)

	base := nappBaseDir(id)
	err = replaceDir(base, func(tmp string) error {
		for key, data := range files {
			p := filepath.Join(tmp, filepath.FromSlash(key[1:]))
			if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
				return err
			}
			if err := os.WriteFile(p, data, 0644); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return Napp{}, err
	}

	// indexed as a dev folder is (hashes, kind), under the local id
	napp, err := readDevFolder(base)
	if err != nil {
		return Napp{}, err
	}
	napp.ID = id
	napp.CreatedAt = nostr.Now()
	napp.InstalledBy = by

	stateMu.Lock()
	if state.InstalledNapps == nil {
		state.InstalledNapps = make(map[string]Napp)
	}
	state.InstalledNapps[id] = napp
	delete(state.PreviewNapps, id)
	saveState()
	stateMu.Unlock()

	refreshInstalled()
	log.Info().Str("napp", id).Str("by", by).Int("files", len(napp.Paths)).Msg("local napp installed")
	return napp, nil
}

// openLocalNapp opens a just-installed local napp, or puts its open windows on
// the new files: a page reloads in place, a wasm module has no page to reload,
// so its window is closed and opened anew.
func openLocalNapp(napp Napp) error {
	open := runningForNapp(napp.ID)
	if len(open) == 0 {
		if _, err := launch(context.Background(), napp); err != nil {
			return fmt.Errorf("launch failed: %w", err)
		}
		return nil
	}
	for _, ci := range open {
		if napp.IsWasm() || ci.napp.IsWasm() {
			ci.Close()
			go func() {
				if _, err := launch(context.Background(), napp); err != nil {
					log.Error().Err(err).Str("napp", napp.ID).Msg("relaunch after install failed")
				}
			}()
			continue
		}
		ci.eval("location.reload()")
	}
	return nil
}
