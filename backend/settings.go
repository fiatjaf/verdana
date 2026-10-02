package backend

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"sync"
)

// Settings windows: one per napp, owned by the launcher rather than by the
// napp. The page in it (webview/napplet-settings.*) renders the napp's
// NAP-CONFIG schema as a form and lists what the user let it do; it talks
// to the backend over the same wire protocol a napp window does ({t:"rpc"}
// up, {t:"resp"} and {t:"eval"} down), through HandleSettingsMessage.

// SettingsSpec is what a platform needs to open a settings window.
type SettingsSpec struct {
	// Window identifies the window to the platform, the way
	// WindowSpec.Instance does a napp window's. It is never a napp instance.
	Window string
	NappID string
	Name   string
	// Section is the x-napplet-section to open on, or empty.
	Section string

	Theme     string
	ThemeVars string
}

type settingsWindow struct {
	id     string
	nappID string

	mu        sync.Mutex
	transport Transport
	queued    []WireMsg
	section   string
}

var (
	settingsMu   sync.Mutex
	settingsWins = map[string]*settingsWindow{} // by napp id
	settingsSeq  int
)

func (w *settingsWindow) send(m WireMsg) {
	w.mu.Lock()
	if w.transport == nil {
		w.queued = append(w.queued, m)
		w.mu.Unlock()
		return
	}
	t := w.transport
	w.mu.Unlock()
	t.Send(m)
}

func (w *settingsWindow) attach(t Transport) {
	w.mu.Lock()
	w.transport = t
	queued := w.queued
	w.queued = nil
	w.mu.Unlock()
	for _, m := range queued {
		t.Send(m)
	}
}

// settingsNapp finds the napp a settings window is for: installed, a dev
// napp, or one that only has a window open.
func settingsNapp(nappID string) (Napp, bool) {
	if n, ok := InstalledNapp(nappID); ok {
		return n, true
	}
	for _, n := range DevNapps() {
		if n.ID == nappID {
			return n, true
		}
	}
	for _, ci := range runningForNapp(nappID) {
		return ci.napp, true
	}
	return Napp{}, false
}

// OpenSettings opens (or brings up) a napp's settings window.
func OpenSettings(nappID string) error { return openSettings(nappID, "") }

// OpenLauncherSettings opens the settings window with only the launcher's
// own page in it (relays, Blossom servers).
func OpenLauncherSettings() error { return openSettings(launcherSettingsID, "") }

// launcherSettingsID stands for the launcher among the napp ids settings
// windows are kept by; no napp id is empty.
const launcherSettingsID = ""

// OpenSettingsFor opens the settings of the napp in a window: the gear in
// the window's chrome.
func OpenSettingsFor(instance string) error {
	ci := lookupInstance(instance)
	if ci == nil {
		return errors.New("no such window")
	}
	return openSettings(ci.napp.ID, "")
}

func openSettings(nappID, section string) error {
	name := "Verdana"
	if nappID != launcherSettingsID {
		napp, ok := settingsNapp(nappID)
		if !ok {
			return errors.New("unknown napp")
		}
		name = napp.Label()
	}

	settingsMu.Lock()
	if w, open := settingsWins[nappID]; open {
		settingsMu.Unlock()
		w.mu.Lock()
		t := w.transport
		w.mu.Unlock()
		if section != "" {
			w.send(WireMsg{T: "eval", Code: "window.__settings_section && window.__settings_section(" + jsString(section) + ")"})
		}
		if t != nil {
			t.Focus()
		}
		return nil
	}
	settingsSeq++
	w := &settingsWindow{id: "settings-" + strconv.Itoa(settingsSeq), nappID: nappID, section: section}
	settingsWins[nappID] = w
	settingsMu.Unlock()

	theme, vars := Theme()
	t, err := host.OpenSettings(SettingsSpec{
		Window:    w.id,
		NappID:    nappID,
		Name:      name,
		Section:   section,
		Theme:     theme,
		ThemeVars: vars,
	})
	if err != nil {
		settingsMu.Lock()
		if settingsWins[nappID] == w {
			delete(settingsWins, nappID)
		}
		settingsMu.Unlock()
		return err
	}
	w.attach(t)
	return nil
}

func lookupSettingsWindow(window string) *settingsWindow {
	settingsMu.Lock()
	defer settingsMu.Unlock()
	for _, w := range settingsWins {
		if w.id == window {
			return w
		}
	}
	return nil
}

// SettingsClosed is what a platform calls once a settings window is gone.
func SettingsClosed(window string) {
	settingsMu.Lock()
	defer settingsMu.Unlock()
	for id, w := range settingsWins {
		if w.id == window {
			delete(settingsWins, id)
			return
		}
	}
}

// IsSettingsWindow tells a platform routing messages by window id whether
// one belongs to a settings window rather than a napp.
func IsSettingsWindow(window string) bool { return lookupSettingsWindow(window) != nil }

// HandleSettingsWireMessage is HandleSettingsMessage for raw JSON.
func HandleSettingsWireMessage(window, raw string) {
	m, err := ParseWireMsg(raw)
	if err != nil {
		log.Warn().Str("window", window).Err(err).Msg("unreadable message from a settings window")
		return
	}
	HandleSettingsMessage(window, m)
}

// HandleSettingsMessage takes what a settings window's page sent up.
func HandleSettingsMessage(window string, m WireMsg) {
	w := lookupSettingsWindow(window)
	if w == nil {
		log.Warn().Str("window", window).Str("t", m.T).Msg("message for an unknown settings window")
		return
	}
	if m.T != "rpc" {
		return
	}
	go func() {
		result, err := settingsRPC(w, m.Method, m.Params)
		resp := WireMsg{T: "resp", ID: m.ID}
		if err != nil {
			resp.Error = err.Error()
		} else if raw, mErr := json.Marshal(result); mErr != nil {
			resp.Error = mErr.Error()
		} else {
			resp.Result = raw
		}
		w.send(resp)
	}()
}

// settingsLoad is everything the page renders.
type settingsLoad struct {
	// Napp is false for the launcher's own window: no napp page then.
	Napp   bool            `json:"napp"`
	Name   string          `json:"name"`
	Schema json.RawMessage `json:"schema"`
	Values map[string]any  `json:"values"`
	// Set are the settings the user set; Secrets, the secrets among them.
	Set     []string `json:"set"`
	Secrets []string `json:"secrets"`
	Section string   `json:"section,omitempty"`
	// Permissions are what the user decided for this napp.
	Permissions []PermissionRule `json:"permissions"`

	// Launcher is the launcher's own settings, on every window's Verdana
	// page.
	Launcher launcherSettings `json:"launcher"`
}

type launcherSettings struct {
	Relays         []string `json:"relays"`
	BlossomServers []string `json:"blossomServers"`
}

func settingsRPC(w *settingsWindow, method, params string) (any, error) {
	switch method {
	case "settings.load":
		return settingsLoadFor(w), nil
	case "settings.save":
		var req struct {
			Values map[string]any `json:"values"`
		}
		if err := json.Unmarshal([]byte(params), &req); err != nil {
			return nil, errors.New("invalid request")
		}
		if err := configSave(w.nappID, req.Values); err != nil {
			return nil, err
		}
		pushConfigValues(w.nappID)
		return settingsLoadFor(w), nil
	case "settings.reset":
		if err := configReset(w.nappID); err != nil {
			return nil, err
		}
		pushConfigValues(w.nappID)
		return settingsLoadFor(w), nil
	case "settings.saveLauncher":
		var req launcherSettings
		if err := json.Unmarshal([]byte(params), &req); err != nil {
			return nil, errors.New("invalid request")
		}
		if req.Relays != nil {
			before := Relays()
			SetRelays(req.Relays)
			if sys != nil && !slices.Equal(before, Relays()) {
				// what is discoverable depends on them
				go Discover()
			}
		}
		if req.BlossomServers != nil {
			SetBlossomServers(req.BlossomServers)
		}
		return settingsLoadFor(w), nil
	case "settings.forgetPermission":
		var req struct {
			Permission Permission `json:"permission"`
		}
		if err := json.Unmarshal([]byte(params), &req); err != nil {
			return nil, errors.New("invalid request")
		}
		if w.nappID == launcherSettingsID {
			return nil, errors.New("no napp to forget for")
		}
		ForgetPermission(w.nappID, req.Permission)
		return settingsLoadFor(w), nil
	}
	return nil, fmt.Errorf("unsupported method: %s", method)
}

func settingsLoadFor(w *settingsWindow) settingsLoad {
	out := settingsLoad{
		Set: []string{}, Secrets: []string{}, Permissions: []PermissionRule{},
		Launcher: launcherSettings{Relays: Relays(), BlossomServers: BlossomServers()},
	}
	if w.nappID == launcherSettingsID {
		out.Name = "Verdana"
		return out
	}
	out.Napp = true
	if napp, ok := settingsNapp(w.nappID); ok {
		out.Name = napp.Label()
	}
	w.mu.Lock()
	out.Section, w.section = w.section, ""
	w.mu.Unlock()
	if s, stored := configSnapshot(w.nappID); s != nil {
		out.Schema = s.Raw
		// what the napplet would be delivered, minus the secrets, which
		// the page only learns are set
		out.Values = withoutConfigSecrets(s.Root, resolveConfigValues(s, stored))
		out.Set = configStoredPaths(s.Root, "", stored, false)
		out.Secrets = configStoredPaths(s.Root, "", stored, true)
	}
	for _, r := range PermissionRules() {
		if r.Napp == w.nappID {
			out.Permissions = append(out.Permissions, r)
		}
	}
	return out
}

// settingsChanged tells a napp's open settings window to load again: its
// schema changed under it.
func settingsChanged(nappID string) {
	settingsMu.Lock()
	w := settingsWins[nappID]
	settingsMu.Unlock()
	if w != nil {
		w.send(WireMsg{T: "eval", Code: "window.__settings_reload && window.__settings_reload()"})
	}
}

// broadcastSettingsTheme gives every settings window the launcher's theme.
func broadcastSettingsTheme(name, vars string) {
	settingsMu.Lock()
	wins := make([]*settingsWindow, 0, len(settingsWins))
	for _, w := range settingsWins {
		wins = append(wins, w)
	}
	settingsMu.Unlock()
	for _, w := range wins {
		w.send(WireMsg{T: "theme", Method: name, Params: vars})
	}
}

// CloseAllSettings closes every settings window, for a launcher shutting down.
func CloseAllSettings() {
	settingsMu.Lock()
	wins := make([]*settingsWindow, 0, len(settingsWins))
	for _, w := range settingsWins {
		wins = append(wins, w)
	}
	settingsMu.Unlock()
	for _, w := range wins {
		w.mu.Lock()
		t := w.transport
		w.mu.Unlock()
		if t != nil {
			t.Close()
		}
	}
}
