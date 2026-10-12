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
	"path/filepath"
	"strings"
	"time"
	"verdana/backend"
	"verdana/backend/wasmhost"
	"verdana/backend/webview"
)

// UI is the Android side of backend.Host. Every method may be called from any
// thread, so implementations that touch views must post to the main looper.
type UI interface {
	// OpenWindow asks for a napp to be put on screen. spec is a JSON object:
	// { instance, nappId, name, description, mode, dir, requires, theme,
	// themeVars, … }. For a web napp (mode "web") the dir is where the napp's
	// files are: serve them to the WebView, inject BridgeJS(), and send
	// everything the page posts up to HandleMessage. A wasm napp (mode
	// "wasm") is already running in Go by the time this is called: its window
	// paints what WasmFrame returns and hands its input to WasmInput.
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

	// AmberRequest hands a NIP-55 operation to the signer app the user has
	// on the phone (Amber and co): show the request in the foreground and
	// deliver the answer back to AnswerAmber carrying the same id. False
	// when no signer could be launched at all.
	AmberRequest(id, op, payload, pubkey, counterpart, pkg string) bool

	// WasmInvalidate says a wasm napp's window has something new to show
	// (an answer arrived, the module finished loading): call WasmFrame
	// again soon. May come from any thread, including from inside WasmFrame.
	WasmInvalidate(instance string)
}

// ─── host adapter ────────────────────────────────────────────────

type mobileHost struct{ ui UI }

func (h mobileHost) OpenWindow(spec backend.WindowSpec) (backend.Transport, error) {
	payload, err := json.Marshal(map[string]any{
		"instance":    spec.Instance,
		"nappId":      spec.NappID,
		"name":        spec.Name,
		"description": spec.Description,
		"mode":        spec.Mode,
		"dir":         spec.Dir,
		"url":         spec.URL,
		"requires":    spec.Requires,
		"theme":       spec.Theme,
		"themeVars":   spec.ThemeVars,
		"width":       spec.Width,
		"height":      spec.Height,
		"storage":     spec.StorageJSON,
	})
	if err != nil {
		return nil, err
	}

	// A wasm napp runs in Go, so it is started before its window exists
	// and its transport is the module's bridge, not the window.
	if spec.Mode == backend.ModeWasm {
		w := openWasm(h.ui, spec)
		if err := h.ui.OpenWindow(spec.Instance, string(payload)); err != nil {
			w.close()
			return nil, err
		}
		return wasmTransport{w: w}, nil
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
func (h mobileHost) AmberRequest(id, op, payload, pubkey, counterpart, pkg string) bool {
	return h.ui.AmberRequest(id, op, payload, pubkey, counterpart, pkg)
}

// AnswerAmber delivers one NIP-55 signer app answer back to whoever on the
// backend is waiting for the request with that id. Called by the UI when
// the signer activity came back.
func AnswerAmber(id string, answer string, ok bool) {
	backend.AnswerAmber(id, answer, ok)
}

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
	// a napp window is a task of its own, which Android keeps after
	// killing the process: its record has to outlive the process too
	stop, err := backend.Start(backend.Options{DataDir: dataDir, Host: mobileHost{ui: ui}, PersistWindows: true})
	if err != nil {
		return err
	}
	closeStores = stop

	// wasm napps run in this process, so this process keeps what they
	// compile to
	logger := backend.Logger()
	wasmhost.SetLogger(logger)
	if err := wasmhost.SetCacheDir(filepath.Join(dataDir, "wasm-cache")); err != nil {
		logger.Warn().Err(err).Msg("wasm napps will be compiled every time they open")
	}
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

// UIKit is the script that puts the napp-ui kit in the page, for the napps
// whose metadata.json asks for it with `requires: ["ui"]`, and "" for the
// others. requiresJSON is that list as a JSON array, as OpenWindow carries it.
func UIKit(requiresJSON string) string {
	var requires []string
	if err := json.Unmarshal([]byte(requiresJSON), &requires); err != nil {
		return ""
	}
	return webview.UIKitScript(requires)
}

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

// LoginWithGoogle takes the ID token Credential Manager got from Google (for
// Pomegranate central's web client id) and runs the rest of the Google
// login. It returns immediately; the state's loginStatus tracks it, then
// phase and loginErr as with Login.
func LoginWithGoogle(idToken string) { go backend.LoginWithGoogleIDToken(idToken) }

// LoginFailed shows an error on the login screen, for logins that fail on
// the Android side before the backend ever sees them.
func LoginFailed(msg string) { backend.LoginFailed(msg) }

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

// UpdateAll applies every update the last check found.
func UpdateAll() { go backend.UpdateAll() }

// CheckForUpdates looks for newer versions of every installed napp on the
// discovery relays and each author's outbox relays, and flags the napps it
// found updates for (watch the state's updateCheckRunning/updateAvailable).
func CheckForUpdates() { go backend.CheckForUpdates() }

// Uninstall removes an installed napp.
func Uninstall(id string) { go backend.Uninstall(id) }

// Pin puts an installed napp on the launcher's home tab.
func Pin(id string) { backend.Pin(id) }

// Unpin takes a napp off the home tab.
func Unpin(id string) { backend.Unpin(id) }

// MovePin moves a pinned napp to position to of the home tab.
func MovePin(id string, to int) { backend.MovePin(id, to) }

// PinBundle puts a bundle on the home tab; UnpinBundle takes it off. A
// pinned bundle moves around with MovePin like a napp, by its home item key.
func PinBundle(name string)   { backend.PinBundle(name) }
func UnpinBundle(name string) { backend.UnpinBundle(name) }

// RunBundle opens every napp of a bundle and dispatches their actions, as a
// click on its home tab tile. Returns immediately; failures show up as the
// state's fetchErr.
func RunBundle(name string) {
	go func() {
		if err := backend.RunBundle(name); err != nil {
			backend.SetFetchErr("bundle failed: " + err.Error())
		}
	}()
}

// BundleDraftFromWindows starts a new bundle from the windows picked on the
// Windows tab: instancesJSON is their instance ids as a JSON array. Returns
// the editor's draft as JSON, [{nappId, label, lines}], one entry per napp,
// lines being its actions as one JSON object per line.
func BundleDraftFromWindows(instancesJSON string) string {
	var instances []string
	if err := json.Unmarshal([]byte(instancesJSON), &instances); err != nil {
		return "[]"
	}
	return draftJSON(backend.DraftFromWindows(instances))
}

// BundleDraft is a stored bundle as an editor draft (see
// BundleDraftFromWindows), "[]" when there is no bundle by that name.
func BundleDraft(name string) string {
	draft := backend.DraftOfStoredBundle(name)
	if draft == nil {
		return "[]"
	}
	return draftJSON(draft)
}

func draftJSON(draft []backend.BundleDraftEntry) string {
	data, err := json.Marshal(draft)
	if err != nil {
		return "[]"
	}
	return string(data)
}

// SaveBundle stores what the bundle editor holds: a new bundle when oldName
// is "", otherwise the one called oldName, renamed to name. draftJSON is
// the draft as BundleDraftFromWindows gave it, lines edited. The error
// says what to fix (a line that isn't JSON, a missing name).
func SaveBundle(oldName, name, draftJSON string) error {
	var draft []backend.BundleDraftEntry
	if err := json.Unmarshal([]byte(draftJSON), &draft); err != nil {
		return err
	}
	return backend.SaveBundleDraft(oldName, name, draft)
}

// DeleteBundle forgets a bundle and takes it off the home tab.
func DeleteBundle(name string) error { return backend.DeleteBundle(name) }

// ReopenWindow opens a window closed earlier again, back on what it was
// showing.
func ReopenWindow(instance string) { backend.ReopenWindow(instance) }

// KeepWindows is the windows Android still has a task for, as a JSON array
// of instance ids: the records of every other window are forgotten. Call it
// right after Start.
func KeepWindows(instancesJSON string) {
	var instances []string
	if err := json.Unmarshal([]byte(instancesJSON), &instances); err != nil {
		return
	}
	backend.KeepWindows(instances)
}

// KnowsWindow says whether a window is open in this process. One Android
// restored after killing the process isn't: it calls RecoverWindow.
func KnowsWindow(instance string) bool { return backend.KnowsWindow(instance) }

// RecoverWindow rebuilds a window Android restored after killing the
// process. The backend opens it again (OpenWindow, with the same instance
// and a fresh spec) and replays its actions; an error means there is no
// record to rebuild it from and the window should just go.
func RecoverWindow(instance string) error { return backend.RecoverWindow(instance) }

// Launch opens an installed napp, or surfaces the window it already has.
func Launch(id string) { backend.LaunchByID(id) }

// SetTheme tells the backend which theme the UI is drawing with, so napps can
// follow it. varsJSON is a flat object of CSS custom properties without the
// leading dashes: {"surface":"#fff","text":"#000",…}.
func SetTheme(name string, varsJSON string) { backend.SetTheme(name, varsJSON) }

// Profile returns what the launcher knows about a pubkey as JSON:
// pubkey, npub, name, displayName, shortName, about, picture, nip05,
// website. Blocks with its own timeout; call it off the main thread.
func Profile(pubkeyHex string) string {
	data, err := json.Marshal(backend.FetchProfileDetail(pubkeyHex))
	if err != nil {
		return "{}"
	}
	return string(data)
}

// AuthorNapps lists the kind:35130/35131 napps an author published as JSON: from
// the author's own write relays plus the launcher's discovery relays.
// Blocks with its own timeout; call it off the main thread.
func AuthorNapps(pubkeyHex string) string {
	data, err := json.Marshal(backend.FetchAuthorNapps(pubkeyHex))
	if err != nil {
		return "[]"
	}
	return string(data)
}

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
func WindowClosed(instance string) {
	if w := lookupWasm(instance); w != nil {
		w.close()
	}
	backend.WindowClosed(instance)
}

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
