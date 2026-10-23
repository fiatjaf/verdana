package backend

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"fiatjaf.com/nostr"

	"verdana/backend/wasm"
)

// NappPath is one file of a napp: where it goes and the blob that holds it.
type NappPath struct {
	Path   string `json:"path"`
	Sha256 string `json:"sha256"`
}

// NappInitialSize is a napp's preferred window size, mirroring nostrapps:
// ["initial_size", "<width>", "<height>"] manifest tags, or an `initial_size`
// {width, height} object in metadata.json.
type NappInitialSize struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// Default window size for napps without (or with an invalid) initial_size.
// Deliberately roomy: most napps are web pages that look cramped in small
// windows.
const (
	DefaultWindowWidth  = 1024
	DefaultWindowHeight = 700
)

// sanitizeInitialSize clamps untrusted napp input like nostrapps does: finite,
// positive, capped at 2000px. False when absent/invalid.
func sanitizeInitialSize(width, height int) (NappInitialSize, bool) {
	if width <= 0 || height <= 0 {
		return NappInitialSize{}, false
	}
	if width > 2000 {
		width = 2000
	}
	if height > 2000 {
		height = 2000
	}
	return NappInitialSize{Width: width, Height: height}, true
}

// WindowSize is the size a napp window should open at: its initial_size when
// valid, the mode's default otherwise. Minimums keep a degenerate value from
// making an unusable window.
func (n Napp) WindowSize() (int, int) {
	defW, defH := DefaultWindowWidth, DefaultWindowHeight
	if n.IsWasm() {
		defW, defH = wasm.DefaultWidth, wasm.DefaultHeight
	}
	if n.InitialSize != nil {
		if s, ok := sanitizeInitialSize(n.InitialSize.Width, n.InitialSize.Height); ok {
			w, h := s.Width, s.Height
			if w < 320 {
				w = 320
			}
			if h < 240 {
				h = 240
			}
			return w, h
		}
	}
	return defW, defH
}

// What a napp is is its event kind, the way an nsite is 35128 and a napplet
// is 35129: a web napp is a page a webview loads, a wasm napp is a module it
// runs. Kind decides; tags describe. Everything else about a napp window is
// the same either way.
const (
	// KindWeb is the usual napp: a page, served and loaded.
	KindWeb = nostr.Kind(35130)

	// KindWasm is a napp made of one WebAssembly module that paints into a
	// canvas the launcher hands it. See wasm and WASM.md.
	KindWasm = nostr.Kind(35131)
)

// How a napp is hosted, as the window shells see it: ModeWeb loads the napp's
// index.html, ModeWasm runs its module. Backend.Napp.Kind is the source of
// truth; this is only the word the shell is picked by.
const (
	// ModeWeb is the default: the napp's index.html, served and loaded.
	ModeWeb = "web"

	// ModeWasm is a napp made of one WebAssembly module that paints into a
	// canvas the launcher hands it. See wasm and WASM.md.
	ModeWasm = "wasm"
)

// Napp is a napp as its kind:35130 or kind:35131 event describes it.
type Napp struct {
	ID          string           `json:"id"`
	D           string           `json:"d"`
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Icon        string           `json:"icon"`
	Author      nostr.PubKey     `json:"author"`
	AuthorName  string           `json:"authorName,omitempty"`
	Actions     []string         `json:"actions"`
	Requires    []string         `json:"requires"`
	InitialSize *NappInitialSize `json:"initialSize,omitempty"`
	CreatedAt   nostr.Timestamp  `json:"created_at"`
	Paths       []NappPath       `json:"paths"`
	Servers     []string         `json:"servers"`

	// Kind is the event kind that described the napp: 35130 for a page,
	// 35131 for a module. Zero reads as 35130, which is what every record
	// written before kinds split carries.
	Kind nostr.Kind `json:"kind,omitempty"`

	// InstalledBy is, for a local napp put in by napp.dev.installLocal, the
	// napp that did it: it may replace this one again without asking.
	InstalledBy string `json:"installedBy,omitempty"`

	// UpdateAvailable is stamped by Snapshot(): a newer version of this napp
	// was seen on the relays (same kind, same author+d-tag, newer
	// created_at). It is not part of the wire model.
	UpdateAvailable *Napp `json:"updateAvailable"`
}

// Label is the napp's name, falling back to its id.
func (n Napp) Label() string {
	if n.Name != "" {
		return n.Name
	}
	return n.ID
}

// LocalPrefix starts the id of a local napp: one installed from files on this
// device (a folder, or what a dev napp handed over with napp.dev.installLocal)
// instead of from an event. It is "local~" + its metadata.json id.
const LocalPrefix = "local~"

// IsLocal says whether this is a local napp: installed from files, with no
// event, no author and no updates, until it is published.
func (n Napp) IsLocal() bool { return strings.HasPrefix(n.ID, LocalPrefix) }

// HasAuthor says whether the napp came from someone's event: local and dev
// napps have no author.
func (n Napp) HasAuthor() bool { return n.Author != nostr.ZeroPK }

// KindOf is the event kind that described the napp, with the past folded in:
// records written before the kinds split carry zero and were all pages.
func (n Napp) KindOf() nostr.Kind {
	if n.Kind == KindWasm {
		return KindWasm
	}
	return KindWeb
}

// Mode is how the napp gets hosted: ModeWeb for the usual page-in-a-webview,
// ModeWasm for a module painting a canvas.
func (n Napp) Mode() string {
	if n.KindOf() == KindWasm {
		return ModeWasm
	}
	return ModeWeb
}

// IsWasm says whether this is a wasm napp: a module instead of a page.
func (n Napp) IsWasm() bool { return n.Mode() == ModeWasm }

// WasmFile is where the napp's module lives on disk once the napp is
// installed (or, for a dev napp, is loaded from a folder): always
// /app.wasm. False when the file is not there — a wasm napp with no module
// has nothing to run.
func (n Napp) WasmFile() (string, bool) {
	root := nappBaseDir(n.ID)
	if d := devLookup(n.ID); d != nil && d.source == "folder" {
		root = d.dir
	}
	abs := filepath.Join(root, wasm.DefaultEntry)
	if st, err := os.Stat(abs); err != nil || !st.Mode().IsRegular() {
		return "", false
	}
	return abs, true
}

// Handles says whether the napp declares this action. A napp declaring "view"
// handles every "view:<kind>" dispatch.
func (n Napp) Handles(action string) bool {
	for _, a := range n.Actions {
		if a == action || (a == "view" && strings.HasPrefix(action, "view:")) {
			return true
		}
	}
	return false
}

// InstalledNapp looks a napp that is on disk up by id: installed or
// previewed, which work the same everywhere but on the installed list and in
// the choice of action handlers.
func InstalledNapp(id string) (Napp, bool) {
	stateMu.Lock()
	defer stateMu.Unlock()
	return localNappLocked(id)
}

// localNappLocked is InstalledNapp with stateMu held.
func localNappLocked(id string) (Napp, bool) {
	if n, ok := state.InstalledNapps[id]; ok {
		return n, true
	}
	n, ok := state.PreviewNapps[id]
	return n, ok
}

// ─── icons ───────────────────────────────────────────────────────

// iconAsset resolves the napp's "icon" tag to the file it names. The tag is a
// path into the napp itself ("/icon.png"), which the event also carries a
// "path" tag for, so the icon is a blob like everything else. The leading
// slash is optional on either side, so both are trimmed before comparing.
func (n Napp) iconAsset() (NappPath, bool) {
	want := strings.TrimPrefix(n.Icon, "/")
	if want == "" {
		return NappPath{}, false
	}
	for _, p := range n.Paths {
		if strings.TrimPrefix(p.Path, "/") == want {
			return p, true
		}
	}
	return NappPath{}, false
}

// IconHash identifies a napp's icon: the blob hash, so a GUI can cache the
// decoded image by it. Empty when the napp declares no icon.
func (n Napp) IconHash() string {
	if asset, ok := n.iconAsset(); ok {
		return asset.Sha256
	}
	return ""
}

// IconBlob loads the bytes of a napp's icon: from the install directory when
// the napp is installed, from its author's blossom servers otherwise. The
// GUIs decode and cache it themselves (keyed by IconHash).
func (n Napp) IconBlob(ctx context.Context) ([]byte, error) {
	if data, ok := devIconBlob(ctx, n); ok {
		return data, nil
	}
	asset, ok := n.iconAsset()
	if !ok {
		return nil, errNotFound("this napp has no icon")
	}
	if rel := filepath.FromSlash(strings.TrimPrefix(asset.Path, "/")); filepath.IsLocal(rel) {
		if data, err := os.ReadFile(filepath.Join(nappBaseDir(n.ID), rel)); err == nil {
			return data, nil
		}
	}
	return downloadBlob(ctx, n.BlossomServers(ctx), asset.Sha256)
}

// ─── blossom servers ─────────────────────────────────────────────

// blossomServers is where a napp's blobs may live, most specific first: the
// servers the napp event named itself, then the author's own blossom server
// list (kind:10063, through the sdk), then ours as a last resort.
func (n Napp) BlossomServers(ctx context.Context) []string {
	servers := make([]string, 0, 8)
	add := func(raw string) {
		url, err := nostr.NormalizeHTTPURL(raw)
		if err != nil || url == "" {
			return
		}
		if !slices.Contains(servers, url) {
			servers = append(servers, url)
		}
	}

	add("https://relay.nostrapps.com")

	for _, srv := range n.Servers {
		add(srv)
	}

	if sys != nil && n.Author != nostr.ZeroPK {
		listCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		list := sys.FetchBlossomServerList(listCtx, n.Author)
		cancel()
		for _, srv := range list.Items {
			add(string(srv))
		}
		log.Debug().Str("napp", n.ID).Int("authored", len(list.Items)).
			Msg("loaded the author's blossom servers")
	}

	add("https://nostr.download")

	return servers
}

// ─── author ──────────────────────────────────────────────────────

// Author is the napp author's short name and picture url, from whatever the
// sdk has cached or can fetch.
func (n Napp) AuthorProfile(ctx context.Context) (string, string) {
	if sys == nil || n.Author == nostr.ZeroPK {
		return "", ""
	}
	pm := sys.FetchProfileMetadata(ctx, n.Author)
	return pm.ShortName(), pm.Picture
}

// ─── author names ────────────────────────────────────────────────
// AuthorShortName is the napp author's short name, as far as it is known: from
// the sdk cache or the launcher's user index, without touching the network.
// When it is not known yet, a background resolution is kicked off (which
// re-notifies the state when it lands) and "" is returned meanwhile. It never
// blocks, so a render loop can call it per-napp per-frame.
func (n Napp) AuthorShortName() string {
	if n.Author == nostr.ZeroPK {
		return ""
	}

	if v, ok := sys.MetadataCache.Get(n.Author); ok {
		return v.ShortName()
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		sys.FetchProfileMetadata(ctx, n.Author)
		notifyState()
	}()
	return ""
}

// MatchesQuery says whether the napp matches a case-insensitive substring in
// its name, description, author pubkey or author name.
func (n Napp) MatchesQuery(q string) bool {
	if q == "" {
		return true
	}
	return strings.Contains(strings.ToLower(n.Name), q) ||
		strings.Contains(strings.ToLower(n.Description), q) ||
		strings.Contains(strings.ToLower(n.Author.Hex()), q) ||
		strings.Contains(strings.ToLower(n.AuthorShortName()), q)
}
