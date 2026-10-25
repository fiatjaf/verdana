package backend

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
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

	// PreviewNapps are napps the user opened without installing them (see
	// preview.go). They are on disk and behave like installed ones, except
	// they stay off the installed list and are only offered as handlers of
	// an action while they have a window open.
	PreviewNapps map[string]Napp `json:"preview_napps,omitempty"`

	// LaunchCounts records how many times the user started a napp from
	// the launcher's Open buttons, so the installed list can be shown
	// most-opened first. Launches that happen because another napp
	// dispatched an action, or because a bundle opened it, don't
	// count: the user didn't choose that window from the launcher.
	LaunchCounts map[string]int `json:"launch_counts"`

	// LastLaunched is the pre-counts ordering, kept only to migrate old
	// state files: anything launched before counts existed starts at 1.
	LastLaunched map[string]time.Time `json:"last_launched,omitempty"`

	// Pinned are the napp ids the user put on the launcher's home tab, in
	// the order they were pinned (the order the home grid shows them in).
	// Pinned bundles are in here too, as BundlePinKey(name).
	Pinned []string `json:"pinned"`

	// Bundles are the bundles the user made on the Windows screen (see
	// bundles.go). Whether one has an OS shortcut is read from the
	// shortcut files, not kept here.
	Bundles []Bundle `json:"bundles"`

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

	// Configurations are the napp configurations (kind:35133, see
	// config.go) the user applied, oldest first, as they were applied.
	// What they set lives in the fields below, where the settings edit it.
	Configurations []NappConfiguration `json:"configurations,omitempty"`

	// DefaultHandlers are the actions' default handlers, by action. Only a
	// napp configuration adds one; the settings change or remove them.
	DefaultHandlers map[string]DefaultHandler `json:"default_handlers,omitempty"`

	// SignPolicy and PublishPolicy are what every napp asking to sign or to
	// publish gets when the user has not answered for that napp: empty (or
	// "ask") asks. Set by a napp configuration or in the settings.
	SignPolicy    Decision `json:"sign_policy,omitempty"`
	PublishPolicy Decision `json:"publish_policy,omitempty"`

	// LinkPolicy is which links napps open in the browser without asking,
	// unless the user said no to that napp: LinksNone (empty), LinksNonImage
	// or LinksAll (see config.go). Set by a napp configuration or in the
	// settings.
	LinkPolicy LinkPolicy `json:"link_policy,omitempty"`

	// Theme is "light" or "dark": what the launcher draws with and what
	// every napp window is told to track.
	Theme string `json:"theme"`

	// NoReadAuth stops the launcher from authenticating (NIP-42) to relays
	// that ask for it when reading. Publishing authenticates regardless.
	NoReadAuth bool `json:"no_read_auth,omitempty"`
}

var (
	state     AppState
	statePath string
	stateMu   sync.Mutex
)

func loadState() {
	// whether a bundle has a shortcut is read from the files, not from here
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
			"relay.nostrapps.com/network",
		}
	}
	if state.InstalledNapps == nil {
		state.InstalledNapps = make(map[string]Napp)
	}
	if state.PreviewNapps == nil {
		state.PreviewNapps = make(map[string]Napp)
	}
	if state.LaunchCounts == nil {
		state.LaunchCounts = make(map[string]int)
	}
	// migrate the pre-counts ordering: anything the user had launched
	// before counts existed starts at 1, never-opened napps stay at 0.
	if len(state.LaunchCounts) == 0 && len(state.LastLaunched) > 0 {
		for id := range state.LastLaunched {
			state.LaunchCounts[id] = 1
		}
	}
	state.LastLaunched = nil
	importShortcutFiles()
	// pins only ever name napps on disk and existing bundles, so a
	// position on the home tab is a position in this list (see MovePin)
	state.Pinned = slices.DeleteFunc(state.Pinned, func(id string) bool {
		if name, ok := strings.CutPrefix(id, bundlePinPrefix); ok {
			return bundleIndexLocked(name) < 0
		}
		_, ok := localNappLocked(id)
		return !ok
	})
	if state.Rules == nil {
		state.Rules = make(map[string]Rule)
	}
	if state.ActionUsage == nil {
		state.ActionUsage = make(map[string]int)
	}
	// configurations applied before their settings were kept apart from
	// them set those settings over again, in the order they were applied
	if state.DefaultHandlers == nil {
		state.DefaultHandlers = make(map[string]DefaultHandler)
		for _, c := range state.Configurations {
			takeConfigSettingsLocked(c)
		}
	}
	if state.Theme != "light" && state.Theme != "dark" {
		state.Theme = "light"
	}
	themeName = state.Theme
	saveState()
	for id := range state.InstalledNapps {
		migrateNappDir(id)
	}
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
	counts := make(map[string]int, len(state.LaunchCounts))
	for id, c := range state.LaunchCounts {
		counts[id] = c
	}
	stateMu.Unlock()

	// most opened first, then by name: never-opened napps sink to the
	// bottom (their zero count sorts after everything opened), keeping
	// the alphabetical order readable among themselves.
	sort.Slice(list, func(i, j int) bool {
		ci, cj := counts[list[i].ID], counts[list[j].ID]
		if ci != cj {
			return ci > cj
		}
		return list[i].Name < list[j].Name
	})
	return list
}

// markLaunched records one user-initiated open of a napp. Only the
// launcher's own Open buttons get here: action-driven window opens (a napp
// dispatching into another napp) and bundle runs never do,
// because the user didn't pick that napp from the launcher.
func markLaunched(id string) {
	stateMu.Lock()
	if _, ok := localNappLocked(id); ok {
		if state.LaunchCounts == nil {
			state.LaunchCounts = make(map[string]int)
		}
		state.LaunchCounts[id]++
		saveState()
	}
	stateMu.Unlock()
}

// ─── pinned napps ────────────────────────────────────────────────

// Pin puts an installed (or previewed) napp on the home tab, after the ones
// pinned before.
func Pin(id string) {
	stateMu.Lock()
	if _, ok := localNappLocked(id); !ok || slices.Contains(state.Pinned, id) {
		stateMu.Unlock()
		return
	}
	state.Pinned = append(state.Pinned, id)
	saveState()
	stateMu.Unlock()
	notifyState()
}

// Unpin takes a napp (or a bundle, by its BundlePinKey) off the home tab.
func Unpin(id string) {
	stateMu.Lock()
	if !slices.Contains(state.Pinned, id) {
		stateMu.Unlock()
		return
	}
	state.Pinned = slices.DeleteFunc(state.Pinned, func(p string) bool { return p == id })
	saveState()
	stateMu.Unlock()
	notifyState()
}

// MovePin puts a pinned napp (or bundle, by its BundlePinKey) at position to
// of the home tab, shifting the ones in between (a drag-and-drop reorder). to
// is clamped to the list.
func MovePin(id string, to int) {
	stateMu.Lock()
	from := slices.Index(state.Pinned, id)
	if from < 0 {
		stateMu.Unlock()
		return
	}
	to = max(0, min(to, len(state.Pinned)-1))
	if from == to {
		stateMu.Unlock()
		return
	}
	state.Pinned = slices.Insert(slices.Delete(state.Pinned, from, from+1), to, id)
	saveState()
	stateMu.Unlock()
	notifyState()
}

// IsPinned says whether a napp is on the home tab.
func IsPinned(id string) bool {
	stateMu.Lock()
	defer stateMu.Unlock()
	return slices.Contains(state.Pinned, id)
}

func pinnedIDs() []string {
	stateMu.Lock()
	defer stateMu.Unlock()
	return append([]string(nil), state.Pinned...)
}

// IsInstalled says whether a napp is installed (a previewed one isn't, see
// IsPreview).
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
