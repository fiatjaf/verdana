package backend

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Bundle shortcuts: the user picks windows on the Windows screen, gives the
// bundle a name, edits the actions each picked napp will get and gets an OS
// shortcut that calls the launcher with a bundle token.
//
// A bundle token is a single string of space-separated fields:
//
//	<napp-id> +<action> +<action> <napp-id> +<action> …
//
// A field starting with "+" is one action for the napp that came just before
// it (a base64url-encoded {"type":…,"payload":…} object, or a bare action
// name); any other field starts a new napp entry. The launcher (this very
// process, or the running one a second invocation forwards to) walks the
// list, opening each napp and dispatching its actions in order.

// ShortcutAction is one action of a bundle entry, as the user writes it in
// the editor: an action name (its "type") and the payload to hand it, which
// is any JSON — a string, an event, an object.
type ShortcutAction struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// ShortcutEntry is one napp of a bundle and the actions it gets when the
// bundle opens.
type ShortcutEntry struct {
	NappID  string           `json:"nappId"`
	Actions []ShortcutAction `json:"actions"`
}

// ShortcutInfo is one created shortcut, as shown. Entries come back from the
// bundle token in its OS shortcut file and File is that file, so deleting the
// shortcut deletes it.
type ShortcutInfo struct {
	Name    string          `json:"name"`
	Entries []ShortcutEntry `json:"entries"`
	File    string          `json:"file,omitempty"`
}

// ─── bundle tokens ───────────────────────────────────────────────

// bundleToken encodes entries as launcher arguments. Actions go in as one
// base64url word each, so the token survives a .desktop Exec line, a
// powershell argument and a shell script line without any quoting games.
func bundleToken(entries []ShortcutEntry) string {
	var fields []string
	for _, e := range entries {
		if e.NappID == "" {
			continue
		}
		fields = append(fields, e.NappID)
		for _, a := range e.Actions {
			raw, err := json.Marshal(a)
			if err != nil {
				continue
			}
			fields = append(fields, "+"+base64.RawURLEncoding.EncodeToString(raw))
		}
	}
	return strings.Join(fields, " ")
}

// parseBundleToken turns a token back into entries. Anything that doesn't
// build (empty fields, malformed actions) is rejected here so a broken
// shortcut fails loudly before the launcher starts opening windows.
func parseBundleToken(token string) ([]ShortcutEntry, error) {
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("empty bundle token")
	}
	var entries []ShortcutEntry
	for _, field := range strings.Fields(token) {
		if !strings.HasPrefix(field, "+") {
			entries = append(entries, ShortcutEntry{NappID: field})
			continue
		}
		if len(entries) == 0 {
			return nil, errors.New("bundle token starts with an action and no napp")
		}
		action, err := parseActionField(strings.TrimPrefix(field, "+"))
		if err != nil {
			return nil, err
		}
		entries[len(entries)-1].Actions = append(entries[len(entries)-1].Actions, action)
	}
	return entries, nil
}

// parseActionField reads one "+" field: the encoded {"type","payload"} object
// bundleToken writes, or — for a token typed or stored by an older build —
// a bare action name with no payload.
func parseActionField(field string) (ShortcutAction, error) {
	if raw, err := base64.RawURLEncoding.DecodeString(field); err == nil {
		var action ShortcutAction
		if err := json.Unmarshal(raw, &action); err == nil && action.Type != "" {
			return action, nil
		}
	}
	if err := validActionName(field); err != nil {
		return ShortcutAction{}, err
	}
	return ShortcutAction{Type: field}, nil
}

// validActionName keeps action names single-word: no whitespace, no "+"
// prefix, which would corrupt the bundle token of a shortcut.
func validActionName(action string) error {
	if strings.TrimSpace(action) == "" {
		return errors.New("action name is empty")
	}
	if strings.ContainsAny(action, " \t\v\n") {
		return fmt.Errorf("action %q has spaces inside", action)
	}
	if strings.HasPrefix(action, "+") {
		return fmt.Errorf("action %q starts with a +: that is reserved", action)
	}
	return nil
}

// ─── the shortcut list ───────────────────────────────────────────

// shortcuts is what the Windows screen lists, read back from the OS shortcut
// files: the files are the record of what the user created, so a shortcut
// survives anything that happens to our own state, and disappears from the
// list exactly when its file does.
//
// Cached, since a Snapshot() asks for it every frame, and thrown away whenever
// a shortcut is created or deleted. A file added or removed behind our back
// shows up on the next run.
var shortcutCache atomic.Pointer[[]ShortcutInfo]

func shortcuts() []ShortcutInfo {
	if c := shortcutCache.Load(); c != nil {
		return *c
	}
	files := host.ListShortcutFiles()
	list := make([]ShortcutInfo, 0, len(files))
	for _, f := range files {
		entries, err := parseBundleToken(f.Token)
		if err != nil {
			log.Warn().Err(err).Str("path", f.Path).Msg("unreadable shortcut token, skipping")
			continue
		}
		if f.Name == "" {
			f.Name = f.Path
		}
		list = append(list, ShortcutInfo{Name: f.Name, Entries: entries, File: f.Path})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	shortcutCache.Store(&list)
	return list
}

func reloadShortcuts() { shortcutCache.Store(nil) }

func shortcutByName(name string) *ShortcutInfo {
	for _, s := range shortcuts() {
		if s.Name == name {
			s := s
			return &s
		}
	}
	return nil
}

// CreateShortcut gives a bundle an OS shortcut file on this platform. An
// existing shortcut with the same name is replaced. entries is a
// JSON-encoded []ShortcutEntry.
func CreateShortcut(name string, entries string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("give the bundle a name")
	}
	var parsed []ShortcutEntry
	if err := json.Unmarshal([]byte(entries), &parsed); err != nil {
		return errors.New("bad shortcut spec: " + err.Error())
	}
	if len(parsed) == 0 {
		return errors.New("pick at least one window for the bundle")
	}
	for _, e := range parsed {
		if e.NappID == "" {
			return errors.New("shortcut entry without a napp")
		}
		for _, a := range e.Actions {
			if err := validActionName(a.Type); err != nil {
				return err
			}
		}
	}

	file, err := host.CreateShortcutFile(name, bundleToken(parsed))
	if err != nil {
		log.Warn().Err(err).Str("name", name).Msg("could not create a shortcut file")
		return err
	}

	// The file we just wrote is what the list now reads from, and re-using a
	// name can land on a different file (a rename): the old one goes.
	if old := shortcutByName(name); old != nil && old.File != "" && old.File != file {
		if err := host.DeleteShortcutFile(old.File); err != nil {
			log.Warn().Err(err).Str("path", old.File).Msg("could not remove the replaced shortcut file")
		}
	}

	reloadShortcuts()
	notifyState()
	log.Info().Str("name", name).Int("entries", len(parsed)).Str("file", file).Msg("bundle shortcut created")
	return nil
}

// DeleteShortcut removes a shortcut's OS shortcut file, which is all a
// shortcut is.
func DeleteShortcut(name string) error {
	sc := shortcutByName(name)
	if sc == nil {
		return nil
	}
	if sc.File != "" {
		if err := host.DeleteShortcutFile(sc.File); err != nil {
			return err
		}
	}
	reloadShortcuts()
	notifyState()
	log.Info().Str("name", name).Msg("bundle shortcut deleted")
	return nil
}

// ─── running a bundle ────────────────────────────────────────────

// RunShortcutToken opens a bundle token's napps, dispatching each napp's
// actions in order. Meant for a launcher being started by one of its
// shortcut files (or forwarded the token by a second invocation).
func RunShortcutToken(token string) error {
	// a napp address (an naddr, a nostr: link) opens what it names, and
	// offers to install it first when it isn't
	if IsNappAddress(token) {
		return OpenAddress(token)
	}
	entries, err := parseBundleToken(token)
	if err != nil {
		return err
	}
	return RunShortcutEntries(entries)
}

// RunShortcutEntries opens one napp per entry and sends the napp its
// actions, sequentially, with the same context (a shortcut run can span
// several windows for a while).
func RunShortcutEntries(entries []ShortcutEntry) error {
	ctx, cancel := context.WithTimeout(context.Background(), shortcutActionTimeout*time.Duration(len(entries)+1))
	defer cancel()

	// A bundle run in a launcher that is still starting up waits for its
	// login to answer, so the windows it opens can already sign. One served
	// by a launcher that is already up never waits.
	waitStartupLogin(ctx)

	for _, entry := range entries {
		napp, ok := InstalledNapp(entry.NappID)
		if !ok {
			SetFetchErr("shortcut napp " + entry.NappID + " is not installed")
			continue
		}
		if err := openAndDispatch(ctx, napp, entry.Actions); err != nil {
			SetFetchErr("shortcut failed on " + napp.Label() + ": " + err.Error())
		}
	}
	return nil
}

// shortcutActionTimeout bounds how long a bundle run may take before it
// gives up dispatching: each napp's window gets its share.
const shortcutActionTimeout = 60 * time.Second

// shortcutLaunchMu makes the running-instance check and instance registration
// one operation. Two launcher entries opened at the same moment therefore
// converge on one napp window instead of both observing that none exists.
var shortcutLaunchMu sync.Mutex

// openAndDispatch launches a napp (or finds its running instance) and sends
// each action straight there, in order, with no handler-picking prompt: a
// shortcut names its napp exactly, unlike an unknown-caller action dispatch.
func openAndDispatch(ctx context.Context, napp Napp, actions []ShortcutAction) error {
	shortcutLaunchMu.Lock()
	if running := runningForNapp(napp.ID); len(running) > 0 {
		ci := running[0]
		shortcutLaunchMu.Unlock()
		log.Info().Str("napp", napp.ID).Str("instance", running[0].instance).Msg("shortcut reusing open instance")
		ci.focus()
		return dispatchActions(ctx, ci, actions)
	}

	markLaunched(napp.ID)
	ci, err := launch(ctx, napp)
	shortcutLaunchMu.Unlock()
	if err != nil {
		return err
	}
	return dispatchActions(ctx, ci, actions)
}

// dispatchActions sends every action of one bundle entry in order, name and
// payload as the user wrote it, logging the failures so the run never stops
// mid-way over a single stuck action.
func dispatchActions(ctx context.Context, ci *Instance, actions []ShortcutAction) error {
	for _, action := range actions {
		if _, err := dispatchToInstance(ctx, ci, &actionRequest{name: action.Type, payload: action.Payload}); err != nil {
			log.Warn().Err(err).Str("napp", ci.napp.ID).Str("action", action.Type).
				Msg("shortcut action dispatch failed")
		}
	}
	return nil
}
