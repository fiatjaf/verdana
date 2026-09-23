package backend

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"fiatjaf.com/nostr"
)

// AppState is everything the launcher remembers between runs.
type AppState struct {
	ClientKey      nostr.SecretKey `json:"client_key"`
	Login          string          `json:"login"`
	Relays         []string        `json:"relays"`
	InstalledNapps map[string]Napp `json:"installed_napps"`

	// LastLaunched records when the user last started a napp from the
	// launcher's installed list, so it can be shown most-recently-used first.
	// Launches that happen because another napp dispatched an action don't
	// count: the user didn't choose that window.
	LastLaunched map[string]time.Time `json:"last_launched"`

	// Theme is "light" or "dark": what the launcher draws with and what
	// every napp window is told to track.
	Theme   string                 `json:"theme"`
	Windows map[string]SavedWindow `json:"windows"`
}

type SavedAction struct {
	Name    string          `json:"name"`
	Payload json.RawMessage `json:"payload"`
}
type SavedWindow struct {
	Instance string        `json:"instance"`
	NappID   string        `json:"napp_id"`
	Pinned   bool          `json:"pinned"`
	Closed   bool          `json:"closed"`
	Actions  []SavedAction `json:"actions"`
}

var (
	state     AppState
	statePath string
	stateMu   sync.Mutex
)

func loadState() {
	statePath = filepath.Join(dataDir, "state.json")
	data, err := os.ReadFile(statePath)
	if err == nil {
		json.Unmarshal(data, &state)
	} else {
		log.Debug().Err(err).Msg("no existing state file, using defaults")
	}
	if state.ClientKey == (nostr.SecretKey{}) {
		state.ClientKey = nostr.Generate()
		log.Debug().Msg("generated new client key")
	}
	if len(state.Relays) == 0 {
		state.Relays = []string{
			"relay.nostrapps.com",
			"relay.nostrapps.com/public",
		}
	}
	if state.InstalledNapps == nil {
		state.InstalledNapps = make(map[string]Napp)
	}
	if state.LastLaunched == nil {
		state.LastLaunched = make(map[string]time.Time)
	}
	if state.Theme != "light" && state.Theme != "dark" {
		state.Theme = "light"
	}
	if state.Windows == nil {
		state.Windows = make(map[string]SavedWindow)
	}
	themeName = state.Theme
	saveState()
	log.Info().Int("napps", len(state.InstalledNapps)).Msg("state loaded")
}

func savedWindows() []SavedWindow {
	stateMu.Lock()
	defer stateMu.Unlock()
	out := make([]SavedWindow, 0, len(state.Windows))
	for _, w := range state.Windows {
		w.Actions = append([]SavedAction(nil), w.Actions...)
		out = append(out, w)
	}
	return out
}

func saveWindow(w SavedWindow) {
	stateMu.Lock()
	state.Windows[w.Instance] = w
	saveState()
	stateMu.Unlock()
}

// saveState must be called with stateMu held.
func saveState() {
	data, err := json.MarshalIndent(&state, "", "  ")
	if err != nil {
		log.Error().Err(err).Msg("failed to marshal state")
		return
	}
	if err := os.WriteFile(statePath, data, 0600); err != nil {
		log.Error().Err(err).Msg("failed to write state file")
	}
}

// ─── relays ──────────────────────────────────────────────────────

// Relays are the relays napps are discovered on.
func Relays() []string {
	return append([]string(nil), state.Relays...)
}

// SetRelays stores the discovery relay list.
func SetRelays(relays []string) {
	cleaned := make([]string, 0, len(relays))
	for _, r := range relays {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		if !strings.Contains(r, "://") {
			r = "wss://" + r
		}
		cleaned = append(cleaned, r)
	}

	stateMu.Lock()
	state.Relays = cleaned
	saveState()
	stateMu.Unlock()
	notifyState()
}

// ─── login ───────────────────────────────────────────────────────

// StoredLogin is the nsec/bunker input the user logged in with last time.
func StoredLogin() string {
	return strings.TrimSpace(state.Login)
}

// ─── installed napps ─────────────────────────────────────────────

func installedNapps() []Napp {
	stateMu.Lock()

	list := make([]Napp, 0, len(state.InstalledNapps))
	for _, n := range state.InstalledNapps {
		list = append(list, n)
	}
	last := make(map[string]time.Time, len(state.LastLaunched))
	for id, t := range state.LastLaunched {
		last[id] = t
	}
	stateMu.Unlock()

	// most recently started first, then by name: never-started napps sink
	// to the bottom (their zero time sorts before everything), keeping the
	// alphabetical order readable among themselves.
	sort.Slice(list, func(i, j int) bool {
		ti, tj := last[list[i].ID], last[list[j].ID]
		if !ti.Equal(tj) {
			return ti.After(tj)
		}
		return list[i].Name < list[j].Name
	})
	return list
}

// markLaunched records a user-initiated launch of a napp. Only the launcher's
// own Open buttons get here: action-driven window opens (a napp dispatching
// into another napp) never do, because the user didn't pick that napp.
func markLaunched(id string) {
	stateMu.Lock()
	if _, ok := state.InstalledNapps[id]; ok {
		state.LastLaunched[id] = time.Now()
		saveState()
	}
	stateMu.Unlock()
}

// IsInstalled says whether a napp is on disk.
func IsInstalled(id string) bool {
	stateMu.Lock()
	defer stateMu.Unlock()
	_, ok := state.InstalledNapps[id]
	return ok
}
