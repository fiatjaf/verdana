package backend

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Dev napps are temporary, in-memory napps for development: they live only in
// this process (never in state.json), show up in the launcher's Dev list, and
// are served from a throwaway http server on loopback.
//
// A dev napp comes from either a local folder (its files are served directly
// from disk) or a dev-server url like http://localhost:5173 (used
// directly: the shell's bridge bindings don't depend on the page's origin, so
// no proxying is needed and HMR keeps working untouched).
//
// In both cases the napp is described by the same metadata.json the uploader
// turns into manifest tags on publish, and its id is "dev~<metadata id>".

// devMetadata is the metadata.json at the top level of a napp folder (next to
// index.html), or served by a dev server next to its index.
type devMetadata struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Icon        string   `json:"icon"`
	Description string   `json:"description"`
	Requires    []string `json:"requires"`
	Actions     []string `json:"actions"`

	// Preferred window size, like nostrapps: `initial_size` (snake_case,
	// camelCase accepted too) {width, height} object.
	InitialSize   *NappInitialSize `json:"initial_size"`
	InitialSizeCC *NappInitialSize `json:"initialSize"`
}

var devIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._~-]*$`)

// devNapp is one loaded dev napp.
type devNapp struct {
	napp Napp

	// source is "folder" or "url".
	source string

	// dir is set for folder napps: re-read on reload.
	dir string

	// target is set for url napps: the normalized dev server url.
	target string
}

var (
	devMu    sync.Mutex
	devNapps = make(map[string]*devNapp)
)

// pageURL is where the napp's shell should navigate: the throwaway server for
// folder napps, the dev server itself for url napps.
func (d *devNapp) pageURL() string {
	if d.source == "url" {
		return d.target + "/"
	}
	return devServerBase() + "/dev/" + d.napp.ID + "/"
}

func devLookup(id string) *devNapp {
	devMu.Lock()
	defer devMu.Unlock()
	return devNapps[id]
}

// DevSourceKind says where a loaded dev napp came from: "folder" or "url", ""
// when it isn't a dev napp the launcher knows.
func DevSourceKind(id string) string {
	if d := devLookup(id); d != nil {
		return d.source
	}
	return ""
}

// DevNapps lists the loaded dev napps, by name.
func DevNapps() []Napp {
	devMu.Lock()
	out := make([]Napp, 0, len(devNapps))
	for _, d := range devNapps {
		out = append(out, d.napp)
	}
	devMu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func refreshDev() {
	ls.mu.Lock()
	ls.dev = DevNapps()
	ls.mu.Unlock()
	notifyState()
}

func nappFromDevMetadata(meta devMetadata) (Napp, error) {
	if !devIDPattern.MatchString(meta.ID) {
		return Napp{}, fmt.Errorf("bad napp id %q in metadata.json", meta.ID)
	}
	name := meta.Title
	if name == "" {
		name = meta.ID
	}
	initial := meta.InitialSize
	if initial == nil {
		initial = meta.InitialSizeCC
	}
	if initial != nil {
		if s, ok := sanitizeInitialSize(initial.Width, initial.Height); ok {
			initial = &s
		} else {
			initial = nil
		}
	}
	return Napp{
		ID:          "dev~" + meta.ID,
		D:           meta.ID,
		Name:        name,
		Description: meta.Description,
		Icon:        meta.Icon,
		InitialSize: initial,
		Requires:    append([]string(nil), meta.Requires...),
		Actions:     append([]string(nil), meta.Actions...),
	}, nil
}

// ─── loading ─────────────────────────────────────────────────────

// devFileCap and devTotalCap keep a mistaken folder pick (a home directory, a
// checkout with build artifacts) from taking too long to index.
const (
	devFileCap  = 32 << 20
	devTotalCap = 256 << 20
)

// DevLoadFolder indexes a napp folder and registers it as a dev napp. Blocking:
// call it from a goroutine. Errors land in DevErr.
func DevLoadFolder(dir string) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		setDevErr("pick a napp folder first")
		return
	}
	setDevLoading(true)
	defer setDevLoading(false)

	napp, err := readDevFolder(dir)
	if err != nil {
		setDevErr(err.Error())
		return
	}
	devMu.Lock()
	devNapps[napp.ID] = &devNapp{napp: napp, source: "folder", dir: dir}
	devMu.Unlock()
	setDevErr("")
	refreshDev()
	log.Info().Str("napp", napp.ID).Str("dir", dir).Int("files", len(napp.Paths)).Msg("dev napp loaded from folder")
}

// DevLoadURL registers a dev-server url as a dev napp, reading its
// metadata.json from the server itself. Blocking: call it from a goroutine.
func DevLoadURL(rawurl string) {
	rawurl = strings.TrimSpace(rawurl)
	if rawurl == "" {
		setDevErr("type a dev server url first")
		return
	}
	if !strings.Contains(rawurl, "://") {
		rawurl = "http://" + rawurl
	}
	setDevLoading(true)
	defer setDevLoading(false)

	target, meta, err := readDevURL(rawurl)
	if err != nil {
		setDevErr(err.Error())
		return
	}
	napp, err := nappFromDevMetadata(meta)
	if err != nil {
		setDevErr(err.Error())
		return
	}
	devMu.Lock()
	devNapps[napp.ID] = &devNapp{napp: napp, source: "url", target: target}
	devMu.Unlock()
	setDevErr("")
	refreshDev()
	log.Info().Str("napp", napp.ID).Str("url", target).Msg("dev napp loaded from url")
}

// DevReload re-reads a folder dev napp from disk. Blocking: call it from a
// goroutine.
func DevReload(id string) {
	d := devLookup(id)
	if d == nil {
		setDevErr("dev napp " + id + " is not loaded")
		return
	}
	if d.source != "folder" {
		return
	}
	DevLoadFolder(d.dir)
}

// DevUnload forgets a dev napp, closing its windows first.
func DevUnload(id string) {
	for _, ci := range runningForNapp(id) {
		ci.Close()
	}
	devMu.Lock()
	delete(devNapps, id)
	devMu.Unlock()
	refreshDev()
	log.Info().Str("napp", id).Msg("dev napp unloaded")
}

// LaunchDev opens a loaded dev napp. Failures show up as the dev tab's error.
func LaunchDev(id string) {
	d := devLookup(id)
	if d == nil {
		setDevErr("dev napp " + id + " is not loaded")
		return
	}
	Launch(d.napp)
}

// readDevFolder reads metadata.json and indexes every regular file under dir.
// File contents are only streamed while calculating their hashes.
func readDevFolder(dir string) (Napp, error) {
	st, err := os.Stat(dir)
	if err != nil {
		return Napp{}, fmt.Errorf("can't read %s: %w", dir, err)
	}
	if !st.IsDir() {
		return Napp{}, fmt.Errorf("%s is not a folder", dir)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "metadata.json"))
	if err != nil {
		return Napp{}, fmt.Errorf("no metadata.json in %s", dir)
	}
	var meta devMetadata
	if err := json.Unmarshal(raw, &meta); err != nil {
		return Napp{}, fmt.Errorf("unreadable metadata.json: %w", err)
	}
	napp, err := nappFromDevMetadata(meta)
	if err != nil {
		return Napp{}, err
	}

	var total int64
	err = filepath.WalkDir(dir, func(p string, de os.DirEntry, err error) error {
		if err != nil || !de.Type().IsRegular() {
			return err
		}
		info, err := de.Info()
		if err != nil {
			return err
		}
		if info.Size() > devFileCap {
			return fmt.Errorf("%s is bigger than %dMB, refusing", p, devFileCap>>20)
		}
		file, err := os.Open(p)
		if err != nil {
			return err
		}
		total += info.Size()
		if total > devTotalCap {
			file.Close()
			return fmt.Errorf("folder holds more than %dMB, refusing", devTotalCap>>20)
		}
		sum := sha256.New()
		_, copyErr := io.Copy(sum, file)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		key := "/" + filepath.ToSlash(rel)
		napp.Paths = append(napp.Paths, NappPath{Path: key, Sha256: hex.EncodeToString(sum.Sum(nil))})
		return nil
	})
	if err != nil {
		return Napp{}, err
	}
	indexInfo, err := os.Stat(filepath.Join(dir, "index.html"))
	if err != nil || !indexInfo.Mode().IsRegular() {
		return Napp{}, fmt.Errorf("no index.html in %s", dir)
	}
	sort.Slice(napp.Paths, func(i, j int) bool { return napp.Paths[i].Path < napp.Paths[j].Path })
	return napp, nil
}

// readDevURL normalizes the url and fetches the metadata.json the dev server
// must serve next to its index.
func readDevURL(rawurl string) (string, devMetadata, error) {
	u, err := url.Parse(rawurl)
	if err != nil {
		return "", devMetadata{}, fmt.Errorf("bad url %q", rawurl)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", devMetadata{}, fmt.Errorf("dev url must be http(s)")
	}
	if u.Host == "" {
		return "", devMetadata{}, fmt.Errorf("bad url %q", rawurl)
	}
	target := strings.TrimSuffix(u.Scheme+"://"+u.Host+u.Path, "/")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target+"/metadata.json", nil)
	if err != nil {
		return "", devMetadata{}, fmt.Errorf("bad url %q", rawurl)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", devMetadata{}, fmt.Errorf("can't reach %s: %w", target, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", devMetadata{}, fmt.Errorf("%s has no metadata.json (status %s)", target, resp.Status)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", devMetadata{}, fmt.Errorf("can't read %s/metadata.json: %w", target, err)
	}
	var meta devMetadata
	if err := json.Unmarshal(raw, &meta); err != nil {
		return "", devMetadata{}, fmt.Errorf("unreadable metadata.json at %s: %w", target, err)
	}
	if !devIDPattern.MatchString(meta.ID) {
		return "", devMetadata{}, fmt.Errorf("bad napp id %q in metadata.json", meta.ID)
	}
	return target, meta, nil
}

// ─── icons ───────────────────────────────────────────────────────

// devIconBlob serves a dev napp's icon: from disk for folder
// napps, from the dev server itself for url napps. The second result is false
// when this isn't a dev napp's icon, and IconBlob falls through to disk.
func devIconBlob(ctx context.Context, n Napp) ([]byte, bool) {
	d := devLookup(n.ID)
	if d == nil {
		return nil, false
	}
	want := strings.TrimPrefix(strings.TrimSpace(n.Icon), "/")
	if want == "" {
		return nil, false
	}
	if d.source == "folder" {
		filePath := filepath.Join(d.dir, filepath.FromSlash(want))
		rel, err := filepath.Rel(d.dir, filePath)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, false
		}
		file, err := os.Open(filePath)
		if err != nil {
			return nil, false
		}
		defer file.Close()
		if info, err := file.Stat(); err != nil || !info.Mode().IsRegular() {
			return nil, false
		}
		data, err := io.ReadAll(io.LimitReader(file, 5<<20))
		return data, err == nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.target+"/"+want, nil)
	if err != nil {
		return nil, false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			resp.Body.Close()
		}
		return nil, false
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20))
	if err != nil {
		return nil, false
	}
	return data, true
}

// ─── the throwaway server ────────────────────────────────────────

var (
	devSrvMu   sync.Mutex
	devSrvBase string
)

// devServerBase is the throwaway http server folder dev napps are served
// from, started on first use. Loopback only, one per process: dev napps are
// ephemeral, so there is nothing to persist or share.
func devServerBase() string {
	devSrvMu.Lock()
	defer devSrvMu.Unlock()
	if devSrvBase != "" {
		return devSrvBase
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Error().Err(err).Msg("dev server failed to listen")
		return ""
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/dev/", serveDevFile)
	mux.HandleFunc("/", serveDevRootFile)
	go http.Serve(ln, mux)
	devSrvBase = "http://" + ln.Addr().String()
	log.Info().Str("addr", devSrvBase).Msg("dev server up")
	return devSrvBase
}

// serveDevRootFile handles absolute asset URLs such as /index.js. The page's
// same-origin Referer identifies which folder napp owns the request.
func serveDevRootFile(wr http.ResponseWriter, r *http.Request) {
	ref, err := url.Parse(r.Referer())
	if err != nil {
		http.NotFound(wr, r)
		return
	}
	rest := strings.TrimPrefix(ref.Path, "/dev/")
	id, _, ok := strings.Cut(rest, "/")
	if !ok || id == "" {
		http.NotFound(wr, r)
		return
	}
	if d := devLookup(id); d == nil || d.source != "folder" {
		http.NotFound(wr, r)
		return
	}
	request := r.Clone(r.Context())
	request.URL.Path = "/dev/" + id + r.URL.Path
	serveDevFile(wr, request)
}

// serveDevFile serves one folder dev napp's files directly from disk at
// /dev/<id>/..., with the same SPA fallback to index.html the napp shells
// use for installed napps.
func serveDevFile(wr http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/dev/")
	id, f, _ := strings.Cut(rest, "/")

	d := devLookup(id)
	if d == nil || d.source != "folder" {
		http.NotFound(wr, r)
		return
	}
	key := path.Clean("/" + f)
	filePath := filepath.Join(d.dir, filepath.FromSlash(strings.TrimPrefix(key, "/")))
	file, err := os.Open(filePath)
	if err == nil {
		info, statErr := file.Stat()
		if statErr != nil || !info.Mode().IsRegular() {
			file.Close()
			err = os.ErrNotExist
		}
	}
	if err != nil {
		// extensionless app routes fall back to the entrypoint
		if key != "/index.html" && !strings.Contains(path.Base(key), ".") {
			file, err = os.Open(filepath.Join(d.dir, "index.html"))
		}
	}
	if err != nil {
		http.NotFound(wr, r)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(wr, r)
		return
	}
	if ct := mime.TypeByExtension(path.Ext(key)); ct != "" {
		wr.Header().Set("Content-Type", ct)
	}
	http.ServeContent(wr, r, info.Name(), info.ModTime(), file)
}

// ─── errors ──────────────────────────────────────────────────────

// SetDevErr shows an error on the dev tab (a GUI may also use it for its own
// dev failures).
func SetDevErr(msg string) {
	ls.mu.Lock()
	ls.devErr = msg
	ls.mu.Unlock()
	notifyState()
}

func setDevErr(msg string) { SetDevErr(msg) }

func setDevLoading(loading bool) {
	ls.mu.Lock()
	ls.devLoading = loading
	ls.mu.Unlock()
	notifyState()
}
