package backend

import (
	"sort"
	"strings"

	"github.com/puzpuzpuz/xsync/v3"
)

// The rules a napp runs under: what it may do without asking, and what it may
// not. Every sensitive rpc — signing, encrypting, publishing, opening a link,
// saving a file, copying to the clipboard — asks lookupRule first and only
// becomes a prompt when nothing there has an opinion.
//
// There are three layers, read in this order: the installed configuration
// (InstalledConfiguration, at the bottom of this file — nothing loads one
// yet), the answers given for this session, and the ones saved in state.json.
// A question none of them answers is a prompt.

// Permission is one of the things a napp can be allowed to do. It is what a
// remembered answer belongs to: "always allow" is about the napp and the
// permission, not about the prompt that happened to come up.
type Permission string

const (
	PermSign     Permission = "sign"
	PermEncrypt  Permission = "encrypt"
	PermDecrypt  Permission = "decrypt"
	PermPublish  Permission = "publish"
	PermOpenLink Permission = "open_link"
	PermSaveFile Permission = "save_file"
	PermCopyText Permission = "copy_text"
	// PermUpload lets a napplet publish bytes to the signed-in user's
	// Blossom servers (NAP-UPLOAD). It is separate from publishing a Nostr
	// event: the identity linkage and public network egress deserve their own
	// remembered decision.
	PermUpload Permission = "upload"
	// PermFetch is a napplet having the launcher download from the web for
	// it (NAP-RESOURCE): napplets have no network of their own.
	PermFetch Permission = "fetch"
	// PermMedia is a napplet having the launcher play media in the system's
	// player for it (NAP-MEDIA shell-owned sessions).
	PermMedia Permission = "media"

	// PermDispatch is the one permission no napp asks for out loud: it is
	// which napp should handle an action, a question only a rule can settle
	// (a user who said "always open these with that one", or an installed
	// configuration saying the same). No prompt is remembered under it
	// today, but everything is keyed so that one can be.
	PermDispatch Permission = "dispatch"
)

// Decision is what the rules have to say about a key: yes, no, or nothing at
// all — which is the only answer that has the user asked.
type Decision string

const (
	DecisionAsk   Decision = "ask"
	DecisionAllow Decision = "allow"
	DecisionDeny  Decision = "deny"
)

// granted says whether a decision lets the napp go ahead. Anything that is not
// an allow — including no decision at all — has the user asked.
func (d Decision) granted() bool { return d == DecisionAllow }

// RuleKey is what an answer is filed under: the napp that asked (the launcher's
// own questions have none, and an installed configuration with no napp is a
// statement about the launcher-wide case), the permission, and the subject
// when the permission takes one — the action name, for a dispatch.
type RuleKey struct {
	Napp       string     `json:"napp"`
	Permission Permission `json:"permission"`
	Subject    string     `json:"subject,omitempty"`
}

func (k RuleKey) valid() bool { return k.Permission != "" }

func (k RuleKey) String() string {
	s := string(k.Permission)
	if k.Subject != "" {
		s += "/" + k.Subject
	}
	if k.Napp != "" {
		s = k.Napp + "/" + s
	}
	return s
}

// ruleID is the key as state.json files it. Napp ids and action names are
// whatever the network said, so the parts are joined with a separator neither
// can hold rather than with a readable one.
func (k RuleKey) ruleID() string {
	return strings.Join([]string{k.Napp, string(k.Permission), k.Subject}, "\x1f")
}

func ruleKeyFromID(id string) RuleKey {
	parts := strings.Split(id, "\x1f")
	for len(parts) < 3 {
		parts = append(parts, "")
	}
	return RuleKey{Napp: parts[0], Permission: Permission(parts[1]), Subject: parts[2]}
}

// Rule is what an answer says: the verdict, plus the napp a dispatch should
// land in, so "always open these with notes" is expressible too.
type Rule struct {
	Decision Decision `json:"decision"`
	Target   string   `json:"target,omitempty"`
}

// decisionOf is the rule a plain yes/no answer becomes.
func decisionOf(ok bool) Decision {
	if ok {
		return DecisionAllow
	}
	return DecisionDeny
}

// ─── the layers ──────────────────────────────────────────────────

// sessionRules are the answers the "allow/deny this session" buttons gave:
// they live in memory for as long as the launcher runs and are never written
// down.
var sessionRules = xsync.NewMapOf[string, Rule]()

// lookupRule is the one place a question is answered from: the installed
// configuration first (it states what a napp needs, so it comes before
// anything the user has said about it), then this session, then what was
// saved. Not finding a rule is the normal case, and means "ask".
func lookupRule(key RuleKey) (Rule, bool) {
	if key == (RuleKey{}) {
		return Rule{}, false
	}
	if r, ok := installedRule(key); ok {
		return r, true
	}
	if r, ok := sessionRule(key); ok {
		return r, true
	}
	return storedRule(key)
}

// remember files an answer under the scope the user chose it with: for this
// prompt (nothing is kept), for this session, or always.
//
// The newest word from the user is the one that counts, but not at the cost of
// a decision they wanted to keep: a session answer only shadows what was
// saved, so the saved answer is still there after the launcher quits, while a
// saved answer does replace the session's, which would otherwise keep shadowing
// it for the rest of the run.
func remember(key RuleKey, r Rule, scope Scope) {
	switch scope {
	case ScopeSession:
		setSessionRule(key, r)
	case ScopeAlways:
		clearSessionRule(key)
		storeRule(key, r)
	}
}

func sessionRule(key RuleKey) (Rule, bool) {
	return sessionRules.Load(key.ruleID())
}

func setSessionRule(key RuleKey, r Rule) {
	sessionRules.Store(key.ruleID(), r)
}

func clearSessionRule(key RuleKey) {
	sessionRules.Delete(key.ruleID())
}

func storedRule(key RuleKey) (Rule, bool) {
	stateMu.Lock()
	defer stateMu.Unlock()
	r, ok := state.Rules[key.ruleID()]
	return r, ok
}

func storeRule(key RuleKey, r Rule) {
	stateMu.Lock()
	if state.Rules == nil {
		state.Rules = make(map[string]Rule)
	}
	if r.Decision == DecisionAsk {
		delete(state.Rules, key.ruleID())
	} else {
		state.Rules[key.ruleID()] = r
	}
	saveState()
	stateMu.Unlock()
	log.Info().Str("key", key.String()).Str("decision", string(r.Decision)).
		Msg("remembered the user's answer")
}

// ─── what the user has decided ───────────────────────────────────

// PermissionRule is one remembered answer as a UI wants to show it.
type PermissionRule struct {
	Napp       string     `json:"napp"`
	NappName   string     `json:"nappName"`
	Permission Permission `json:"permission"`
	Subject    string     `json:"subject"`
	Decision   Decision   `json:"decision"`
	Target     string     `json:"target,omitempty"`
}

// PermissionRules is everything remembered so far, saved answers first and
// then this session's, for a UI that wants to show what it agreed to and let
// the user take it back.
func PermissionRules() []PermissionRule {
	// a keyed answer, while the state file only keys it by id
	type keyed struct {
		id   string
		rule Rule
	}

	stateMu.Lock()
	saved := make([]keyed, 0, len(state.Rules))
	for id, rule := range state.Rules {
		if rule.Decision == DecisionAsk {
			continue
		}
		saved = append(saved, keyed{id, rule})
	}
	stateMu.Unlock()

	out := make([]PermissionRule, 0, len(saved))
	seen := make(map[string]bool, len(saved))
	for _, r := range saved {
		seen[r.id] = true
		out = append(out, permissionRuleOf(ruleKeyFromID(r.id), r.rule))
	}

	pending := make([]keyed, 0, sessionRules.Size())
	for id, rule := range sessionRules.Range {
		if !seen[id] {
			pending = append(pending, keyed{id, rule})
		}
	}
	for _, r := range pending {
		out = append(out, permissionRuleOf(ruleKeyFromID(r.id), r.rule))
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Napp != out[j].Napp {
			return out[i].Napp < out[j].Napp
		}
		if out[i].Permission != out[j].Permission {
			return out[i].Permission < out[j].Permission
		}
		return out[i].Subject < out[j].Subject
	})
	return out
}

func permissionRuleOf(key RuleKey, rule Rule) PermissionRule {
	name := "the launcher"
	if key.Napp != "" {
		name = key.Napp
	}
	if n, ok := InstalledNapp(key.Napp); ok {
		name = n.Label()
	} else {
		for _, n := range DevNapps() {
			if n.ID == key.Napp {
				name = n.Label()
				break
			}
		}
	}
	return PermissionRule{
		Napp:       key.Napp,
		NappName:   name,
		Permission: key.Permission,
		Subject:    key.Subject,
		Decision:   rule.Decision,
		Target:     rule.Target,
	}
}

// ForgetPermission takes back what was remembered: everything about a napp
// with no permission named, or one permission of a napp. Whatever that napp
// asks next is a prompt again.
func ForgetPermission(napp string, perm Permission) {
	// a rule is filed under the napp and the permission (and the subject, for
	// the permissions that take one), so a whole napp is everything filed
	// under its name, and one of its permissions is everything filed under
	// that too
	matches := func(key RuleKey) bool {
		return key.Napp == napp && (perm == "" || key.Permission == perm)
	}

	dropped := false
	for id := range sessionRules.Range {
		if matches(ruleKeyFromID(id)) {
			sessionRules.Delete(id)
			dropped = true
		}
	}

	stateMu.Lock()
	saved := false
	for id := range state.Rules {
		if !matches(ruleKeyFromID(id)) {
			continue
		}
		delete(state.Rules, id)
		saved = true
	}
	if saved {
		saveState()
	}
	stateMu.Unlock()

	if dropped || saved {
		log.Info().Str("napp", napp).Str("permission", string(perm)).Msg("forgot a remembered answer")
		notifyState()
		if perm == "" || perm == PermDispatch {
			go broadcastIntentChanges()
		}
	}
}

// forgetDispatchTarget removes defaults that point at an app which is no
// longer installed. Leaving one behind would turn a missing handler into a
// stale-rule failure instead of allowing discovery or another user choice.
func forgetDispatchTarget(target string) {
	for id, rule := range sessionRules.Range {
		if rule.Target == target {
			sessionRules.Delete(id)
		}
	}

	stateMu.Lock()
	changed := false
	for id, rule := range state.Rules {
		if rule.Target == target {
			delete(state.Rules, id)
			changed = true
		}
	}
	if changed {
		saveState()
	}
	stateMu.Unlock()
}

// ─── the installed configuration ──────────────────────────────────
//
// A napp — or the person running it — will be able to ship a document saying
// what it is allowed to do and which actions it handles, so the launcher can
// decide on its own instead of asking about every signature. Nothing loads
// one yet: the file format, where it comes from (inside a napp, or a file the
// user keeps) and how it is trusted are all still open. When that lands it
// goes through the same RuleKey lookup as everything else, and its verdicts
// come first.

// InstalledConfiguration is such a document.
type InstalledConfiguration struct {
	// Napp is the napp it speaks for; empty means it is about the launcher's
	// own questions rather than any one napp's.
	Napp string `json:"napp,omitempty"`

	// Allow and Deny are the verdicts it wants taken as given. They are
	// separate lists so a document can grant without having to spell out
	// the denials, and so both can grow wildcards later without the reader
	// having to know about them.
	Allow []ConfigRule `json:"allow,omitempty"`
	Deny  []ConfigRule `json:"deny,omitempty"`
}

// ConfigRule is one entry of an installed configuration: what it is about (a
// permission, with a subject when the permission takes one) and, for a
// dispatch, the napp that should handle it.
type ConfigRule struct {
	Permission Permission `json:"permission"`
	Subject    string     `json:"subject,omitempty"`
	Target     string     `json:"target,omitempty"`
}

// installedConfigs is what installedRule reads. It is empty until something
// loads a document into it.
var installedConfigs []InstalledConfiguration

func installedRule(key RuleKey) (Rule, bool) {
	for _, cfg := range installedConfigs {
		if cfg.Napp != key.Napp {
			continue
		}
		for _, list := range []struct {
			rules    []ConfigRule
			decision Decision
		}{
			{cfg.Allow, DecisionAllow},
			{cfg.Deny, DecisionDeny},
		} {
			for _, r := range list.rules {
				if r.Permission != key.Permission || r.Subject != key.Subject {
					continue
				}
				return Rule{Decision: list.decision, Target: r.Target}, true
			}
		}
	}
	return Rule{}, false
}
