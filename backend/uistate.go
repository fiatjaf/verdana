package backend

import (
	"context"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/puzpuzpuz/xsync/v3"
)

// Everything a launcher UI draws lives here, so the Gio window and the Compose
// screen render the same thing from the same source. A GUI never mutates it:
// it calls the actions (Login, Fetch, Install…), gets a StateChanged callback
// and reads a fresh Snapshot.

// Phases of the launcher.
const (
	PhaseLoading = "loading"
	PhaseLogin   = "login"
	PhaseMain    = "main"
)

// State is an immutable snapshot of the launcher. The json tags are part of
// the contract with the Android UI, which reads this over the gomobile
// binding.
type State struct {
	// Phase is "loading", "login" or "main".
	Phase string `json:"phase"`

	// LoginErr is why the last login attempt failed, if it did.
	LoginErr string `json:"loginErr"`

	// LoginStatus is what a login that takes a while (the Google one) is
	// doing right now, "" when none is running.
	LoginStatus string `json:"loginStatus"`

	// ProfileName and ProfilePicture describe the logged-in user.
	ProfileName    string `json:"profileName"`
	ProfilePicture string `json:"profilePicture"`
	Pubkey         string `json:"pubkey"`

	// FetchErr is the last discovery/install/launch error worth showing.
	FetchErr string `json:"fetchErr"`

	// Fetching is true while discovery is running.
	Fetching bool `json:"fetching"`

	// Theme is "light" or "dark".
	Theme string `json:"theme"`

	// ReadAuth is whether relays that ask for it get authenticated to when
	// reading.
	ReadAuth bool `json:"readAuth"`

	// Relays are the discovery relays.
	Relays []string `json:"relays"`

	// Installed napps, with updates available first, then most opened
	// first. Discovery holds the ones the relays found, with the
	// ones not installed yet first — see sortDiscovery.
	Installed []Napp `json:"installed"`
	Discovery []Napp `json:"discovery"`

	// Previews are the napps opened without installing them (see
	// preview.go), by name: on disk like the installed ones, but kept
	// apart from them.
	Previews []Napp `json:"previews"`

	// Pinned are the installed napps the user put on the home tab, in the
	// order they were pinned.
	Pinned []Napp `json:"pinned"`

	// Home is everything on the home tab, pinned napps and pinned bundles
	// together, in the order the user put them in.
	Home []HomeItem `json:"home"`

	// Dev napps are the ephemeral in-memory ones loaded from a folder or a
	// dev-server url (see dev.go): shown on the launcher's dev tab, never
	// persisted.
	Dev []Napp `json:"dev"`

	// DevErr is the last dev-tab failure worth showing, DevLoading is true
	// while a dev napp is being read in.
	DevErr     string `json:"devErr"`
	DevLoading bool   `json:"devLoading"`

	// Busy holds the ids of napps being installed, uninstalled or updated.
	Busy []string `json:"busy"`

	// UpdateCheckRunning is true while the launcher is looking for newer
	// versions of the installed napps (the "check for updates" button).
	UpdateCheckRunning bool `json:"updateCheckRunning"`

	// Windows are the napp instances currently open.
	Windows []WindowInfo `json:"windows"`

	// ManagedWindows includes the open windows and the ones closed earlier
	// in this run. Closed entries stay visible so they can be reopened
	// without losing their state; nothing here outlives the launcher.
	ManagedWindows []WindowInfo `json:"managedWindows"`

	// Bundles are the bundles the user made (see bundles.go), listed on the
	// Windows screen with whether each is pinned and has an OS shortcut.
	Bundles []BundleInfo `json:"bundles"`
}

// HomeItem is one cell of the home tab: a napp or a bundle, never both. Key
// is what MovePin calls it.
type HomeItem struct {
	Key    string      `json:"key"`
	Napp   *Napp       `json:"napp,omitempty"`
	Bundle *BundleInfo `json:"bundle,omitempty"`
}

// WindowInfo is one open napp instance, for a window list or tab switcher.
type WindowInfo struct {
	Instance string `json:"instance"`
	NappID   string `json:"nappId"`
	Name     string `json:"name"`

	// Action is what the window is currently showing, when the napp told us
	// (a dispatched action, or one it pushed itself).
	Action string `json:"action"`
	Open   bool   `json:"open"`

	// History is every action this window was sent this run, oldest first:
	// what a reopen puts it back on and what the bundle editor offers as the
	// actions of its napp. Only the Windows tab listing carries it, the
	// switcher just wants where each window is.
	History []ShortcutAction `json:"history,omitempty"`
}

type launcherState struct {
	mu sync.Mutex

	phase      string
	loginErr   string
	loginStat  string
	profName   string
	profPic    string
	pubkey     string
	fetchErr   string
	fetching   bool
	installed  []Napp
	discovery  []Napp
	dev        []Napp
	devErr     string
	devLoading bool
	busy       map[string]bool

	// changed is closed and replaced on every phase change, so a waiter can
	// block until the launcher is out of PhaseLoading.
	changed chan struct{}
}

var ls = launcherState{phase: PhaseLoading, busy: make(map[string]bool)}

// The Napp model crosses the gomobile boundary by value, so per-napp "an
// update is out there" flags can't be shared mutable state on it: Snapshot()
// stamps them from this atomic set instead, keyed by napp id.
//
// The set is swapped whole rather than refilled in place, so a snapshot never
// catches a check halfway through replacing it.
var (
	updateSet      atomic.Pointer[xsync.MapOf[string, Napp]]
	updateChecking atomic.Bool
)

func init() { updateSet.Store(xsync.NewMapOf[string, Napp]()) }

// setUpdateAvailable replaces the "has an update" set and republishes the
// launcher state.
func setUpdateAvailable(apps map[string]Napp) {
	next := xsync.NewMapOf[string, Napp]()
	for id, n := range apps {
		next.Store(id, n)
	}
	updateSet.Store(next)
	notifyState()
}

// Snapshot is the current launcher state, safe to hold on to and read from a
// render loop.
func Snapshot() State {
	name, _ := Theme()

	ls.mu.Lock()
	s := State{
		Phase:          ls.phase,
		LoginErr:       ls.loginErr,
		LoginStatus:    ls.loginStat,
		ProfileName:    ls.profName,
		ProfilePicture: ls.profPic,
		Pubkey:         ls.pubkey,
		FetchErr:       ls.fetchErr,
		Fetching:       ls.fetching,
		Theme:          name,
		ReadAuth:       ReadAuth(),
		Installed:      append([]Napp(nil), ls.installed...),
		Discovery:      append([]Napp(nil), ls.discovery...),
		Dev:            append([]Napp(nil), ls.dev...),
		DevErr:         ls.devErr,
		DevLoading:     ls.devLoading,
		Busy:           make([]string, 0, len(ls.busy)),
	}
	for id := range ls.busy {
		s.Busy = append(s.Busy, id)
	}
	ls.mu.Unlock()

	s.Relays = Relays()
	s.Windows = OpenWindows()
	s.ManagedWindows = ManagedWindows()
	s.Bundles = bundles()
	s.UpdateCheckRunning = updateChecking.Load()
	s.Previews = previewNapps()
	for i := range s.Installed {
		if newVersion, ok := updateSet.Load().Load(s.Installed[i].ID); ok {
			s.Installed[i].UpdateAvailable = &newVersion
		}
	}
	for i := range s.Previews {
		if newVersion, ok := updateSet.Load().Load(s.Previews[i].ID); ok {
			s.Previews[i].UpdateAvailable = &newVersion
		}
		if s.Previews[i].AuthorName == "" {
			s.Previews[i].AuthorName = s.Previews[i].AuthorShortName()
		}
	}
	// napps with an update available float to the top of the installed
	// list, so the ones needing attention are seen first. Stable sort
	// keeps the most-opened order among themselves.
	sort.SliceStable(s.Installed, func(i, j int) bool {
		iUpd := s.Installed[i].UpdateAvailable != nil
		jUpd := s.Installed[j].UpdateAvailable != nil
		return iUpd && !jUpd
	})
	// author names resolve in the background and are stamped on every
	// snapshot, so the UIs get them for free (display and filtering).
	for i := range s.Installed {
		if s.Installed[i].AuthorName == "" {
			s.Installed[i].AuthorName = s.Installed[i].AuthorShortName()
		}
	}

	byID := make(map[string]Napp, len(s.Installed)+len(s.Previews))
	for _, n := range s.Previews {
		byID[n.ID] = n
	}
	for _, n := range s.Installed {
		byID[n.ID] = n
	}
	bundleByName := make(map[string]BundleInfo, len(s.Bundles))
	for _, b := range s.Bundles {
		bundleByName[b.Name] = b
	}
	s.Pinned = make([]Napp, 0)
	s.Home = make([]HomeItem, 0)
	for _, id := range pinnedIDs() {
		if name, ok := strings.CutPrefix(id, bundlePinPrefix); ok {
			if b, ok := bundleByName[name]; ok {
				s.Home = append(s.Home, HomeItem{Key: id, Bundle: &b})
			}
			continue
		}
		if n, ok := byID[id]; ok {
			s.Pinned = append(s.Pinned, n)
			s.Home = append(s.Home, HomeItem{Key: id, Napp: &n})
		}
	}

	for i := range s.Discovery {
		if s.Discovery[i].AuthorName == "" {
			s.Discovery[i].AuthorName = s.Discovery[i].AuthorShortName()
		}
	}
	for i := range s.Dev {
		if s.Dev[i].AuthorName == "" {
			s.Dev[i].AuthorName = s.Dev[i].AuthorShortName()
		}
	}
	return s
}

// notifyState tells the GUI to re-render.
func notifyState() {
	if host != nil {
		host.StateChanged()
	}
}

// setPhase moves the launcher to a phase and wakes whoever is waiting for it
// to change. ls.mu must be held.
func setPhaseLocked(phase string) {
	ls.phase = phase
	if ls.changed != nil {
		close(ls.changed)
	}
	ls.changed = make(chan struct{})
}

func setPhase(phase string) {
	ls.mu.Lock()
	setPhaseLocked(phase)
	ls.mu.Unlock()
	notifyState()
}

// Phase is the launcher's phase on its own, for a GUI that only needs that.
func Phase() string {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	return ls.phase
}

// waitStartupLogin blocks until the launcher's startup phase is over: the
// login Start resumed has answered, or there was none to resume. Work that
// opens napps right after a cold start waits for it, so those windows find a
// signer already in place (or an answer saying there is none) instead of
// racing the login. A launcher that is already up is never in the startup
// phase, so this returns at once.
func waitStartupLogin(ctx context.Context) {
	for {
		ls.mu.Lock()
		if ls.phase != PhaseLoading {
			ls.mu.Unlock()
			return
		}
		if ls.changed == nil {
			ls.changed = make(chan struct{})
		}
		changed := ls.changed
		ls.mu.Unlock()

		select {
		case <-changed:
		case <-ctx.Done():
			return
		}
	}
}

func setLoginErr(msg string) {
	ls.mu.Lock()
	ls.loginErr = msg
	ls.loginStat = ""
	setPhaseLocked(PhaseLogin)
	ls.mu.Unlock()
	notifyState()
}

// LoginFailed puts a login error on the login screen, for a platform whose
// part of a login failed.
func LoginFailed(msg string) { setLoginErr(msg) }

// setLoginStatus shows how a long login is going (and that the last error
// is no longer the news).
func setLoginStatus(msg string) {
	ls.mu.Lock()
	ls.loginStat = msg
	if msg != "" {
		ls.loginErr = ""
	}
	ls.mu.Unlock()
	notifyState()
}

func setProfile(pubkey, name, picture string) {
	ls.mu.Lock()
	ls.loginErr = ""
	ls.pubkey = pubkey
	ls.profName = name
	ls.profPic = picture
	setPhaseLocked(PhaseMain)
	ls.mu.Unlock()
	notifyState()
}

// SetFetchErr shows an error in the launcher (a GUI may also use it for its
// own failures).
func SetFetchErr(msg string) {
	ls.mu.Lock()
	ls.fetchErr = msg
	ls.mu.Unlock()
	notifyState()
}

func setFetching(fetching bool) {
	ls.mu.Lock()
	ls.fetching = fetching
	if fetching {
		ls.fetchErr = ""
		ls.discovery = nil
	}
	ls.mu.Unlock()
	notifyState()
}

func setDiscovery(list []Napp) {
	ls.mu.Lock()
	ls.discovery = list
	ls.sortDiscovery()
	ls.mu.Unlock()
	notifyState()
}

// sortDiscovery floats the napps the user hasn't installed to the top of the
// discovery list: that tab exists to show what's missing, and the ones already
// on disk are the ones they came here without. The ones on disk sink to the
// bottom, where their buttons offer uninstall and update instead of install.
// Within each group the order the relays delivered is kept.
//
// The installed set is read here rather than remembered, so a napp installed
// or uninstalled after discovery ran moves between the groups immediately.
func (l *launcherState) sortDiscovery() {
	installed := installedIDs()
	sort.SliceStable(l.discovery, func(i, j int) bool {
		_, iIn := installed[l.discovery[i].ID]
		_, jIn := installed[l.discovery[j].ID]
		return jIn && !iIn
	})
}

func setBusy(id string, busy bool) {
	ls.mu.Lock()
	if busy {
		ls.busy[id] = true
	} else {
		delete(ls.busy, id)
	}
	ls.mu.Unlock()
	notifyState()
}

// IsBusy says whether a napp is being installed or uninstalled right now.
func IsBusy(id string) bool {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	return ls.busy[id]
}

// DiscoveredNapp looks a napp up among what discovery last found, so a UI can
// act on an id alone.
func DiscoveredNapp(id string) (Napp, bool) {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	for _, n := range ls.discovery {
		if n.ID == id {
			return n, true
		}
	}
	return Napp{}, false
}
