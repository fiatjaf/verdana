package backend

import "errors"

// Host is the platform side of the launcher: everything the backend needs
// done that depends on where it is running. The Gio desktop app implements it
// with OS windows and child processes, the Android app with WebView tabs.
//
// Every method may be called from any goroutine.
type Host interface {
	// OpenWindow puts a napp on screen and returns the Transport the backend
	// will talk to it through. It should return as soon as the window exists
	// (or is on its way): a napp announces its own readiness later, by
	// registering its actions.
	//
	// The platform must call WindowClosed when the window goes away and
	// HandleWireMessage for everything the napp's bridge sends up.
	OpenWindow(spec WindowSpec) (Transport, error)

	// OpenPicker puts up a window of its own for the user to pick which napp
	// handles an action (see picker.go). It shows the prompt it is sent the
	// way a napp window shows an overlay, and answers it the same way. A
	// host with no such window fails, and an action with more than one
	// handler to choose from then goes nowhere.
	//
	// The platform calls WindowClosed when it goes away, unless it went
	// away to make room for the napp that was picked (see WindowSpec.Reuse).
	OpenPicker(spec PickerSpec) (Transport, error)

	// StateChanged says the launcher's State() changed and whatever renders
	// it should render it again.
	StateChanged()

	// PromptsChanged says CurrentPrompt() changed: a napp is now blocked on
	// the user, or has stopped being.
	PromptsChanged()

	// CopyText puts text on the system clipboard. Already approved.
	CopyText(text string) error

	// SaveFile writes bytes where the user keeps downloads and returns the
	// name it ended up under. Already approved.
	SaveFile(name string, data []byte) (string, error)

	// SaveFileTarget names that destination for the approval prompt
	// ("~/Downloads", "your Downloads folder"…).
	SaveFileTarget() string

	// OpenLink hands an http(s) url to the platform's browser. Already
	// approved.
	OpenLink(url string) error

	// CreateShortcutFile writes a shortcut a desktop environment understands
	// running `the launcher with token` (a bundle token, see shortcuts.go)
	// and returns the path it wrote. Only the desktop host does anything.
	CreateShortcutFile(name, token string) (string, error)

	// DeleteShortcutFile removes a shortcut file this host wrote before.
	DeleteShortcutFile(path string) error

	// ListShortcutFiles reads back every bundle shortcut this launcher wrote
	// before: the OS shortcut files are where shortcuts live, so this is
	// where the launcher rediscovers them. Anything unreadable or unparseable
	// is left out. Nothing where there are no OS shortcuts.
	ListShortcutFiles() []ShortcutFile

	// AmberRequest hands a NIP-55 operation (sign_event, nip44_encrypt, …)
	// to the phone's signer app — the Android host launches the signer and
	// the answer comes back to AnswerAmber with the same id. False means
	// the signer app could not be launched at all.
	AmberRequest(id, op, payload, pubkey, counterpart, pkg string) bool
}

// ShortcutFile is one bundle shortcut found on disk: the name the user gave
// it, the file it is (to delete it) and the token it runs.
type ShortcutFile struct {
	Name  string
	Path  string
	Token string
}

// Transport is one napp window, seen from the backend: a place to send wire
// messages and a way to make it go away.
type Transport interface {
	// Send delivers a message to the napp's shell (a resp, an eval, an
	// action dispatch, a theme change).
	Send(msg WireMsg)

	// Close asks the window to close. The platform is expected to call
	// WindowClosed afterwards.
	Close()
}

// WindowSpec is what a platform needs to know to show a napp.
type WindowSpec struct {
	// Instance is window.napp.instance, and what the window is called on
	// screen: 4 random bytes in hex, unique per window.
	Instance string

	NappID      string
	Name        string
	Description string

	// Width and Height are the window's initial size in pixels, from the
	// napp's initial_size or the roomy default (see Napp.WindowSize).
	Width  int
	Height int

	// Dir holds the napp's unpacked files (index.html and friends).
	Dir string

	// Mode is how the napp is hosted: ModeWeb for the usual page in a
	// webview, ModeWasm for a module painting a canvas. A host that has no
	// wasm window says so by failing OpenWindow.
	Mode string

	// Wasm is the napp's module, an absolute path, set only for ModeWasm.
	Wasm string

	// URL navigates the shell straight to a page instead of serving Dir:
	// dev napps use it (a dev-server url, or the throwaway server the
	// backend runs for folder dev napps). Empty for installed napps.
	URL string

	// Requires are the domains the napp asked to reach (behavior.md).
	Requires []string

	// Theme and ThemeVars are the launcher's current theme, so the napp
	// paints right from its first frame instead of flashing.
	Theme     string
	ThemeVars string

	// StorageJSON is the napp's localStorage snapshot as a JSON object
	// string, injected at document-start so the bridge's synchronous shim
	// starts from it. Writes go back through the napp.storage* rpcs.
	StorageJSON string

	// Reuse is the picker window the user just picked this napp in, when
	// that is how it is being opened. The napp is to land in it instead of
	// in a new window: in place, when the picker is the kind of window this
	// napp is drawn in, or by closing it and opening a new one otherwise.
	// Either way the picker's transport is the host's to finish with.
	Reuse Transport
}

// PickerSpec is what a platform needs to know to show a picker window.
type PickerSpec struct {
	// Instance is the picker's window id, and the napp's if the picker
	// becomes the window of the napp the user picked.
	Instance string

	Theme     string
	ThemeVars string
}

// noopHost stands in when a caller (a test, a one-off tool) has no GUI.
type noopHost struct{}

func (noopHost) OpenWindow(WindowSpec) (Transport, error) {
	return nil, errors.New("this host cannot open windows")
}
func (noopHost) OpenPicker(PickerSpec) (Transport, error) {
	return nil, errors.New("this host cannot open windows")
}
func (noopHost) StateChanged()                           {}
func (noopHost) PromptsChanged()                         {}
func (noopHost) CopyText(string) error                   { return errors.New("no clipboard") }
func (noopHost) SaveFile(string, []byte) (string, error) { return "", errors.New("no filesystem") }
func (noopHost) SaveFileTarget() string                  { return "" }
func (noopHost) OpenLink(string) error                   { return errors.New("no browser") }
func (noopHost) CreateShortcutFile(string, string) (string, error) {
	return "", errors.New("no shortcuts here")
}
func (noopHost) DeleteShortcutFile(string) error   { return nil }
func (noopHost) ListShortcutFiles() []ShortcutFile { return nil }
func (noopHost) AmberRequest(string, string, string, string, string, string) bool {
	return false
}
