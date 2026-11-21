package backend

import (
	"context"
	"os"
	"testing"
	"time"

	"fiatjaf.com/nostr"
)

func TestIsAllowedRelayURL(t *testing.T) {
	ctx := context.Background()
	for _, bad := range []string{
		"ws://127.0.0.1:7777",
		"wss://localhost/relay",
		"wss://LOCALHOST./relay",
		"wss://sub.localhost/relay",
		"http://169.254.169.254/latest/meta-data/",
		"ws://[::1]:8080",
		"ws://[64:ff9b::127.0.0.1]/",
		"ws://0.0.0.0:8080",
		"file:///etc/passwd",
		"not a url",
	} {
		if err := isAllowedRelayURL(ctx, bad); err == nil {
			t.Errorf("isAllowedRelayURL(%q) should have been rejected", bad)
		}
	}
	for _, good := range []string{
		"wss://8.8.8.8",
		"https://1.1.1.1/info",
	} {
		if err := isAllowedRelayURL(ctx, good); err != nil {
			t.Errorf("isAllowedRelayURL(%q) = %v, want nil", good, err)
		}
	}
}

func TestHighImpactSignKindNeverAutoApproved(t *testing.T) {
	for _, k := range []nostr.Kind{0, 3, 10002, 10063, 22242, 24133, 27235, 35130, 35131, 35133} {
		if !isHighImpactSignKind(k) {
			t.Errorf("kind %d should be high-impact", k)
		}
	}
	if isHighImpactSignKind(1) {
		t.Error("kind 1 (plain note) should not be high-impact")
	}

	ci := &Instance{instance: "inst-hi", napp: Napp{ID: "pub~social", Name: "Social"}}
	key := RuleKey{Napp: ci.napp.ID, Permission: PermSign}
	setSessionRule(key, Rule{Decision: DecisionAllow})
	defer clearSessionRule(key)

	// Even with a blanket PermSign allow rule, kind:3 (follow list) must
	// still enqueue a one-shot prompt (Remember == false).
	done := make(chan bool, 1)
	go func() {
		done <- askSignApproval(ci, nostr.Event{Kind: 3})
	}()

	var p *Prompt
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if cur := CurrentPrompt(); cur != nil && cur.Instance == ci.instance {
			p = cur
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if p == nil {
		t.Fatal("high-impact kind:3 was auto-approved instead of prompting")
	}
	if p.Remember {
		t.Error("high-impact sign prompt must have Remember == false")
	}
	AnswerPrompt(p.ID, Answer{OK: false})
	if <-done {
		t.Error("denied high-impact sign prompt returned true")
	}
}

func TestUninstallCleansPermissionsStorageAndWindows(t *testing.T) {
	oldDir, oldPath, oldState, oldHost := dataDir, statePath, state, host
	dir := t.TempDir()
	dataDir = dir
	statePath = dir + "/state.json"
	state = AppState{
		InstalledNapps: map[string]Napp{"pub~gone": {ID: "pub~gone"}},
		Rules:          map[string]Rule{},
	}
	host = noopHost{}
	t.Cleanup(func() {
		dataDir, statePath, state, host = oldDir, oldPath, oldState, oldHost
	})

	id := "pub~gone"
	key := RuleKey{Napp: id, Permission: PermSign}
	storeRule(key, Rule{Decision: DecisionAllow})
	setSessionRule(key, Rule{Decision: DecisionAllow})
	if err := storageSet(id, "k", "v"); err != nil {
		t.Fatal(err)
	}
	windows.Store("win-gone", windowRecord{Instance: "win-gone", NappID: id})
	defer windows.Delete("win-gone")

	Uninstall(id)

	if _, ok := lookupRule(key); ok {
		t.Error("Uninstall left permission rules behind")
	}
	if _, err := os.Stat(storageFileFor(id)); !os.IsNotExist(err) {
		t.Errorf("Uninstall left storage file behind: %v", err)
	}
	if _, ok := windows.Load("win-gone"); ok {
		t.Error("Uninstall left window record behind")
	}
}

func TestExplicitInstanceDispatchGuards(t *testing.T) {
	picker := &Instance{instance: "pick1", picker: true, gone: make(chan struct{})}
	target := &Instance{instance: "tgt1", napp: Napp{ID: "pub~tgt", Actions: []string{"view:1"}}, gone: make(chan struct{})}
	caller := &Instance{instance: "call1", napp: Napp{ID: "pub~caller"}, gone: make(chan struct{})}
	registerInstance(picker)
	registerInstance(target)
	t.Cleanup(func() {
		instancesMu.Lock()
		out := instances[:0]
		for _, c := range instances {
			if c != picker && c != target {
				out = append(out, c)
			}
		}
		instances = out
		instancesMu.Unlock()
	})

	if _, err := runNappAction(context.Background(), caller, "view:1", nil, actionOptions{Instance: "pick1"}); err == nil {
		t.Error("dispatch to picker instance should be rejected")
	}
	if _, err := runNappAction(context.Background(), caller, "compose", nil, actionOptions{Instance: "tgt1"}); err == nil {
		t.Error("dispatch of undeclared action to explicit instance should be rejected")
	}
	key := RuleKey{Napp: caller.napp.ID, Permission: PermDispatch, Subject: "view:1"}
	setSessionRule(key, Rule{Decision: DecisionDeny})
	defer clearSessionRule(key)
	if _, err := runNappAction(context.Background(), caller, "view:1", nil, actionOptions{Instance: "tgt1"}); err == nil {
		t.Error("dispatch denied by PermDispatch rule should be rejected even with opts.Instance")
	}
}

func TestReadAuthDefaultsOffAndBlossomFallbackOrder(t *testing.T) {
	oldState := state
	state = AppState{}
	t.Cleanup(func() { state = oldState })

	if ReadAuth() {
		t.Fatal("ReadAuth() should default to false (opt-in)")
	}

	n := Napp{
		ID:      "pub~app",
		Servers: []string{"https://blossom.author.example"},
	}
	servers := n.BlossomServers(context.Background())
	if len(servers) < 2 || servers[0] != "https://blossom.author.example" {
		t.Fatalf("author/napp Blossom server should be tried first, got %v", servers)
	}
	if servers[1] != "https://relay.nostrapps.com" {
		t.Fatalf("relay.nostrapps.com should be fallback after napp servers, got %v", servers)
	}
}

func TestClearAllSessionRules(t *testing.T) {
	key := RuleKey{Napp: "pub~sess", Permission: PermCopyText}
	setSessionRule(key, Rule{Decision: DecisionAllow})
	clearAllSessionRules()
	if _, ok := sessionRule(key); ok {
		t.Fatal("clearAllSessionRules did not clear session rule")
	}
}
