package backend

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"fiatjaf.com/nostr"
)

// NappPath is one file of a napp: where it goes and the blob that holds it.
type NappPath struct {
	Path   string `json:"path"`
	Sha256 string `json:"sha256"`
}

// Napp is a napp as its kind:35130 event describes it.
type Napp struct {
	ID          string          `json:"id"`
	D           string          `json:"d"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Icon        string          `json:"icon"`
	Author      nostr.PubKey    `json:"author"`
	AuthorName  string          `json:"authorName,omitempty"`
	Actions     []string        `json:"actions"`
	Requires    []string        `json:"requires"`
	Singleton   bool            `json:"singleton"`
	CreatedAt   nostr.Timestamp `json:"created_at"`
	Paths       []NappPath      `json:"paths"`
	Servers     []string        `json:"servers"`

	// UpdateAvailable is stamped by Snapshot(): a newer version of this napp
	// was seen on the relays (kind:35130, same author+d-tag, newer
	// created_at). It is not part of the wire model.
	UpdateAvailable bool `json:"updateAvailable"`
}

// Label is the napp's name, falling back to its id.
func (n Napp) Label() string {
	if n.Name != "" {
		return n.Name
	}
	return n.ID
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

// InstalledNapp looks an installed napp up by id.
func InstalledNapp(id string) (Napp, bool) {
	stateMu.Lock()
	defer stateMu.Unlock()
	n, ok := state.InstalledNapps[id]
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
	local := filepath.Join(nappBaseDir(n.ID), filepath.FromSlash(strings.TrimPrefix(asset.Path, "/")))
	if data, err := os.ReadFile(local); err == nil {
		return data, nil
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

var (
	authorNameMu      sync.Mutex
	authorNameCache   = make(map[nostr.PubKey]string)
	authorNamePending = make(map[nostr.PubKey]bool)
)

// AuthorShortName is the napp author's short name, as far as it is known: from
// the sdk cache or the launcher's user index, without touching the network.
// When it is not known yet, a background resolution is kicked off (which
// re-notifies the state when it lands) and "" is returned meanwhile. It never
// blocks, so a render loop can call it per-napp per-frame.
func (n Napp) AuthorShortName() string {
	if n.Author == nostr.ZeroPK {
		return ""
	}

	authorNameMu.Lock()
	if name := authorNameCache[n.Author]; name != "" {
		authorNameMu.Unlock()
		return name
	}
	authorNameMu.Unlock()

	// the user index often knows the author already (kind:0s in the local
	// eventstore), which saves the network round entirely
	userIndexMu.RLock()
	if u, ok := userIndex[n.Author]; ok {
		name := u.pm.ShortName()
		userIndexMu.RUnlock()
		authorNameMu.Lock()
		authorNameCache[n.Author] = name
		authorNameMu.Unlock()
		return name
	}
	userIndexMu.RUnlock()

	pk := n.Author
	authorNameMu.Lock()
	pending := authorNamePending[pk]
	authorNamePending[pk] = true
	authorNameMu.Unlock()
	if pending {
		return ""
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		name := ""
		if sys != nil {
			name = sys.FetchProfileMetadata(ctx, pk).ShortName()
		}
		authorNameMu.Lock()
		authorNameCache[pk] = name
		authorNameMu.Unlock()
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
