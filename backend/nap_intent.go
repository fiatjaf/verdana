package backend

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"strings"
)

// NAP-INTENT uses the launcher's handler selection and window routing, while
// keeping acceptance and delivery separate. The stable convention identity is
// the routed action; napplet targets receive an intent.deliver push.

func init() {
	handleNap(map[string]napHandler{
		"intent.invoke":    napIntentInvoke,
		"intent.available": napIntentAvailable,
		"intent.handlers":  napIntentHandlers,
	})
}

type intentRequest struct {
	Archetype  string          `json:"archetype"`
	Action     string          `json:"action"`
	Convention string          `json:"convention"`
	Payload    json.RawMessage `json:"payload"`
	Handler    string          `json:"handler"`
}

func napIntentInvoke(c *napCall) {
	var r struct {
		Request intentRequest `json:"request"`
	}
	if err := c.decode(&r); err != nil {
		c.reply(map[string]any{"result": map[string]any{"ok": false, "error": "invalid convention"}})
		return
	}
	req := r.Request
	result := map[string]any{"ok": false}
	fail := func(msg string) {
		result["error"] = msg
		c.reply(map[string]any{"result": result})
	}

	archetype, action, ok := conventionParts(req.Convention)
	if !ok || archetype != req.Archetype || action != req.Action {
		fail("invalid convention")
		return
	}
	topic := req.Convention

	opts := actionOptions{}
	switch h := req.Handler; {
	case h == "" || h == "default":
	case h == "choose":
		opts.Choose = true
	default:
		target, ok := handlerByDTag(h)
		if !ok {
			fail("no handler")
			return
		}
		opts.NappID = target.ID
	}
	accepted := false
	opts.Accept = func(target *Instance) {
		accepted = true
		result = map[string]any{
			"ok": true, "archetype": archetype, "action": action,
			"convention": req.Convention, "handler": target.napp.D,
		}
		c.reply(map[string]any{"result": result})
	}

	c.async(func(ctx context.Context) {
		_, err := runNappAction(ctx, c.ci, topic, req.Payload, opts)
		if accepted {
			return
		}
		switch {
		case errors.Is(err, errNoHandler):
			result["error"] = "no handler"
		case err != nil && strings.Contains(err.Error(), "cancelled"):
			result["error"] = "user cancelled"
		default:
			result["error"] = "invoke rejected"
		}
		c.reply(map[string]any{"result": result})
	})
}

func conventionParts(convention string) (string, string, bool) {
	rest, ok := strings.CutPrefix(convention, "napplet:")
	archetype, action, found := strings.Cut(rest, "/")
	if !ok || !found || !domainToken.MatchString(archetype) || action == "" ||
		strings.ContainsAny(action, "/?#") {
		return "", "", false
	}
	return archetype, action, true
}

func handlerByDTag(dTag string) (Napp, bool) {
	for _, n := range intentPool() {
		if n.D == dTag {
			return n, true
		}
	}
	return Napp{}, false
}

func intentPool() []Napp {
	return append(installedNapps(), DevNapps()...)
}

// ─── availability ────────────────────────────────────────────────

func napIntentAvailable(c *napCall) {
	var r struct {
		Archetype string `json:"archetype"`
	}
	if err := c.decode(&r); err != nil || r.Archetype == "" {
		c.reply(map[string]any{"error": "unknown archetype"})
		return
	}
	c.reply(map[string]any{"availability": intentAvailability(r.Archetype)})
}

func napIntentHandlers(c *napCall) {
	list := []map[string]any{}
	for _, a := range intentArchetypes() {
		list = append(list, intentAvailability(a))
	}
	c.reply(map[string]any{"handlers": list})
}

// intentActionsFor lists what a napp accepts for an archetype: the actions
// (the last path segment) and the convention ids.
func intentActionsFor(n Napp, archetype string) (actions, conventions []string, contracts []map[string]any) {
	prefix := "napplet:" + archetype + "/"
	for _, contract := range n.Conventions {
		if rest, ok := strings.CutPrefix(contract.ID, prefix); ok && rest != "" {
			actions = appendUniqueString(actions, rest)
			conventions = appendUniqueString(conventions, contract.ID)
			item := map[string]any{"convention": contract.ID}
			if len(contract.EventKinds) > 0 {
				item["eventKinds"] = contract.EventKinds
			}
			contracts = append(contracts, item)
		}
	}
	return actions, conventions, contracts
}

func intentAvailability(archetype string) map[string]any {
	candidates := []map[string]any{}
	defaultID := ""
	if rule, ok := lookupRule(RuleKey{Permission: PermDispatch, Subject: "napplet:" + archetype + "/open"}); ok &&
		rule.Decision.granted() {
		defaultID = rule.Target
	}
	hasDefault := false
	for _, n := range intentPool() {
		actions, conventions, contracts := intentActionsFor(n, archetype)
		if len(actions) == 0 {
			continue
		}
		cand := map[string]any{
			"dTag":        n.D,
			"title":       n.Label(),
			"actions":     actions,
			"conventions": conventions,
			"contracts":   contracts,
		}
		if defaultID != "" && n.ID == defaultID {
			cand["isDefault"] = true
			hasDefault = true
		}
		candidates = append(candidates, cand)
	}
	return map[string]any{
		"archetype":  archetype,
		"available":  len(candidates) > 0,
		"candidates": candidates,
		"hasDefault": hasDefault,
	}
}

// intentArchetypes is every archetype some installed or dev napp handles.
func intentArchetypes() []string {
	seen := []string{}
	for _, n := range intentPool() {
		for _, contract := range n.Conventions {
			rest, ok := strings.CutPrefix(contract.ID, "napplet:")
			if !ok {
				continue
			}
			role, _, found := strings.Cut(rest, "/")
			if found && role != "" && !slices.Contains(seen, role) {
				seen = append(seen, role)
			}
		}
	}
	sort.Strings(seen)
	return seen
}

// broadcastIntentChanges tells every napplet the handler landscape moved (a
// napp or napplet was installed, removed or reloaded).
func broadcastIntentChanges() {
	napplets := liveNapplets()
	if len(napplets) == 0 {
		return
	}
	envs := []any{}
	for _, a := range intentArchetypes() {
		envs = append(envs, map[string]any{"type": "intent.changed", "availability": intentAvailability(a)})
	}
	for _, ci := range napplets {
		ci.napPush(envs...)
	}
}
