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

	// BlossomServers are the servers napp and napplet files are fetched
	// from before any the napp or its author names. Nil (never set) means
	// defaultBlossomServers; an empty list means none of our own.
	BlossomServers []string `json:"blossom_servers"`

	// LastLaunched records when the user last started a napp from the
	// launcher's installed list, so it can be shown most-recently-used first.
	// Launches that happen because another napp dispatched an action don't
	// count: the user didn't choose that window.
	LastLaunched map[string]time.Time `json:"last_launched"`

	// Rules are the answers the user gave to permission prompts that were
	// meant to stick ("always allow", "always deny"), keyed by RuleKey
	// (see permissions.go). The "this session" ones are not here: they live
	// in memory and go when the launcher quits.
	Rules map[string]Rule `json:"rules"`

	// ActionUsage counts how often each napp ended up handling each action,
	// keyed by usageKey (see usage.go): both "from this napp, this action
	// went there" and "this action went there". Nothing is dispatched from
	// these — they only order the options the user gets to choose from. The
	// "this session" ones are not here either.
	ActionUsage map[string]int `json:"action_usage"`

	// Theme is "light" or "dark": what the launcher draws with and what
	// every napp window is told to track.
	Theme string `json:"theme"`
}

var (
	state     AppState
	statePath string
	stateMu   sync.Mutex
)

func loadState() {
	// the shortcut list is read from the files, not from here
	reloadShortcuts()
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
	if state.Rules == nil {
		state.Rules = make(map[string]Rule)
	}
	if state.ActionUsage == nil {
		state.ActionUsage = make(map[string]int)
	}
	if state.Theme != "light" && state.Theme != "dark" {
		state.Theme = "light"
	}
	themeMu.Lock()
	themeName = state.Theme
	themeMu.Unlock()
	saveState()
	log.Info().Int("napps", len(state.InstalledNapps)).Msg("state loaded")
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

// installedIDs snapshots the installed ids into a set, for callers that test
// membership once per napp.
func installedIDs() map[string]struct{} {
	stateMu.Lock()
	defer stateMu.Unlock()
	ids := make(map[string]struct{}, len(state.InstalledNapps))
	for id := range state.InstalledNapps {
		ids[id] = struct{}{}
	}
	return ids
}
