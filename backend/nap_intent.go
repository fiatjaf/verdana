package backend

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"slices"
	"sort"
	"strings"
)

// NAP-INTENT on top of the launcher's action routing. An intent is the
// action napplet:<archetype>/<action>: napplets declare it through their z
// and i tags (Napp.Actions), napps can declare the same string among their
// own actions, and the existing picker, rules and "launch it if it isn't
// open" all apply unchanged. A napplet handler receives the intent as an
// inc.event on that topic (dispatchToNapplet); a napp handler receives it as
// a napp action.

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
	_ = c.decode(&r)
	req := r.Request
	if req.Action == "" {
		req.Action = "open"
	}
	result := map[string]any{
		"ok": false, "archetype": req.Archetype, "action": req.Action, "handled": false,
	}
	fail := func(msg string) {
		result["error"] = msg
		c.reply(map[string]any{"result": result})
	}

	if !domainToken.MatchString(req.Archetype) {
		fail("unknown archetype")
		return
	}
	if strings.ContainsAny(req.Action, "/?#") {
		fail("unsupported action")
		return
	}
	topic := "napplet:" + req.Archetype + "/" + req.Action
	payload := req.Payload
	if req.Convention != "" {
		conv, query, err := splitConvention(req.Convention)
		if err != nil || conv != topic {
			fail("unsupported convention")
			return
		}
		if query != nil {
			if len(payload) > 0 && string(payload) != "null" {
				fail("invoke failed")
				return
			}
			payload = query
		}
		result["convention"] = conv
	}

	opts := actionOptions{}
	switch h := req.Handler; {
	case h == "" || h == "default":
	case h == "choose":
		opts.Choose = true
	default:
		target, ok := handlerByAddress(h)
		if !ok {
			fail("no handler")
			return
		}
		opts.NappID = target.ID
	}

	c.async(func(ctx context.Context) {
		ctx, report := withDispatchReport(ctx)
		_, err := runNappAction(ctx, c.ci, topic, payload, opts)
		if target := report.target(); target != nil {
			result["handler"] = target.napp.Address()
			result["windowId"] = target.instance
		}
		switch {
		case err == nil:
			result["ok"], result["handled"] = true, true
		case errors.Is(err, errNoHandler):
			result["error"] = "no handler"
		case strings.Contains(err.Error(), "cancelled"):
			result["error"] = "user cancelled"
		default:
			result["error"] = "invoke failed"
		}
		c.reply(map[string]any{"result": result})
	})
}

// splitConvention reads napplet:<role>/<intent>[?query] into the queryless
// topic and the query as a payload of string fields, the transposition
// NAP-INC defines for convention topics.
func splitConvention(raw string) (string, json.RawMessage, error) {
	if strings.Contains(raw, "#") {
		return "", nil, errors.New("fragment in convention")
	}
	base, query, hasQuery := strings.Cut(raw, "?")
	if _, err := parseConvention([]string{"i", base}); err != nil {
		return "", nil, err
	}
	if !hasQuery {
		return base, nil, nil
	}
	fields := map[string]string{}
	for _, pair := range strings.Split(query, "&") {
		if pair == "" {
			continue
		}
		k, v, ok := strings.Cut(pair, "=")
		if !ok {
			return "", nil, errors.New("query pair without =")
		}
		key, err1 := url.PathUnescape(k)
		val, err2 := url.PathUnescape(v)
		if err1 != nil || err2 != nil {
			return "", nil, errors.New("bad escape in query")
		}
		if _, dup := fields[key]; dup {
			return "", nil, errors.New("repeated query parameter")
		}
		fields[key] = val
	}
	raw2, err := json.Marshal(fields)
	return base, raw2, err
}

// handlerByAddress finds the installed or dev napp/napplet a NAP-INTENT
// handler address (35129:<pubkey>:<d>, or a 35130 napp) names.
func handlerByAddress(addr string) (Napp, bool) {
	for _, n := range intentPool() {
		if n.Address() == addr {
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
func intentActionsFor(n Napp, archetype string) (actions, conventions []string) {
	prefix := "napplet:" + archetype + "/"
	for _, a := range n.Actions {
		if rest, ok := strings.CutPrefix(a, prefix); ok && rest != "" {
			actions = appendUniqueString(actions, rest)
			conventions = appendUniqueString(conventions, a)
		}
	}
	return actions, conventions
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
		actions, conventions := intentActionsFor(n, archetype)
		if len(actions) == 0 {
			continue
		}
		cand := map[string]any{
			"dTag":        n.D,
			"address":     n.Address(),
			"title":       n.Label(),
			"actions":     actions,
			"conventions": conventions,
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
		for _, a := range n.Actions {
			rest, ok := strings.CutPrefix(a, "napplet:")
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
