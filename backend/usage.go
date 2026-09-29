package backend

import (
	"sort"
	"strings"

	"github.com/puzpuzpuz/xsync/v3"
)

// Which napp the user keeps sending an action to, counted. When a napp fires
// "view" and it lands in a reader, that is a vote for the reader — and next
// time the same action is ambiguous, the picker can put the napp the user has
// been choosing at the top instead of listing handlers by name.
//
// Two things are counted for every dispatch, from two angles: the action on
// its own (whoever sent it, this action goes there) and the napp that sent it
// (from this napp, this action goes there). The second is the sharper signal
// — the same action can reasonably go to different napps depending on who
// asked — and it is what the picker leads with.
//
// The counts live in two layers, read in this order, newest habit first:
//
//   1. this session, in memory, thrown away when the launcher quits
//   2. what was saved in state.json, carried across restarts
//
// A session count shadows a saved one of the same kind, but does not replace
// it: what the user has been doing in the last five minutes is the best
// evidence there is, and the habit it sits on top of is still true tomorrow.

// sessionUsage is the in-memory layer. Kept next to sessionRules in spirit —
// an answer for this run only, never written down.
var sessionUsage = xsync.NewMapOf[string, int]()

// usageKey is what a count is filed under: the napp that sent the action ("" for
// the launcher's own), the action, and the napp that handled it.
//
// A count with no napp is the action-wide one: "this action goes there,
// whoever asked". That is what a launcher shortcut or a shared link produces,
// since there is no napp behind it.
type usageKey struct {
	Napp   string `json:"napp,omitempty"`
	Action string `json:"action"`
	Target string `json:"target"`
}

// usageID is the key as state.json files it, same shape as RuleKey.ruleID:
// napp ids and action names are whatever the network said, so the parts are
// joined with a separator neither can hold.
func (k usageKey) usageID() string {
	return strings.Join([]string{k.Napp, k.Action, k.Target}, "\x1f")
}

func usageKeyFromID(id string) usageKey {
	parts := strings.Split(id, "\x1f")
	for len(parts) < 3 {
		parts = append(parts, "")
	}
	return usageKey{Napp: parts[0], Action: parts[1], Target: parts[2]}
}

// recordActionUse counts a dispatch: napp A fired action X and it was handled
// by napp B. Both the napp-specific and the action-wide count go up, in both
// layers.
//
// This is a plain tally of where actions went, not a vote on what should
// happen next: it never dispatches anything by itself, it only reorders what
// the user is already being asked.
func recordActionUse(caller, action, target string) {
	if action == "" || target == "" {
		return
	}

	keys := []usageKey{
		{Napp: caller, Action: action, Target: target},
		{Action: action, Target: target},
	}
	// a launcher-originated dispatch has no caller, and the two keys are then
	// the same count: don't let it count twice
	if caller == "" {
		keys = keys[1:]
	}

	for _, k := range keys {
		sessionUsage.Compute(k.usageID(), func(n int, _ bool) (int, bool) { return n + 1, false })
	}

	stateMu.Lock()
	if state.ActionUsage == nil {
		state.ActionUsage = make(map[string]int)
	}
	for _, k := range keys {
		state.ActionUsage[k.usageID()]++
	}
	saveState()
	stateMu.Unlock()
}

// usageTier is how good the evidence is that a napp is the one the user wants
// for an action. The tiers are listed weakest first and the numbers run that
// way round, so tierNone is the zero value — a napp nothing was ever counted
// for is the default, not a special case — and sorting by tier puts the
// strongest evidence on top.
type usageTier int

const (
	// tierNone is a napp nothing has been recorded for.
	tierNone usageTier = iota

	// tierStoredAction: "this action goes there", from a previous run.
	tierStoredAction

	// tierSessionAction: "this action goes there", formed this run.
	tierSessionAction

	// tierStoredCaller: "from this napp, this action goes there", saved.
	tierStoredCaller

	// tierSessionCaller: "from this napp, this action goes there", this run.
	tierSessionCaller
)

// suggested is the evidence there is for one napp handling an action for one
// caller: which tier vouches for it, and how many times it was counted there.
type suggested struct {
	Tier  usageTier
	Count int
}

// rankFor is what the counts say about `target` handling `action` when
// `caller` sent it. The tiers are consulted in order and the first with a
// count wins outright — a single time this run beats fifty times ever, because
// recency is the whole point of having two layers.
func rankFor(caller, action, target string) suggested {
	if action == "" || target == "" {
		return suggested{}
	}
	specific := usageKey{Napp: caller, Action: action, Target: target}.usageID()
	general := usageKey{Action: action, Target: target}.usageID()

	if n, ok := sessionUsageCount(specific); ok {
		return suggested{Tier: tierSessionCaller, Count: n}
	}
	if n, ok := storedUsageCount(specific); ok {
		return suggested{Tier: tierStoredCaller, Count: n}
	}
	if n, ok := sessionUsageCount(general); ok {
		return suggested{Tier: tierSessionAction, Count: n}
	}
	if n, ok := storedUsageCount(general); ok {
		return suggested{Tier: tierStoredAction, Count: n}
	}
	return suggested{}
}

func sessionUsageCount(id string) (int, bool) {
	return sessionUsage.Load(id)
}

func storedUsageCount(id string) (int, bool) {
	stateMu.Lock()
	defer stateMu.Unlock()
	n, ok := state.ActionUsage[id]
	return n, ok
}

// sortHandlerOptions orders a picker's options by what the counts say, best
// first. Options nothing was ever recorded for keep the order they came in
// with, so the rest of the list is exactly as it was: open windows above
// napps that would have to be launched, dev napps last.
//
// The sort is stable, so napps tied on a tier stay in the incoming order —
// which is what keeps "this napp already has a window open" ahead of "this
// napp has to be launched" for the same napp.
func sortHandlerOptions(options []PromptOption, caller, action string) {
	ranks := make(map[string]suggested, len(options))
	rankOf := func(nappID string) suggested {
		if r, ok := ranks[nappID]; ok {
			return r
		}
		r := rankFor(caller, action, nappID)
		ranks[nappID] = r
		return r
	}
	for i := range options {
		options[i].Suggested = rankOf(options[i].NappID).Tier != tierNone
		options[i].Uses = rankOf(options[i].NappID).Count
	}

	sort.SliceStable(options, func(i, j int) bool {
		return rankOf(options[i].NappID).Tier > rankOf(options[j].NappID).Tier
	})
}

// forgetActionUsage drops every count naming a napp, from both layers: a napp
// that isn't installed can't be the answer to anything, and reinstalling it
// later shouldn't inherit a history the user never formed with the copy they
// have now.
func forgetActionUsage(napp string) {
	for id := range sessionUsage.Range {
		k := usageKeyFromID(id)
		if k.Napp == napp || k.Target == napp {
			sessionUsage.Delete(id)
		}
	}

	stateMu.Lock()
	changed := false
	for id := range state.ActionUsage {
		k := usageKeyFromID(id)
		if k.Napp == napp || k.Target == napp {
			delete(state.ActionUsage, id)
			changed = true
		}
	}
	if changed {
		saveState()
	}
	stateMu.Unlock()
}
