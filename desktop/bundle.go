package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"gioui.org/widget"
	"verdana/backend"
)

// The bundle editor: everything the Gio window needs to turn the checkboxes
// of the Windows tab into a stored bundle (with one call to
// backend.SaveBundle behind it).

// pickedBundleWindows is the selected windows, in listed order, merged into
// one entry per napp (the same napp may have two windows checked), each entry
// carrying the actions those windows were actually sent: that is what the
// editor suggests.
func pickedBundleWindows(st backend.State) []backend.ShortcutEntry {
	var entries []backend.ShortcutEntry
	byID := make(map[string]int)
	for _, w := range st.ManagedWindows {
		cb, ok := bundleChecks[w.Instance]
		if !ok || !cb.Value {
			continue
		}
		if i, ok := byID[w.NappID]; ok {
			// actions are per napp: a second window of the same napp only
			// adds what its own window was sent
			entries[i].Actions = append(entries[i].Actions, w.History...)
			continue
		}
		byID[w.NappID] = len(entries)
		entries = append(entries, backend.ShortcutEntry{NappID: w.NappID, Actions: w.History})
	}
	return entries
}

// newBundleEditState builds the editor overlay for a fresh bundle, from
// the checked windows' napps.
func newBundleEditState(entries []backend.ShortcutEntry) *bundleEditState {
	out := &bundleEditState{name: ""}
	out.nameEd.SingleLine = true
	for _, e := range entries {
		napp, ok := backend.InstalledNapp(e.NappID)
		if !ok {
			continue
		}
		ed := widget.Editor{}
		// default content: the actions those windows were sent, payloads
		// included, then whatever else the napp declares handling
		ed.SetText(suggestionLines(e.Actions, napp.Actions))
		out.entries = append(out.entries, bundleEditEntry{nappID: e.NappID, label: napp.Label(), ed: ed})
	}
	return out
}

// editBundleEditState builds the overlay pre-filled with a stored bundle, so
// the name and every action textarea come out editable.
func editBundleEditState(sc backend.BundleInfo) *bundleEditState {
	out := &bundleEditState{name: sc.Name}
	out.nameEd.SingleLine = true
	out.nameEd.SetText(sc.Name)
	for _, e := range sc.Entries {
		label := e.NappID
		if napp, ok := backend.InstalledNapp(e.NappID); ok {
			label = napp.Label()
		}
		ed := widget.Editor{}
		ed.SetText(actionsToLines(e.Actions))
		out.entries = append(out.entries, bundleEditEntry{nappID: e.NappID, label: label, ed: ed})
	}
	return out
}

// actionLines is what a fresh bundle's textareas start with when no window of
// the napp has handled anything yet: one line per action the napp declares,
// as {"type": …} with no payload yet.
func actionLines(names []string) string {
	actions := make([]backend.ShortcutAction, 0, len(names))
	for _, name := range names {
		actions = append(actions, backend.ShortcutAction{Type: name})
	}
	return actionsToLines(actions)
}

// maxSuggestedActions is how much of what a window was sent the editor
// suggests before falling back on the napp's own list: enough to rebuild a
// bundle of what the windows were just doing, short enough to read and edit.
const maxSuggestedActions = 8

// suggestionLines is what a fresh bundle's textarea starts with: the actions
// the picked windows were actually sent, with the payloads they carried,
// most recent first and without repeats, then whatever else the napp declares
// handling and none of that already covers.
func suggestionLines(seen []backend.ShortcutAction, declared []string) string {
	line := func(a backend.ShortcutAction) string {
		raw, err := json.Marshal(a)
		if err != nil {
			return ""
		}
		return string(raw)
	}

	taken := make(map[string]bool, len(seen))
	var lines []string
	for i := len(seen) - 1; i >= 0 && len(lines) < maxSuggestedActions; i-- {
		key := seen[i].Type + "\n" + string(seen[i].Payload)
		if taken[key] {
			continue
		}
		taken[key] = true
		if l := line(seen[i]); l != "" {
			lines = append(lines, l)
		}
	}

	for _, name := range declared {
		if len(lines) >= maxSuggestedActions {
			break
		}
		// a declared name the window was already sent shows up above, with
		// the payload it was sent
		already := false
		for _, a := range seen {
			if a.Type == name {
				already = true
				break
			}
		}
		if already {
			continue
		}
		if l := line(backend.ShortcutAction{Type: name}); l != "" {
			lines = append(lines, l)
		}
	}

	return strings.Join(lines, "\n")
}

// historyLabel is a window's action log on one line for the Windows tab: the
// actions it was sent, oldest first, so a row says where the window has been
// and not only where it is.
func historyLabel(actions []backend.ShortcutAction) string {
	var names []string
	for _, a := range actions {
		// the same name twice in a row says nothing a single one doesn't
		if len(names) > 0 && names[len(names)-1] == a.Type {
			continue
		}
		names = append(names, a.Type)
	}
	return strings.Join(names, " → ")
}

// actionsToLines is the stored actions back as editable lines.
func actionsToLines(actions []backend.ShortcutAction) string {
	lines := make([]string, 0, len(actions))
	for _, a := range actions {
		raw, err := json.Marshal(a)
		if err != nil {
			continue
		}
		lines = append(lines, string(raw))
	}
	return strings.Join(lines, "\n")
}

// bundleSpecJSON serializes what the editor holds into a
// []backend.ShortcutEntry JSON. Each line of a textarea is one action:
// {"type": "profile", "payload": …} — the payload optional, anything JSON.
func bundleSpecJSON(edit *bundleEditState) (string, error) {
	var out []backend.ShortcutEntry
	for _, e := range edit.entries {
		entry := backend.ShortcutEntry{NappID: e.nappID}
		for n, line := range strings.Split(e.ed.Text(), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var action backend.ShortcutAction
			if err := json.Unmarshal([]byte(line), &action); err != nil {
				return "", fmt.Errorf("line %d is not valid JSON: %v", n+1, err)
			}
			action.Type = strings.TrimSpace(action.Type)
			if action.Type == "" {
				return "", fmt.Errorf("line %d has no \"type\"", n+1)
			}
			if strings.ContainsAny(action.Type, " \t") || strings.HasPrefix(action.Type, "+") {
				return "", fmt.Errorf("line %d: %q is not a single action name", n+1, action.Type)
			}
			entry.Actions = append(entry.Actions, action)
		}
		out = append(out, entry)
	}
	data, err := json.Marshal(out)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// saveBundle runs the actual save off the Gio loop: state.json and a
// shortcut file to rewrite are not frame work. An error goes back to the
// Windows tab instead of vanishing into a goroutine.
func saveBundle(name, oldName, spec string, editing *bundleEditState) {
	if err := backend.SaveBundle(oldName, name, spec); err != nil {
		if !errors.Is(err, backend.ErrShortcutNotUpdated) {
			// put the editor back with everything the user typed, so they
			// can fix the problem and try again
			setBundleEdit(editing)
		}
		setBundleErr(err.Error())
		return
	}
	setBundleErr("")
}

// runInBackground does a bundle operation off the Gio loop, showing its error
// on the Windows tab.
func runInBackground(f func() error) {
	go func() {
		if err := f(); err != nil {
			setBundleErr(err.Error())
		} else {
			setBundleErr("")
		}
	}()
}

// previewToken shortens a bundle token for log lines.
func previewToken(token string) string {
	return truncate(token, 120)
}

// clearBundleChecks unchecks every window row, on the Gio loop only.
func clearBundleChecks() {
	for _, cb := range bundleChecks {
		cb.Value = false
	}
}
