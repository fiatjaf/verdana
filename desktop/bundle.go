package main

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"verdana/backend"

	"gioui.org/layout"
	"gioui.org/widget"
)

// The bundle editor: everything the Gio window needs to turn the checkboxes
// of the Sessions tab into a stored bundle (with one call to
// backend.SaveBundle behind it).

// pickedBundleWindows is the checked windows, in listed order.
func pickedBundleWindows(st backend.State) []string {
	var picked []string
	for _, w := range st.ManagedWindows {
		if cb, ok := bundleChecks[w.Instance]; ok && cb.Value {
			picked = append(picked, w.Instance)
		}
	}
	return picked
}

// newBundleEditState builds the editor overlay from a draft: a fresh one
// from the checked windows (backend.DraftFromWindows) or a stored bundle's.
func newBundleEditState(name string, draft []backend.BundleDraftEntry) *bundleEditState {
	out := &bundleEditState{name: name}
	out.nameEd.SingleLine = true
	out.nameEd.SetText(name)
	for _, e := range draft {
		var actions []backend.ShortcutAction
		if parsed, err := backend.ParseBundleDraft([]backend.BundleDraftEntry{e}); err == nil {
			actions = parsed[0].Actions
		}
		out.entries = append(out.entries, bundleEditEntry{
			nappID:     e.NappID,
			label:      e.Label,
			actions:    actions,
			removeBtns: make([]widget.Clickable, len(actions)),
		})
	}
	return out
}

// removeClickedActions drops the actions whose remove button was clicked
// this frame, on the Gio loop only.
func removeClickedActions(gtx layout.Context, edit *bundleEditState) {
	for i := range edit.entries {
		e := &edit.entries[i]
		for j := 0; j < len(e.actions); j++ {
			if e.removeBtns[j].Clicked(gtx) {
				e.actions = slices.Delete(e.actions, j, j+1)
				e.removeBtns = slices.Delete(e.removeBtns, j, j+1)
				j--
			}
		}
	}
}

// editBundleEditState builds the overlay pre-filled with a stored bundle, so
// the name comes out editable and every action removable.
func editBundleEditState(sc backend.BundleInfo) *bundleEditState {
	return newBundleEditState(sc.Name, backend.DraftFromBundle(sc.Entries))
}

// bundleDraft is what the editor holds, for backend.SaveBundleDraft.
func bundleDraft(edit *bundleEditState) []backend.BundleDraftEntry {
	draft := make([]backend.BundleDraftEntry, 0, len(edit.entries))
	for _, e := range edit.entries {
		lines := make([]string, 0, len(e.actions))
		for _, a := range e.actions {
			if raw, err := json.Marshal(a); err == nil {
				lines = append(lines, string(raw))
			}
		}
		draft = append(draft, backend.BundleDraftEntry{NappID: e.nappID, Label: e.label, Lines: strings.Join(lines, "\n")})
	}
	return draft
}

// saveBundle runs the actual save off the Gio loop: state.json and a
// shortcut file to rewrite are not frame work. An error goes back to the
// Sessions tab instead of vanishing into a goroutine.
func saveBundle(name, oldName string, draft []backend.BundleDraftEntry, editing *bundleEditState) {
	if err := backend.SaveBundleDraft(oldName, name, draft); err != nil {
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
// on the Sessions tab.
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
