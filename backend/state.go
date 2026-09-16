package backend

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"fiatjaf.com/nostr"
)

// DefaultRelays is where the launcher looks for napps when the user hasn't
// said otherwise.
var DefaultRelays = []string{
	"relay.nostrapps.com",
	"relay.nostrapps.com/public",
}

// AppState is everything the launcher remembers between runs.
type AppState struct {
	ClientKey      nostr.SecretKey `json:"client_key"`
	Login          string          `json:"login"`
	Relays         []string        `json:"relays"`
	InstalledNapps map[string]Napp `json:"installed_napps"`

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
		state.Relays = append([]string(nil), DefaultRelays...)
	}
	if state.InstalledNapps == nil {
		state.InstalledNapps = make(map[string]Napp)
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
	stateMu.Lock()
	defer stateMu.Unlock()
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
	stateMu.Lock()
	defer stateMu.Unlock()
	return strings.TrimSpace(state.Login)
}

// ─── installed napps ─────────────────────────────────────────────

func installedNapps() []Napp {
	stateMu.Lock()
	list := make([]Napp, 0, len(state.InstalledNapps))
	for _, n := range state.InstalledNapps {
		list = append(list, n)
	}
	stateMu.Unlock()
	return list
}

// IsInstalled says whether a napp is on disk.
func IsInstalled(id string) bool {
	stateMu.Lock()
	defer stateMu.Unlock()
	_, ok := state.InstalledNapps[id]
	return ok
}
