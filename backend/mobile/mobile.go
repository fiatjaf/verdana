// Package mobile is the gomobile face of the backend: the same launcher, with
// an API a JVM can hold on to.
//
// gomobile only carries strings, numbers, bools, []byte, errors and the types
// declared right here, so everything structured crosses as JSON — the state
// the UI renders, the prompt it shows, the wire messages a napp's WebView
// exchanges with the backend. Kotlin implements UI; Go calls it back.
package mobile

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"verdana/backend"
	"verdana/backend/webview"
)

// UI is the Android side of backend.Host. Every method may be called from any
// thread, so implementations that touch views must post to the main looper.
type UI interface {
	// OpenWindow asks for a napp to be put on screen. spec is a JSON object:
	// { instance, nappId, name, description, dir, requires, theme, themeVars }.
	// The dir is where the napp's files are: serve them to the WebView, inject
	// BridgeJS(), and send everything the page posts up to HandleMessage.
	OpenWindow(instance string, specJSON string) error

	// SendToWindow delivers one wire message to a napp's shell. It is a JSON
	// object with a "t": "resp" answers an rpc, "eval" runs code, "action"
	// dispatches an action, "theme" changes the theme, "close" closes it.
	SendToWindow(instance string, msgJSON string)

	// CloseWindow gets rid of a napp's window (its tab). WindowClosed must be
	// called once it is really gone.
	CloseWindow(instance string)

	// StateChanged says State() changed; PromptsChanged, that CurrentPrompt()
	// did.
	StateChanged()
	PromptsChanged()

	// CopyText, SaveFile and OpenLink are the platform services behind the
	// napp rpcs of the same names. All three are already user-approved.
	// SaveFile returns the name the file ended up under.
	CopyText(text string) error
	SaveFile(name string, data []byte) (string, error)
	SaveFileTarget() string
	OpenLink(url string) error
}

// ─── host adapter ────────────────────────────────────────────────

type mobileHost struct{ ui UI }

func (h mobileHost) OpenWindow(spec backend.WindowSpec) (backend.Transport, error) {
	payload, err := json.Marshal(map[string]any{
		"instance":    spec.Instance,
		"nappId":      spec.NappID,
		"name":        spec.Name,
		"description": spec.Description,
		"dir":         spec.Dir,
		"url":         spec.URL,
		"requires":    spec.Requires,
		"theme":       spec.Theme,
		"themeVars":   spec.ThemeVars,
		"width":       spec.Width,
		"height":      spec.Height,
	})
	if err != nil {
		return nil, err
	}
	if err := h.ui.OpenWindow(spec.Instance, string(payload)); err != nil {
		return nil, err
	}
	return mobileTransport{ui: h.ui, instance: spec.Instance}, nil
}

func (h mobileHost) StateChanged()                               { h.ui.StateChanged() }
func (h mobileHost) PromptsChanged()                             { h.ui.PromptsChanged() }
func (h mobileHost) CopyText(text string) error                  { return h.ui.CopyText(text) }
func (h mobileHost) SaveFileTarget() string                      { return h.ui.SaveFileTarget() }
func (h mobileHost) OpenLink(url string) error                   { return h.ui.OpenLink(url) }
func (h mobileHost) SaveFile(n string, d []byte) (string, error) { return h.ui.SaveFile(n, d) }

// shortcut files are a desktop concept: on Android the launcher either isn't
// running (no shortcut files) or has no OS shortcut system to talk to.
func (h mobileHost) CreateShortcutFile(string, string) (string, error) {
	return "", errors.New("shortcut files are a desktop concept")
}
func (h mobileHost) DeleteShortcutFile(string) error           { return nil }
func (h mobileHost) ListShortcutFiles() []backend.ShortcutFile { return nil }

type mobileTransport struct {
	ui       UI
	instance string
}

func (t mobileTransport) Send(msg backend.WireMsg) { t.ui.SendToWindow(t.instance, msg.JSON()) }
func (t mobileTransport) Close()                   { t.ui.CloseWindow(t.instance) }

// ─── lifecycle ───────────────────────────────────────────────────

var closeStores func()

// Start brings the backend up. dataDir should be the app's private files
// directory: the eventstore, the installed napps and state.json go there.
func Start(dataDir string, ui UI) error {
	stop, err := backend.Start(backend.Options{DataDir: dataDir, Host: mobileHost{ui: ui}})
	if err != nil {
		return err
	}
	closeStores = stop
	return nil
}

// Stop closes the napp windows and the stores. Android may kill the process
// without ever calling this, which is fine — the stores are crash-safe.
func Stop() {
	backend.CloseAllWindows()
	if closeStores != nil {
		closeStores()
		closeStores = nil
	}
}

// BridgeJS is the script that has to run before a napp's page does, on every
// navigation: it installs window.nostr, window.nostrdb and window.napp.
func BridgeJS() string { return webview.JS() }

// ─── what the UI renders ─────────────────────────────────────────

// State is the launcher as JSON: phase, login error, profile, relays, the
// installed and discovered napps, which ones are busy, and the open windows.
func State() string {
	data, err := json.Marshal(backend.Snapshot())
	if err != nil {
		return "{}"
	}
	return string(data)
}

// CurrentPrompt is the question the user has to answer as JSON, or "" when
// there is none.
func CurrentPrompt() string {
	p := backend.CurrentPrompt()
	if p == nil {
		return ""
	}
	data, err := json.Marshal(p)
	if err != nil {
		return ""
	}
	return string(data)
}

// AnswerPrompt answers the prompt with that id. index picks an option for a
// picker prompt and is ignored otherwise. scope is how long the answer holds:
// "once" (this prompt only), "session" (until the launcher quits) or "always"
// (written to state, until the user takes it back). Anything else is taken as
// "once", and the scopes are ignored for prompts that can't be remembered.
func AnswerPrompt(id int, ok bool, index int, scope string) {
	backend.AnswerPrompt(id, backend.Answer{OK: ok, Index: index, Scope: backend.Scope(scope)})
}

// ─── launcher actions ────────────────────────────────────────────

// Login resolves an nsec or bunker:// url. It returns immediately; watch the
// state's phase and loginErr for the outcome.
func Login(input string) { go backend.Login(input) }

// Logout forgets the key and closes every napp.
func Logout() { backend.Logout() }

// Fetch looks for napps on the discovery relays.
func Fetch() { go backend.Discover() }

// SetRelays replaces the discovery relay list, one relay per line.
func SetRelays(text string) {
	lines := strings.Split(text, "\n")
	backend.SetRelays(lines)
}

// Install downloads and installs a napp discovery found, by id. For an
// already-installed napp it re-downloads it over: that is how an update
// button on a discovery card applies the newer version.
func Install(id string) {
	if !backend.InstallFromDiscovery(id) {
		backend.SetFetchErr("nothing known about napp " + id)
	}
}

// Update applies newer event already found by update check.
func Update(id string) { go backend.Update(id) }

// CheckForUpdates looks for newer versions of every installed napp on the
// discovery relays and each author's outbox relays, and flags the napps it
// found updates for (watch the state's updateCheckRunning/updateAvailable).
func CheckForUpdates() { go backend.CheckForUpdates() }

// Uninstall removes an installed napp.
func Uninstall(id string) { go backend.Uninstall(id) }

// Launch opens an installed napp, or surfaces the window it already has.
func Launch(id string) { backend.LaunchByID(id) }

// SetTheme tells the backend which theme the UI is drawing with, so napps can
// follow it. varsJSON is a flat object of CSS custom properties without the
// leading dashes: {"surface":"#fff","text":"#000",…}.
func SetTheme(name string, varsJSON string) { backend.SetTheme(name, varsJSON) }

// NappIcon is the bytes of a napp's icon, from disk when it is installed and
// from its author's blossom servers otherwise.
func NappIcon(id string) ([]byte, error) {
	n, ok := backend.InstalledNapp(id)
	if !ok {
		n, ok = backend.DiscoveredNapp(id)
	}
	if !ok {
		return nil, errNoNapp(id)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return n.IconBlob(ctx)
}

type errNoNapp string

func (e errNoNapp) Error() string { return "no napp " + string(e) }

// ─── napp windows ────────────────────────────────────────────────

// HandleMessage takes a wire message a napp's page posted up (an rpc).
func HandleMessage(instance string, msgJSON string) {
	backend.HandleWireMessage(instance, msgJSON)
}

// WindowClosed says a napp's window is gone, so whatever was waiting on it
// stops waiting.
func WindowClosed(instance string) { backend.WindowClosed(instance) }

// CloseWindow asks a napp to close (the user swiped its tab away).
func CloseWindow(instance string) { backend.CloseWindow(instance) }

// RunAction fires an action from outside any napp — a shared link, a
// shortcut — and routes it like napp.action() would. Returns the handler's
// result as JSON.
func RunAction(name string, payloadJSON string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	result, err := backend.RunAction(ctx, name, payloadJSON)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
