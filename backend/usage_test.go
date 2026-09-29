package backend

import (
	"path/filepath"
	"reflect"
	"testing"
)

// Which napp gets picked for an action, counted. See usage.go.

// usageTest resets both layers of the counts and points the state at a file of
// its own, the way rulesTest does for rules.
func usageTest(t *testing.T) {
	t.Helper()
	stateMu.Lock()
	prevUsage := state.ActionUsage
	prevPath := statePath
	state.ActionUsage = make(map[string]int)
	statePath = filepath.Join(t.TempDir(), "state.json")
	stateMu.Unlock()

	usageMu.Lock()
	prevSession := sessionUsage
	sessionUsage = make(map[string]int)
	usageMu.Unlock()

	t.Cleanup(func() {
		stateMu.Lock()
		state.ActionUsage, statePath = prevUsage, prevPath
		stateMu.Unlock()
		usageMu.Lock()
		sessionUsage = prevSession
		usageMu.Unlock()
	})
}

// A dispatch from one napp into another counts twice: once for that napp
// asking, once for the action on its own. A launcher-originated dispatch has
// no napp behind it, so it only counts once.
func TestUsageCountsBothAngles(t *testing.T) {
	usageTest(t)

	recordActionUse("npub1aaa", "view:1", "npub1bbb")
	recordActionUse("npub1aaa", "view:1", "npub1bbb")
	recordActionUse("", "open", "npub1ccc")

	specific := usageKey{Napp: "npub1aaa", Action: "view:1", Target: "npub1bbb"}
	if n := sessionUsage[specific.usageID()]; n != 2 {
		t.Fatalf("napp-specific count is %d", n)
	}
	if n := state.ActionUsage[specific.usageID()]; n != 2 {
		t.Fatalf("saved napp-specific count is %d", n)
	}

	general := usageKey{Action: "view:1", Target: "npub1bbb"}
	if n := sessionUsage[general.usageID()]; n != 2 {
		t.Fatalf("action-wide count is %d", n)
	}

	// "open" came from the launcher, so the napp-specific and the
	// action-wide key are the same one and must not be counted twice
	launcher := usageKey{Action: "open", Target: "npub1ccc"}
	if n := state.ActionUsage[launcher.usageID()]; n != 1 {
		t.Fatalf("launcher dispatch counted %d", n)
	}
}

// The four tiers, in the order they win: this run and this caller, then a
// saved habit for this caller, then this run for anyone, then a saved habit
// for anyone. A habit formed now beats one carried over, however old.
func TestUsageTierOrder(t *testing.T) {
	usageTest(t)

	stateMu.Lock()
	state.ActionUsage[usageKey{Napp: "npub1aaa", Action: "a", Target: "t1"}.usageID()] = 50
	state.ActionUsage[usageKey{Action: "a", Target: "t2"}.usageID()] = 50
	stateMu.Unlock()
	sessionUsage[usageKey{Action: "a", Target: "t3"}.usageID()] = 1
	sessionUsage[usageKey{Napp: "npub1aaa", Action: "a", Target: "t4"}.usageID()] = 1

	want := map[string]usageTier{
		"t4": tierSessionCaller,
		"t1": tierStoredCaller,
		"t3": tierSessionAction,
		"t2": tierStoredAction,
		"t5": tierNone,
	}
	for target, tier := range want {
		if got := rankFor("npub1aaa", "a", target); got.Tier != tier {
			t.Fatalf("%s ranked %d want %d", target, got.Tier, tier)
		}
	}
}

// A count about one caller must not be offered as the reason to pick a napp
// for a different caller, or for a different action. The action-wide count is
// the one exception: it was recorded without a caller precisely so it applies
// to whoever asks.
func TestUsageDoesNotLeakBetweenCallers(t *testing.T) {
	usageTest(t)

	recordActionUse("npub1aaa", "view:1", "npub1bbb")

	// the caller-specific count is not this caller's to spend, so the best
	// this other caller can be shown is the action-wide one (recorded in this
	// run, hence the session action tier rather than the caller one)
	if got := rankFor("npub1zzz", "view:1", "npub1bbb"); got.Tier != tierSessionAction {
		t.Fatalf("another caller got %d", got.Tier)
	}
	if got := rankFor("npub1aaa", "view:9", "npub1bbb"); got.Tier != tierNone {
		t.Fatalf("another action got %d", got.Tier)
	}
	if got := rankFor("npub1zzz", "view:9", "npub1bbb"); got.Tier != tierNone {
		t.Fatalf("another caller and action got %d", got.Tier)
	}
}

// The picker puts the habitual handlers first and leaves the rest of the list
// exactly as it was — open windows above napps to launch, dev napps last.
func TestHandlerOptionsOrderByHabit(t *testing.T) {
	usageTest(t)

	recordActionUse("napp1", "compose", "habit")
	recordActionUse("", "compose", "actionwide")

	options := []PromptOption{
		{Label: "plain", NappID: "plain"},
		{Label: "habit window", NappID: "habit", Instance: "i1"},
		{Label: "actionwide", NappID: "actionwide"},
		{Label: "habit closed", NappID: "habit"},
	}
	sortHandlerOptions(options, "napp1", "compose")

	want := []string{"habit window", "habit closed", "actionwide", "plain"}
	got := make([]string, 0, len(options))
	for _, o := range options {
		got = append(got, o.Label)
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("got %v want %v", got, want)
	}

	// the ones the counts speak for are marked, and nothing else is
	marked := map[string]bool{}
	for _, o := range options {
		marked[o.Label] = o.Suggested
	}
	for label, is := range map[string]bool{
		"habit window": true, "habit closed": true, "actionwide": true, "plain": false,
	} {
		if marked[label] != is {
			t.Fatalf("%q suggested=%v", label, marked[label])
		}
	}
	if n := options[0].Uses; n != 1 {
		t.Fatalf("the top option reports %d uses", n)
	}
}

// An open window and the napp behind it are two options for one napp, so both
// are the habit: the user should not be shown the napp they always pick with
// the window they already have sitting below a stranger.
func TestBothSidesOfANappAreSuggested(t *testing.T) {
	usageTest(t)

	recordActionUse("napp1", "compose", "habit")

	options := []PromptOption{
		{Label: "stranger", NappID: "stranger"},
		{Label: "habit closed", NappID: "habit"},
		{Label: "habit window", NappID: "habit", Instance: "i9"},
	}
	sortHandlerOptions(options, "napp1", "compose")

	for _, o := range options {
		if o.NappID == "habit" && !o.Suggested {
			t.Fatalf("%q is the same napp but was not marked", o.Label)
		}
	}
}

// Nothing to count means the picker opens in the order it always did.
func TestNoUsageLeavesOrderAlone(t *testing.T) {
	usageTest(t)

	options := []PromptOption{
		{Label: "a", NappID: "a"},
		{Label: "b", NappID: "b"},
		{Label: "c", NappID: "c"},
	}
	sortHandlerOptions(options, "napp1", "compose")

	for i, o := range options {
		if o.Suggested {
			t.Fatalf("%q is suggested with nothing recorded", o.Label)
		}
		if want := string(rune('a' + i)); o.NappID != want {
			t.Fatalf("position %d holds %q", i, o.NappID)
		}
	}
}

// Uninstalling a napp takes its habits with it: it can't be the answer to
// anything, and whatever is installed under that id next shouldn't inherit a
// history the user never formed with it.
func TestForgetActionUsageDropsBothSides(t *testing.T) {
	usageTest(t)

	recordActionUse("gone", "compose", "kept")
	recordActionUse("kept", "view:1", "gone")
	recordActionUse("kept", "view:1", "kept")

	forgetActionUsage("gone")

	for id, n := range sessionUsage {
		if usageKeyFromID(id).Napp == "gone" || usageKeyFromID(id).Target == "gone" {
			t.Fatalf("session kept a count naming the gone napp: %s = %d", id, n)
		}
	}
	for id, n := range state.ActionUsage {
		if usageKeyFromID(id).Napp == "gone" || usageKeyFromID(id).Target == "gone" {
			t.Fatalf("saved state kept a count naming the gone napp: %s = %d", id, n)
		}
	}
	if n := sessionUsage[usageKey{Action: "view:1", Target: "kept"}.usageID()]; n != 1 {
		t.Fatalf("the surviving count is %d", n)
	}
}

// Napp ids and action names are whatever the network said, so a key that
// joined them readably could be forged. The separator can't occur in a napp id
// (pubkey + "~" + the d tag's kind) and a name carrying one is not a thing any
// napp dispatches, so the round trip holds for every real key.
func TestUsageIDRoundTripsRealKeys(t *testing.T) {
	for _, k := range []usageKey{
		{Action: "view:1", Target: "npub1bbb"},
		{Napp: "npub1aaa", Action: "compose", Target: "npub1bbb"},
		{Napp: "abcd1234~long note napp", Action: "view:30023", Target: "efgh5678~a b"},
	} {
		if got := usageKeyFromID(k.usageID()); got != k {
			t.Fatalf("%#v came back as %#v", k, got)
		}
	}
}
