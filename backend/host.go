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

	// OpenDiscovery brings the launcher to Discovery and limits the catalog
	// to napplets advertising archetype.
	OpenDiscovery(archetype string)

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

	// NotificationControls names the NAP-NOTIFY controls this platform can
	// actually provide. SendNotification displays one system notification;
	// the returned handle belongs to the napplet session and is dismissed
	// when that session ends. Both methods may be called from any goroutine.
	NotificationControls() []string
	RequestNotificationPermission() bool
	SendNotification(NotificationRequest) (NotificationHandle, error)

	// MediaPlay hands an https url to the platform's media player
	// (NAP-MEDIA shell-owned playback). Already approved, and the url
	// already checked. onState may be called from any goroutine for as long
	// as the player lives, and once more with status "stopped" when it ends.
	MediaPlay(req MediaRequest, onState func(MediaState)) (MediaPlayer, error)

	// OpenSettings puts a napp's settings window on screen: the launcher's
	// settings page (webview.SettingsHTML), whose messages go to
	// HandleSettingsMessage under spec.Window. The platform calls
	// SettingsClosed once it is gone.
	OpenSettings(spec SettingsSpec) (Transport, error)
}

// NotificationRequest is a validated NAP-NOTIFY notification. Text is plain
// text, Actions has at most three entries, and ID is unique for the lifetime
// of the owning window.
type NotificationRequest struct {
	ID       string
	NappID   string
	NappName string
	Title    string
	Body     string
	Icon     string
	Channel  string
	Priority string
	Actions  []NotificationAction
}

type NotificationAction struct {
	ID    string
	Label string
}

// NotificationHandle controls one notification already handed to the OS.
// Dismiss must return quickly because session teardown calls it while locked.
type NotificationHandle interface {
	Dismiss() error
}

// MediaRequest is one thing to play.
type MediaRequest struct {
	URL      string
	MimeType string
	// Title is display text for the player's window, already sanitized.
	Title    string
	Live     bool
	Autoplay bool
}

// MediaState is a player's playback state, as NAP-MEDIA reports it. Nil
// fields are ones the player doesn't know (yet).
type MediaState struct {
	// Status is "playing", "paused", "stopped" or "buffering".
	Status   string
	Position *float64
	Duration *float64
	// Volume is 0..1.
	Volume *float64
}

// MediaPlayer controls one playback started by MediaPlay. No method may
// block for long: Stop in particular is called with the napplet session
// locked, so it asks the player to quit and returns.
type MediaPlayer interface {
	Play() error
	Pause() error
	Stop() error
	// Seek goes to a position in seconds.
	Seek(sec float64) error
	// SetVolume sets output volume, 0..1.
	SetVolume(v float64) error
	SetTitle(title string) error
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

	// Focus asks the platform to surface an existing window. Platforms that
	// cannot reliably raise another process may leave this as a best effort.
	Focus()

	// Close asks the window to close. The platform is expected to call
	// WindowClosed afterwards.
	Close()
}

// WindowSpec is what a platform needs to know to show a napp.
type WindowSpec struct {
	// Instance is window.napp.instance: a serial, unique per window.
	Instance string
	// Number is the desktop-facing window number, stable for this open window.
	Number int

	NappID      string
	Name        string
	Description string

	// Width and Height are the window's initial size in pixels, from the
	// napp's initial_size or the roomy default (see Napp.WindowSize).
	Width  int
	Height int

	// Dir holds the napp's unpacked files (index.html and friends).
	Dir string

	// URL navigates the shell straight to a page instead of serving Dir:
	// dev napps use it (a dev-server url, or the throwaway server the
	// backend runs for folder dev napps). Empty for installed napps.
	URL string

	// Requires are the domains the napp asked to reach (behavior.md).
	Requires []string

	// Format is "napplet" for a napplet window: the shell then loads the
	// napplet host page (webview.NappletHostHTML) with only the host script,
	// instead of serving Dir/URL with bridge.js. Empty for napps.
	Format string

	// Theme and ThemeVars are the launcher's current theme, so the napp
	// paints right from its first frame instead of flashing.
	Theme     string
	ThemeVars string

	// StorageJSON is the napp's localStorage snapshot as a JSON object
	// string, injected at document-start so the bridge's synchronous shim
	// starts from it. Writes go back through the napp.storage* rpcs.
	StorageJSON string
}

// noopHost stands in when a caller (a test, a one-off tool) has no GUI.
type noopHost struct{}

func (noopHost) OpenWindow(WindowSpec) (Transport, error) {
	return nil, errors.New("this host cannot open windows")
}
func (noopHost) OpenDiscovery(string)                    {}
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
func (noopHost) NotificationControls() []string      { return nil }
func (noopHost) RequestNotificationPermission() bool { return false }
func (noopHost) SendNotification(NotificationRequest) (NotificationHandle, error) {
	return nil, errors.New("no notification service")
}
func (noopHost) MediaPlay(MediaRequest, func(MediaState)) (MediaPlayer, error) {
	return nil, errors.New("no media player")
}
func (noopHost) OpenSettings(SettingsSpec) (Transport, error) {
	return nil, errors.New("this host cannot open windows")
}
