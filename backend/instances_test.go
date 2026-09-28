package backend

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
)

// The action log of a window: what a reopen replays, what the Windows tab
// lists and what the bundle editor suggests.

func testInstance(t *testing.T, id, nappID string) *Instance {
	ci := &Instance{
		instance: id,
		napp:     Napp{ID: nappID, Name: nappID},
		gone:     make(chan struct{}),
	}
	instancesMu.Lock()
	instances = append(instances, ci)
	instancesMu.Unlock()
	windowsMu.Lock()
	windows[id] = windowRecord{Instance: id, NappID: nappID}
	windowsMu.Unlock()
	t.Cleanup(func() {
		instancesMu.Lock()
		for i, c := range instances {
			if c == ci {
				instances = append(instances[:i], instances[i+1:]...)
				break
			}
		}
		instancesMu.Unlock()
		windowsMu.Lock()
		delete(windows, id)
		windowsMu.Unlock()
	})
	return ci
}

func actionNames(actions []ShortcutAction) []string {
	out := make([]string, 0, len(actions))
	for _, a := range actions {
		out = append(out, a.Type)
	}
	return out
}

// An action we dispatched and one the napp pushed for itself both belong to
// the window, in the order they happened.
func TestActionLogKeepsDispatchedAndSelfEmitted(t *testing.T) {
	ci := testInstance(t, "i1", "npub1abc")

	recordAction(ci, &actionRequest{name: "view:1", payload: json.RawMessage(`"nostr1abc"`)}, false)
	// what a napp calls on itself with an explicit instance lands here too
	ci.setActionState(&actionRequest{name: "compose"}, false)

	got := windowHistory("i1")
	want := []string{"view:1", "compose"}
	if !reflect.DeepEqual(want, actionNames(got)) {
		t.Fatalf("got %v want %v", actionNames(got), want)
	}
	if string(got[0].Payload) != `"nostr1abc"` {
		t.Fatalf("lost the payload: %s", got[0].Payload)
	}
	// the window is on what it last showed
	if last := ci.lastAction.Load(); last == nil || last.name != "compose" {
		t.Fatalf("last action is not the self-emitted one: %#v", last)
	}
}

// A napp that overwrote the entry it was on (replaceState, or going back)
// replaces the tail of the log instead of growing it.
func TestActionLogReplacesOnReplaceState(t *testing.T) {
	ci := testInstance(t, "i2", "npub1abc")

	ci.setActionState(&actionRequest{name: "view:1"}, false)
	ci.setActionState(&actionRequest{name: "view:1", payload: json.RawMessage(`"a"`)}, false)
	ci.setActionState(&actionRequest{name: "profile", payload: json.RawMessage(`"b"`)}, true)

	got := windowHistory("i2")
	if want := []string{"view:1", "profile"}; !reflect.DeepEqual(want, actionNames(got)) {
		t.Fatalf("got %v want %v", actionNames(got), want)
	}
	if string(got[1].Payload) != `"b"` {
		t.Fatalf("replace did not take the new payload: %s", got[1].Payload)
	}
}

// A window navigated a thousand times does not hold a thousand entries, and
// its last one is still the action it is on.
func TestActionLogIsBounded(t *testing.T) {
	ci := testInstance(t, "i3", "npub1abc")

	for i := 0; i < maxWindowActions*2; i++ {
		recordAction(ci, &actionRequest{name: fmt.Sprintf("a%d", i)}, false)
	}
	got := windowHistory("i3")
	if len(got) != maxWindowActions {
		t.Fatalf("log grew to %d", len(got))
	}
	if got[0].Type != fmt.Sprintf("a%d", maxWindowActions) {
		t.Fatalf("kept the wrong end: %s", got[0].Type)
	}
	if last := got[len(got)-1].Type; last != fmt.Sprintf("a%d", maxWindowActions*2-1) {
		t.Fatalf("last action is %s", last)
	}
}

// Replaying a reopen must not double the log it is replaying.
func TestActionLogSkipsReplaying(t *testing.T) {
	ci := testInstance(t, "i4", "npub1abc")
	ci.replaying.Store(true)
	recordAction(ci, &actionRequest{name: "view:1"}, false)
	ci.setActionState(&actionRequest{name: "compose"}, false)
	ci.replaying.Store(false)

	if got := windowHistory("i4"); len(got) != 0 {
		t.Fatalf("a replay recorded %v", actionNames(got))
	}
}

// A null payload is no payload, so it doesn't clutter the editor's lines.
func TestActionLogDropsNullPayload(t *testing.T) {
	ci := testInstance(t, "i5", "npub1abc")
	recordAction(ci, &actionRequest{name: "open", payload: json.RawMessage("null")}, false)

	got := windowHistory("i5")
	if len(got) != 1 || got[0].Payload != nil {
		t.Fatalf("got %#v", got)
	}
}

// The Windows tab listing carries the log; the window switcher, which only
// wants where each window is, does not.
func TestManagedWindowsCarryTheLog(t *testing.T) {
	testInstance(t, "i6", "npub1abc")

	for _, w := range OpenWindows() {
		if w.Instance == "i6" && w.History != nil {
			t.Fatalf("the switcher got a history: %#v", w.History)
		}
	}
	ci := lookupInstance("i6")
	// what dispatching into a window does: log it and make it where it is
	ci.setActionState(&actionRequest{name: "view:1"}, false)
	ci.setActionState(&actionRequest{name: "compose"}, false)

	var found bool
	for _, w := range ManagedWindows() {
		if w.Instance != "i6" {
			continue
		}
		found = true
		if want := []string{"view:1", "compose"}; !reflect.DeepEqual(want, actionNames(w.History)) {
			t.Fatalf("got %v want %v", actionNames(w.History), want)
		}
		if w.Action != "compose" {
			t.Fatalf("the window is on %q", w.Action)
		}
	}
	if !found {
		t.Fatalf("the open window is not listed: %#v", ManagedWindows())
	}
}
