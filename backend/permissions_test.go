package backend

import (
	"os"
	"path/filepath"
	"testing"
)

// rulesTest resets the three layers a remembered answer can live in and points
// the state at a file of its own, so a test never writes the real one.
func rulesTest(t *testing.T) {
	t.Helper()
	stateMu.Lock()
	prevRules := state.Rules
	prevPath := statePath
	state.Rules = make(map[string]Rule)
	statePath = filepath.Join(t.TempDir(), "state.json")
	stateMu.Unlock()

	sessionMu.Lock()
	prevSession := sessionRules
	sessionRules = make(map[string]Rule)
	sessionMu.Unlock()

	prevConfigs := installedConfigs
	installedConfigs = nil

	t.Cleanup(func() {
		stateMu.Lock()
		state.Rules, statePath = prevRules, prevPath
		stateMu.Unlock()
		sessionMu.Lock()
		sessionRules = prevSession
		sessionMu.Unlock()
		installedConfigs = prevConfigs
	})
}

func TestNoRuleMeansAsk(t *testing.T) {
	rulesTest(t)
	key := RuleKey{Napp: "npub1abc", Permission: PermPublish}
	if rule, ok := lookupRule(key); ok {
		t.Fatalf("nothing was remembered, got %#v", rule)
	}
}

func TestSessionAnswerIsNotWrittenDown(t *testing.T) {
	rulesTest(t)
	key := RuleKey{Napp: "npub1abc", Permission: PermPublish}

	remember(key, Rule{Decision: DecisionAllow}, ScopeSession)
	if rule, ok := lookupRule(key); !ok || !rule.Decision.granted() {
		t.Fatalf("session allow should answer the question, got %#v %v", rule, ok)
	}

	// the launcher restarting: the session answers are gone with it
	sessionMu.Lock()
	sessionRules = make(map[string]Rule)
	sessionMu.Unlock()
	if rule, ok := lookupRule(key); ok {
		t.Fatalf("a session answer must not survive a restart, got %#v", rule)
	}
	if _, err := os.Stat(statePath); err == nil {
		t.Fatal("a session answer must not reach the state file")
	}
}

func TestAlwaysAnswerIsPersisted(t *testing.T) {
	rulesTest(t)
	key := RuleKey{Napp: "npub1abc", Permission: PermPublish}

	remember(key, Rule{Decision: DecisionDeny}, ScopeAlways)
	if rule, ok := lookupRule(key); !ok || rule.Decision.granted() {
		t.Fatalf("always deny should answer the question, got %#v %v", rule, ok)
	}

	// a restart: the state file is all that is left
	sessionMu.Lock()
	sessionRules = make(map[string]Rule)
	sessionMu.Unlock()
	if rule, ok := lookupRule(key); !ok || rule.Decision.granted() {
		t.Fatalf("a saved answer should survive a restart, got %#v %v", rule, ok)
	}

	// and it is keyed so a second napp asking the same thing is not affected
	other := RuleKey{Napp: "npub1def", Permission: PermPublish}
	if rule, ok := lookupRule(other); ok {
		t.Fatalf("rules belong to one napp, got %#v for %s", rule, other)
	}
}

func TestOnceKeepsNothing(t *testing.T) {
	rulesTest(t)
	key := RuleKey{Napp: "npub1abc", Permission: PermCopyText}

	remember(key, Rule{Decision: DecisionAllow}, ScopeOnce)
	if rule, ok := lookupRule(key); ok {
		t.Fatalf("an answer for one prompt only must be kept nowhere, got %#v", rule)
	}
}

func TestNewestAnswerWins(t *testing.T) {
	rulesTest(t)
	key := RuleKey{Napp: "npub1abc", Permission: PermOpenLink}

	// a saved deny the user then overrules for this session only
	remember(key, Rule{Decision: DecisionDeny}, ScopeAlways)
	remember(key, Rule{Decision: DecisionAllow}, ScopeSession)
	if rule, ok := lookupRule(key); !ok || !rule.Decision.granted() {
		t.Fatalf("the session answer should shadow the saved one, got %#v", rule)
	}

	// the session shadow must not have eaten the saved answer: what the
	// user asked to keep is still there once the launcher restarts
	sessionMu.Lock()
	sessionRules = make(map[string]Rule)
	sessionMu.Unlock()
	if rule, ok := lookupRule(key); !ok || rule.Decision.granted() {
		t.Fatalf("the saved deny should still be there, got %#v", rule)
	}

	// and the other way round: a saved answer replaces the session's, or it
	// would go on shadowing it for the rest of the run
	remember(key, Rule{Decision: DecisionAllow}, ScopeAlways)
	if rule, ok := lookupRule(key); !ok || !rule.Decision.granted() {
		t.Fatalf("always allow should replace the session's allow, got %#v", rule)
	}
	remember(key, Rule{Decision: DecisionDeny}, ScopeAlways)
	if rule, ok := lookupRule(key); !ok || rule.Decision.granted() {
		t.Fatalf("the last word should be the one that counts, got %#v", rule)
	}
}

func TestForgetPermission(t *testing.T) {
	rulesTest(t)
	publish := RuleKey{Napp: "npub1abc", Permission: PermPublish}
	link := RuleKey{Napp: "npub1abc", Permission: PermOpenLink}
	remember(publish, Rule{Decision: DecisionDeny}, ScopeAlways)
	remember(link, Rule{Decision: DecisionAllow}, ScopeSession)
	remember(publish, Rule{Decision: DecisionAllow}, ScopeSession)

	ForgetPermission("npub1abc", PermPublish)
	if rule, ok := lookupRule(publish); ok {
		t.Fatalf("publish should be a prompt again, got %#v", rule)
	}
	if rule, ok := lookupRule(link); !ok || !rule.Decision.granted() {
		t.Fatalf("the other permission should be untouched, got %#v", rule)
	}

	remember(publish, Rule{Decision: DecisionDeny}, ScopeAlways)
	remember(link, Rule{Decision: DecisionDeny}, ScopeAlways)
	ForgetPermission("npub1abc", "")
	if _, ok := lookupRule(publish); ok {
		t.Fatal("everything about this napp should be gone")
	}
	if _, ok := lookupRule(link); ok {
		t.Fatal("everything about this napp should be gone")
	}
}

func TestPermissionRulesList(t *testing.T) {
	rulesTest(t)
	remember(RuleKey{Napp: "npub1def", Permission: PermSign}, Rule{Decision: DecisionAllow}, ScopeAlways)
	remember(RuleKey{Napp: "npub1abc", Permission: PermPublish}, Rule{Decision: DecisionDeny}, ScopeAlways)
	remember(RuleKey{Napp: "npub1abc", Permission: PermSign}, Rule{Decision: DecisionAllow}, ScopeSession)

	rules := PermissionRules()
	want := []PermissionRule{
		{Napp: "npub1abc", NappName: "npub1abc", Permission: PermPublish, Decision: DecisionDeny},
		{Napp: "npub1abc", NappName: "npub1abc", Permission: PermSign, Decision: DecisionAllow},
		{Napp: "npub1def", NappName: "npub1def", Permission: PermSign, Decision: DecisionAllow},
	}
	if len(rules) != len(want) {
		t.Fatalf("got %d rules, want %d: %#v", len(rules), len(want), rules)
	}
	for i := range want {
		if rules[i] != want[i] {
			t.Fatalf("rule %d is %#v, want %#v", i, rules[i], want[i])
		}
	}
}

func TestInstalledConfigurationAnswersFirst(t *testing.T) {
	rulesTest(t)
	key := RuleKey{Napp: "npub1abc", Permission: PermDispatch, Subject: "compose"}
	installedConfigs = []InstalledConfiguration{{
		Napp:  "npub1abc",
		Allow: []ConfigRule{{Permission: PermSign}},
		Deny:  []ConfigRule{{Permission: PermDispatch, Subject: "compose"}},
	}}

	// a configuration speaks about its own napp only
	other := RuleKey{Napp: "npub1def", Permission: PermSign}
	if rule, ok := installedRule(other); ok {
		t.Fatalf("one napp's configuration should say nothing about another, got %#v", rule)
	}
	if rule, ok := installedRule(RuleKey{Napp: "npub1abc", Permission: PermSign}); !ok || !rule.Decision.granted() {
		t.Fatalf("the allow list should match, got %#v %v", rule, ok)
	}

	// and it wins over what the user said about the same thing
	remember(key, Rule{Decision: DecisionAllow}, ScopeAlways)
	if rule, ok := lookupRule(key); !ok || rule.Decision.granted() {
		t.Fatalf("the configuration should answer first, got %#v", rule)
	}
}

func TestRuleKeySurvivesStorage(t *testing.T) {
	rulesTest(t)
	for _, key := range []RuleKey{
		{Napp: "npub1abc", Permission: PermPublish},
		{Napp: "", Permission: PermSign},
		{Napp: "npub1abc", Permission: PermDispatch, Subject: "view:1"},
		{Napp: "npub-with/slashes", Permission: PermDispatch, Subject: "weird|name"},
	} {
		if got := ruleKeyFromID(key.ruleID()); got != key {
			t.Fatalf("%#v came back as %#v", key, got)
		}
	}
}
