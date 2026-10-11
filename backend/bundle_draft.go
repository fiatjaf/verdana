package backend

import (
	"encoding/json"
	"fmt"
	"strings"
)

// The bundle editor, as text: whatever UI draws it (the desktop's Gio
// overlay, Android's editor screen) shows one textarea per napp of the
// bundle, one action per line as a {"type": …, "payload": …} JSON object,
// and hands the lines back here to be read into a bundle.

// BundleDraftEntry is one napp of a bundle in the editor: Lines are its
// actions, one JSON object per line.
type BundleDraftEntry struct {
	NappID string `json:"nappId"`
	Label  string `json:"label"`
	Lines  string `json:"lines"`
}

// maxSuggestedActions is how much of what a window was sent the editor
// suggests before falling back on the napp's own list: enough to rebuild a
// bundle of what the windows were just doing, short enough to read and edit.
const maxSuggestedActions = 8

// DraftFromWindows starts a new bundle from the windows picked on the
// Windows screen (open or closed earlier this run), in listed order, merged
// into one entry per napp (the same napp may have two windows picked). Each
// entry starts with the actions those windows were actually sent, then
// whatever else the napp declares handling.
func DraftFromWindows(instances []string) []BundleDraftEntry {
	picked := make(map[string]bool, len(instances))
	for _, inst := range instances {
		picked[inst] = true
	}

	var entries []ShortcutEntry
	byID := make(map[string]int)
	for _, w := range ManagedWindows() {
		if !picked[w.Instance] {
			continue
		}
		if i, ok := byID[w.NappID]; ok {
			// actions are per napp: a second window of the same napp only
			// adds what its own window was sent
			entries[i].Actions = append(entries[i].Actions, w.History...)
			continue
		}
		byID[w.NappID] = len(entries)
		entries = append(entries, ShortcutEntry{NappID: w.NappID, Actions: w.History})
	}

	draft := make([]BundleDraftEntry, 0, len(entries))
	for _, e := range entries {
		napp, ok := InstalledNapp(e.NappID)
		if !ok {
			continue
		}
		draft = append(draft, BundleDraftEntry{
			NappID: e.NappID,
			Label:  napp.Label(),
			Lines:  suggestionLines(e.Actions, napp.Actions),
		})
	}
	return draft
}

// DraftFromBundle is a stored bundle back in the editor, every action an
// editable line.
func DraftFromBundle(entries []ShortcutEntry) []BundleDraftEntry {
	draft := make([]BundleDraftEntry, 0, len(entries))
	for _, e := range entries {
		label := e.NappID
		if napp, ok := InstalledNapp(e.NappID); ok {
			label = napp.Label()
		}
		draft = append(draft, BundleDraftEntry{NappID: e.NappID, Label: label, Lines: actionsToLines(e.Actions)})
	}
	return draft
}

// DraftOfStoredBundle is DraftFromBundle for the stored bundle with that
// name, nil when there is none.
func DraftOfStoredBundle(name string) []BundleDraftEntry {
	b, ok := bundleByName(name)
	if !ok {
		return nil
	}
	return DraftFromBundle(b.Entries)
}

// ParseBundleDraft reads the editor's lines back into bundle entries,
// saying which line of which napp is wrong when one doesn't read.
func ParseBundleDraft(draft []BundleDraftEntry) ([]ShortcutEntry, error) {
	out := make([]ShortcutEntry, 0, len(draft))
	for _, e := range draft {
		entry := ShortcutEntry{NappID: e.NappID}
		for n, line := range strings.Split(e.Lines, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var action ShortcutAction
			if err := json.Unmarshal([]byte(line), &action); err != nil {
				return nil, fmt.Errorf("%s, line %d is not valid JSON: %v", e.Label, n+1, err)
			}
			action.Type = strings.TrimSpace(action.Type)
			if action.Type == "" {
				return nil, fmt.Errorf("%s, line %d has no \"type\"", e.Label, n+1)
			}
			if err := validActionName(action.Type); err != nil {
				return nil, fmt.Errorf("%s, line %d: %v", e.Label, n+1, err)
			}
			entry.Actions = append(entry.Actions, action)
		}
		out = append(out, entry)
	}
	return out, nil
}

// SaveBundleDraft is SaveBundle from what the editor holds.
func SaveBundleDraft(oldName, name string, draft []BundleDraftEntry) error {
	entries, err := ParseBundleDraft(draft)
	if err != nil {
		return err
	}
	data, err := json.Marshal(entries)
	if err != nil {
		return err
	}
	return SaveBundle(oldName, name, string(data))
}

// suggestionLines is what a fresh bundle's textarea starts with: the actions
// the picked windows were actually sent, with the payloads they carried,
// most recent first and without repeats, then whatever else the napp declares
// handling and none of that already covers.
func suggestionLines(seen []ShortcutAction, declared []string) string {
	line := func(a ShortcutAction) string {
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
		if l := line(ShortcutAction{Type: name}); l != "" {
			lines = append(lines, l)
		}
	}

	return strings.Join(lines, "\n")
}

// actionsToLines is the stored actions back as editable lines.
func actionsToLines(actions []ShortcutAction) string {
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
