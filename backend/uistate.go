package backend

import "sync"

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

	// Relays are the discovery relays.
	Relays []string `json:"relays"`

	// Installed napps, and the ones discovery found, both sorted by name.
	Installed []Napp `json:"installed"`
	Discovery []Napp `json:"discovery"`

	// Busy holds the ids of napps being installed or uninstalled.
	Busy []string `json:"busy"`

	// Windows are the napp instances currently open.
	Windows []WindowInfo `json:"windows"`
}

// WindowInfo is one open napp instance, for a window list or tab switcher.
type WindowInfo struct {
	Instance string `json:"instance"`
	NappID   string `json:"nappId"`
	Name     string `json:"name"`

	// Action is what the window is currently showing, when the napp told us
	// (a dispatched action, or one it pushed itself).
	Action string `json:"action"`
}

type launcherState struct {
	mu sync.Mutex

	phase     string
	loginErr  string
	profName  string
	profPic   string
	pubkey    string
	fetchErr  string
	fetching  bool
	installed []Napp
	discovery []Napp
	busy      map[string]bool
}

var ls = launcherState{phase: PhaseLoading, busy: make(map[string]bool)}

// Snapshot is the current launcher state, safe to hold on to and read from a
// render loop.
func Snapshot() State {
	name, _ := Theme()

	ls.mu.Lock()
	s := State{
		Phase:          ls.phase,
		LoginErr:       ls.loginErr,
		ProfileName:    ls.profName,
		ProfilePicture: ls.profPic,
		Pubkey:         ls.pubkey,
		FetchErr:       ls.fetchErr,
		Fetching:       ls.fetching,
		Theme:          name,
		Installed:      append([]Napp(nil), ls.installed...),
		Discovery:      append([]Napp(nil), ls.discovery...),
		Busy:           make([]string, 0, len(ls.busy)),
	}
	for id := range ls.busy {
		s.Busy = append(s.Busy, id)
	}
	ls.mu.Unlock()

	s.Relays = Relays()
	s.Windows = OpenWindows()
	return s
}

// notifyState tells the GUI to re-render.
func notifyState() {
	if host != nil {
		host.StateChanged()
	}
}

func setPhase(phase string) {
	ls.mu.Lock()
	ls.phase = phase
	ls.mu.Unlock()
	notifyState()
}

// Phase is the launcher's phase on its own, for a GUI that only needs that.
func Phase() string {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	return ls.phase
}

func setLoginErr(msg string) {
	ls.mu.Lock()
	ls.loginErr = msg
	ls.phase = PhaseLogin
	ls.mu.Unlock()
	notifyState()
}

func setProfile(pubkey, name, picture string) {
	ls.mu.Lock()
	ls.loginErr = ""
	ls.pubkey = pubkey
	ls.profName = name
	ls.profPic = picture
	ls.phase = PhaseMain
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
	ls.mu.Unlock()
	notifyState()
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
