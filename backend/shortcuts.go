package backend

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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

// ShortcutInfo is one created shortcut, as stored and shown. File is the OS
// shortcut path this platform wrote, so deleting the shortcut deletes it.
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

// ─── stored shortcuts ────────────────────────────────────────────

// shortcuts is the stored shortcut list, for a Snapshot.
func shortcuts() []ShortcutInfo {
	stateMu.Lock()
	defer stateMu.Unlock()
	return append([]ShortcutInfo(nil), state.Shortcuts...)
}

func shortcutByName(name string) (*ShortcutInfo, int) {
	for i, s := range state.Shortcuts {
		if s.Name == name {
			return &state.Shortcuts[i], i
		}
	}
	return nil, -1
}

// CreateShortcut stores a shortcut and gives it an OS shortcut file on this
// platform. An existing shortcut with the same name is replaced. entries is
// a JSON-encoded []ShortcutEntry.
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

	stateMu.Lock()
	info := ShortcutInfo{Name: name, Entries: parsed, File: file}
	replaced := false
	for i := range state.Shortcuts {
		if state.Shortcuts[i].Name == name {
			if state.Shortcuts[i].File != "" && state.Shortcuts[i].File != file {
				stateMu.Unlock()
				host.DeleteShortcutFile(state.Shortcuts[i].File)
				stateMu.Lock()
			}
			state.Shortcuts[i] = info
			replaced = true
		}
	}
	if !replaced {
		state.Shortcuts = append(state.Shortcuts, info)
	}
	saveState()
	stateMu.Unlock()
	notifyState()
	log.Info().Str("name", name).Int("entries", len(parsed)).Str("file", file).Msg("bundle shortcut created")
	return nil
}

// DeleteShortcut forgets a shortcut and removes its OS shortcut file.
func DeleteShortcut(name string) error {
	stateMu.Lock()
	idx := -1
	for i := range state.Shortcuts {
		if state.Shortcuts[i].Name == name {
			idx = i
		}
	}
	if idx == -1 {
		stateMu.Unlock()
		return nil
	}
	file := state.Shortcuts[idx].File
	state.Shortcuts = append(state.Shortcuts[:idx], state.Shortcuts[idx+1:]...)
	saveState()
	stateMu.Unlock()

	if file != "" {
		host.DeleteShortcutFile(file)
	}
	notifyState()
	log.Info().Str("name", name).Msg("bundle shortcut deleted")
	return nil
}

// ─── running a bundle ────────────────────────────────────────────

// RunShortcutToken opens a bundle token's napps, dispatching each napp's
// actions in order. Meant for a launcher being started by one of its
// shortcut files (or forwarded the token by a second invocation).
func RunShortcutToken(token string) error {
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

	for _, entry := range entries {
		napp, ok := InstalledNapp(entry.NappID)
		if !ok {
			SetFetchErr("shortcut napp " + entry.NappID + " is not installed")
			continue
		}
		if len(entry.Actions) == 0 {
			Launch(napp)
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

// openAndDispatch launches a napp (or finds its running instance) and sends
// each action straight there, in order, with no handler-picking prompt: a
// shortcut names its napp exactly, unlike an unknown-caller action dispatch.
func openAndDispatch(ctx context.Context, napp Napp, actions []ShortcutAction) error {
	if running := runningForNapp(napp.ID); len(running) > 0 {
		log.Info().Str("napp", napp.ID).Str("instance", running[0].instance).Msg("shortcut reusing open instance")
		return dispatchActions(ctx, running[0], actions)
	}

	markLaunched(napp.ID)
	ci, err := launch(ctx, napp)
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
