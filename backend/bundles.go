package backend

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

// Bundles: the user picks windows on the Windows screen, gives the bundle a
// name and edits the actions each picked napp will get. A bundle lives in the
// launcher's state; it can be pinned to the home tab and it can get an OS
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

// Bundle is one bundle as the launcher keeps it in its state: the name the
// user gave it and the napps it opens, with their actions.
type Bundle struct {
	Name    string          `json:"name"`
	Entries []ShortcutEntry `json:"entries"`
}

// BundleInfo is one bundle as shown: whether it sits on the home tab, and
// whether it has an OS shortcut (File is that shortcut's file).
type BundleInfo struct {
	Name     string          `json:"name"`
	Entries  []ShortcutEntry `json:"entries"`
	Pinned   bool            `json:"pinned"`
	Shortcut bool            `json:"shortcut"`
	File     string          `json:"file,omitempty"`
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

// ─── OS shortcut files ───────────────────────────────────────────

// shortcutFiles is every bundle shortcut file on disk, keyed by the bundle
// name written into it. A bundle "has a shortcut" exactly when its name is
// here, so a file deleted behind our back turns the bundle back into a plain
// one, and nothing else.
//
// Cached, since a Snapshot() asks for it every frame, and thrown away
// whenever a shortcut file is written or deleted. A file added or removed
// behind our back shows up on the next run.
var shortcutCache atomic.Pointer[map[string]ShortcutFile]

func shortcutFiles() map[string]ShortcutFile {
	if c := shortcutCache.Load(); c != nil {
		return *c
	}
	files := make(map[string]ShortcutFile)
	if host != nil {
		for _, f := range host.ListShortcutFiles() {
			if f.Name == "" {
				f.Name = f.Path
			}
			files[f.Name] = f
		}
	}
	shortcutCache.Store(&files)
	return files
}

func reloadShortcuts() { shortcutCache.Store(nil) }

// importShortcutFiles turns every shortcut file with no bundle behind it into
// a bundle: the files were the only record of a bundle before bundles were
// kept in the state, and one copied over from elsewhere is a bundle as well.
// Must be called with stateMu held.
func importShortcutFiles() {
	for name, f := range shortcutFiles() {
		if bundleIndexLocked(name) >= 0 {
			continue
		}
		entries, err := parseBundleToken(f.Token)
		if err != nil {
			log.Warn().Err(err).Str("path", f.Path).Msg("unreadable shortcut token, skipping")
			continue
		}
		state.Bundles = append(state.Bundles, Bundle{Name: name, Entries: entries})
		log.Info().Str("name", name).Msg("bundle imported from its shortcut file")
	}
}

// ─── the bundle list ─────────────────────────────────────────────

// bundlePinPrefix marks a bundle among the napp ids of state.Pinned, so the
// home tab keeps napps and bundles in one order the user can drag around.
// Napp ids are "<hex>~<d>", which never start with it.
const bundlePinPrefix = "bundle:"

// BundlePinKey is what a bundle is called in the home tab's order (see
// MovePin).
func BundlePinKey(name string) string { return bundlePinPrefix + name }

// bundleIndexLocked is where a bundle is in state.Bundles, or -1. Must be
// called with stateMu held.
func bundleIndexLocked(name string) int {
	for i, b := range state.Bundles {
		if b.Name == name {
			return i
		}
	}
	return -1
}

// bundles is what the Windows screen lists, by name.
func bundles() []BundleInfo {
	files := shortcutFiles()
	stateMu.Lock()
	list := make([]BundleInfo, 0, len(state.Bundles))
	for _, b := range state.Bundles {
		info := BundleInfo{
			Name:    b.Name,
			Entries: b.Entries,
			Pinned:  slices.Contains(state.Pinned, BundlePinKey(b.Name)),
		}
		if f, ok := files[b.Name]; ok {
			info.Shortcut = true
			info.File = f.Path
		}
		list = append(list, info)
	}
	stateMu.Unlock()
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	return list
}

func bundleByName(name string) (Bundle, bool) {
	stateMu.Lock()
	defer stateMu.Unlock()
	if i := bundleIndexLocked(name); i >= 0 {
		return state.Bundles[i], true
	}
	return Bundle{}, false
}

// parseBundleEntries reads and checks a JSON-encoded []ShortcutEntry.
func parseBundleEntries(entries string) ([]ShortcutEntry, error) {
	var parsed []ShortcutEntry
	if err := json.Unmarshal([]byte(entries), &parsed); err != nil {
		return nil, errors.New("bad bundle spec: " + err.Error())
	}
	if len(parsed) == 0 {
		return nil, errors.New("pick at least one window for the bundle")
	}
	for _, e := range parsed {
		if e.NappID == "" {
			return nil, errors.New("bundle entry without a napp")
		}
		for _, a := range e.Actions {
			if err := validActionName(a.Type); err != nil {
				return nil, err
			}
		}
	}
	return parsed, nil
}

// SaveBundle stores a bundle: a new one when oldName is "", otherwise the
// one called oldName, renamed to name if that changed. A renamed bundle stays
// where it was on the home tab, and one with an OS shortcut gets its file
// rewritten, so the shortcut always runs what the bundle says. entries is a
// JSON-encoded []ShortcutEntry.
func SaveBundle(oldName, name, entries string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("give the bundle a name")
	}
	parsed, err := parseBundleEntries(entries)
	if err != nil {
		return err
	}

	stateMu.Lock()
	if name != oldName && bundleIndexLocked(name) >= 0 {
		stateMu.Unlock()
		return fmt.Errorf("there is a bundle called %q already", name)
	}
	if i := bundleIndexLocked(oldName); oldName != "" && i >= 0 {
		state.Bundles[i] = Bundle{Name: name, Entries: parsed}
		if j := slices.Index(state.Pinned, BundlePinKey(oldName)); j >= 0 {
			state.Pinned[j] = BundlePinKey(name)
		}
	} else {
		state.Bundles = append(state.Bundles, Bundle{Name: name, Entries: parsed})
	}
	saveState()
	stateMu.Unlock()

	// the shortcut follows the bundle: rewritten under the new name with
	// the new token, and the old file gone when that is a different file
	var shortcutErr error
	if old, ok := shortcutFiles()[oldName]; oldName != "" && ok {
		file, err := host.CreateShortcutFile(name, bundleToken(parsed))
		if err != nil {
			log.Warn().Err(err).Str("name", name).Msg("could not rewrite the bundle's shortcut file")
			shortcutErr = fmt.Errorf("%w: %v", ErrShortcutNotUpdated, err)
		} else if file != old.Path {
			if err := host.DeleteShortcutFile(old.Path); err != nil {
				log.Warn().Err(err).Str("path", old.Path).Msg("could not remove the replaced shortcut file")
			}
		}
		reloadShortcuts()
	}

	notifyState()
	log.Info().Str("name", name).Str("old", oldName).Int("entries", len(parsed)).Msg("bundle saved")
	return shortcutErr
}

// ErrShortcutNotUpdated is what SaveBundle says when the bundle was saved
// but its OS shortcut could not be rewritten to match.
var ErrShortcutNotUpdated = errors.New("bundle saved, but its shortcut could not be updated")

// DeleteBundle forgets a bundle, takes it off the home tab and removes its
// OS shortcut, if it had one.
func DeleteBundle(name string) error {
	stateMu.Lock()
	i := bundleIndexLocked(name)
	if i < 0 {
		stateMu.Unlock()
		return nil
	}
	state.Bundles = slices.Delete(state.Bundles, i, i+1)
	state.Pinned = slices.DeleteFunc(state.Pinned, func(p string) bool { return p == BundlePinKey(name) })
	saveState()
	stateMu.Unlock()

	err := RemoveBundleShortcut(name)
	notifyState()
	log.Info().Str("name", name).Msg("bundle deleted")
	return err
}

// PinBundle puts a bundle on the home tab, after whatever was pinned before.
func PinBundle(name string) {
	stateMu.Lock()
	key := BundlePinKey(name)
	if bundleIndexLocked(name) < 0 || slices.Contains(state.Pinned, key) {
		stateMu.Unlock()
		return
	}
	state.Pinned = append(state.Pinned, key)
	saveState()
	stateMu.Unlock()
	notifyState()
}

// UnpinBundle takes a bundle off the home tab.
func UnpinBundle(name string) { Unpin(BundlePinKey(name)) }

// CreateBundleShortcut gives a bundle an OS shortcut file on this platform,
// so it can be opened from the desktop's own menus without the launcher.
func CreateBundleShortcut(name string) error {
	b, ok := bundleByName(name)
	if !ok {
		return fmt.Errorf("no bundle called %q", name)
	}
	file, err := host.CreateShortcutFile(b.Name, bundleToken(b.Entries))
	if err != nil {
		log.Warn().Err(err).Str("name", name).Msg("could not create a shortcut file")
		return err
	}
	reloadShortcuts()
	notifyState()
	log.Info().Str("name", name).Str("file", file).Msg("bundle shortcut created")
	return nil
}

// RemoveBundleShortcut deletes a bundle's OS shortcut file. The bundle
// itself stays.
func RemoveBundleShortcut(name string) error {
	f, ok := shortcutFiles()[name]
	if !ok {
		return nil
	}
	if err := host.DeleteShortcutFile(f.Path); err != nil {
		return err
	}
	reloadShortcuts()
	notifyState()
	log.Info().Str("name", name).Msg("bundle shortcut removed")
	return nil
}

// RunBundle opens a stored bundle's napps, as its shortcut would.
func RunBundle(name string) error {
	b, ok := bundleByName(name)
	if !ok {
		return fmt.Errorf("no bundle called %q", name)
	}
	return RunShortcutEntries(b.Entries)
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
		if len(entry.Actions) == 0 {
			// a shortcut open is not an Open-button open: it
			// launches without counting toward the installed
			// list's most-opened order (see markLaunched).
			go func(n Napp) {
				if _, err := launch(context.Background(), n); err != nil {
					log.Error().Err(err).Str("napp", n.ID).Msg("launch failed")
					SetFetchErr("launch failed: " + err.Error())
				}
			}(napp)
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
// A shortcut-driven open never counts toward the installed list's
// most-opened order (see markLaunched): only Open-button opens do.
func openAndDispatch(ctx context.Context, napp Napp, actions []ShortcutAction) error {
	if running := runningForNapp(napp.ID); len(running) > 0 {
		log.Info().Str("napp", napp.ID).Str("instance", running[0].instance).Msg("shortcut reusing open instance")
		return dispatchActions(ctx, running[0], actions)
	}

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
		actionCtx, cancel := context.WithTimeout(ctx, replyWait)
		_, err := dispatchToInstance(actionCtx, ci, &actionRequest{name: action.Type, payload: action.Payload})
		cancel()
		if err != nil {
			log.Warn().Err(err).Str("napp", ci.napp.ID).Str("action", action.Type).
				Msg("shortcut action dispatch failed")
		}
	}
	return nil
}
