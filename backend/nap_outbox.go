package backend

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"fiatjaf.com/nostr"
)

// NAP-OUTBOX: outbox-aware event access (napplet/naps PR #32). Where
// NAP-RELAY is a relay proxy, this is intent: the napplet gives filters,
// event ids and templates, and the launcher picks the relays by NIP-65 (see
// outbox.go), deduplicates, verifies and fans publishes out. Relay EOSE
// stays internal: a subscription is events until outbox.close or
// outbox.closed.

func init() {
	handleNap(map[string]napHandler{
		"outbox.getEvent":      napOutboxGetEvent,
		"outbox.query":         napOutboxQuery,
		"outbox.subscribe":     napOutboxSubscribe,
		"outbox.close":         napOutboxClose,
		"outbox.publish":       napOutboxPublish,
		"outbox.resolveRelays": napOutboxResolveRelays,
	})
}

const (
	// outboxDefaultTimeout is how long a read waits when the napplet didn't
	// say; outboxMaxTimeout stays under the shim's own 30s request timeout
	outboxDefaultTimeout = 8 * time.Second
	outboxMaxTimeout     = 25 * time.Second
	// outboxMaxHints bounds the relay hints a read accepts
	outboxMaxHints = 8
)

// outboxReadOptions are OutboxEventOptions, OutboxQueryOptions and
// OutboxSubscribeOptions in one.
type outboxReadOptions struct {
	Author    string   `json:"author"`
	Authors   []string `json:"authors"`
	Relays    []string `json:"relays"`
	Limit     int      `json:"limit"`
	TimeoutMs int      `json:"timeoutMs"`
}

func (o outboxReadOptions) timeout() time.Duration {
	if o.TimeoutMs <= 0 {
		return outboxDefaultTimeout
	}
	return min(max(time.Duration(o.TimeoutMs)*time.Millisecond, time.Second), outboxMaxTimeout)
}

// hintAuthors are the author hints, for relay discovery only.
func (o outboxReadOptions) hintAuthors() []nostr.PubKey {
	out := []nostr.PubKey{}
	for _, s := range append([]string{o.Author}, o.Authors...) {
		if pk, ok := npubOrHex(s); ok && !slices.Contains(out, pk) {
			out = append(out, pk)
		}
	}
	return out
}

// hintRelays are the napplet's relay hints that are public ws(s) relays.
// Hints are only a place to look, so a bad one is dropped, not an error.
func (o outboxReadOptions) hintRelays(ctx context.Context) []string {
	out := []string{}
	for i, raw := range o.Relays {
		if i >= outboxMaxHints {
			break
		}
		if u, err := napExplicitRelay(ctx, raw); err == nil {
			out = nostr.AppendUnique(out, u)
		}
	}
	return out
}

// outboxResult is a RelayEventResult with the relays the event was seen on,
// when it came from the network.
func outboxResult(evt nostr.Event, relays []string) map[string]any {
	res := relayEventResult(evt)
	if len(relays) > 0 {
		res["sidecar"] = map[string]any{"relayHints": relays}
	}
	return res
}

// validEvent is the shell's own check of what it delivers: the id is the
// event's hash and the signature is the author's.
func validEvent(evt nostr.Event) bool {
	return evt.CheckID() && evt.VerifySignature()
}

// ─── getEvent ────────────────────────────────────────────────────

func napOutboxGetEvent(c *napCall) {
	var r struct {
		EventID string            `json:"eventId"`
		Options outboxReadOptions `json:"options"`
	}
	if err := c.decode(&r); err != nil {
		c.reply(map[string]any{"error": "invalid filter"})
		return
	}
	id, err := nostr.IDFromHex(r.EventID)
	if err != nil {
		c.reply(map[string]any{"error": "invalid filter"})
		return
	}
	if sys == nil {
		c.reply(map[string]any{"error": "not ready"})
		return
	}
	c.async(func(ctx context.Context) {
		ctx, cancel := context.WithTimeout(ctx, r.Options.timeout())
		defer cancel()
		f := nostr.Filter{IDs: []nostr.ID{id}}
		for evt := range sys.Store.QueryEvents(f, 1) {
			if evt.ID == id && validEvent(evt) {
				c.reply(map[string]any{"result": outboxResult(evt, nil)})
				return
			}
		}

		// the author's outbox when known, the napplet's hints and the
		// launcher's relays: never a search across everything
		relays := r.Options.hintRelays(ctx)
		for _, pk := range r.Options.hintAuthors() {
			for _, u := range sys.FetchOutboxRelays(ctx, pk, outboxRelaysPerAuthor) {
				relays = nostr.AppendUnique(relays, u)
			}
		}
		for _, u := range Relays() {
			relays = nostr.AppendUnique(relays, nostr.NormalizeURL(u))
		}
		if len(relays) > 0 {
			for re := range sys.Pool.FetchMany(ctx, relays, f, nostr.SubscriptionOptions{Label: "napplet-outbox"}) {
				if re.Event.ID != id || !validEvent(re.Event) {
					continue
				}
				sys.Publisher.Publish(ctx, re.Event)
				c.reply(map[string]any{"result": outboxResult(re.Event, []string{re.Relay.URL})})
				return
			}
		}
		res := map[string]any{"error": "not found"}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			res["incomplete"] = true
		}
		c.reply(res)
	})
}

// ─── query ───────────────────────────────────────────────────────

// outboxFilters parses a request's filters and applies the options' limit.
func outboxFilters(raw json.RawMessage, o outboxReadOptions) ([]nostr.Filter, error) {
	filters, err := napFilters(raw)
	if err != nil {
		return nil, errors.New("invalid filter")
	}
	if o.Limit > 0 {
		for i := range filters {
			filters[i].Limit = min(filters[i].Limit, o.Limit)
		}
	}
	return filters, nil
}

// outboxRoutes is where every filter of a request is asked. Finding the
// relays is the part of a subscription timeoutMs bounds.
func outboxRoutes(ctx context.Context, filters []nostr.Filter, o outboxReadOptions) []nostr.DirectedFilter {
	limit := 5 * time.Second
	if o.TimeoutMs > 0 {
		limit = min(limit, o.timeout())
	}
	rctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	hints := o.hintRelays(rctx)
	authors := o.hintAuthors()
	var dfs []nostr.DirectedFilter
	for _, f := range filters {
		dfs = append(dfs, outboxDirected(rctx, f, authors, hints)...)
	}
	return dfs
}

func napOutboxQuery(c *napCall) {
	var r struct {
		Filters json.RawMessage   `json:"filters"`
		Options outboxReadOptions `json:"options"`
	}
	fail := func(msg string) { c.reply(map[string]any{"events": []any{}, "error": msg}) }
	if err := c.decode(&r); err != nil {
		fail("invalid filter")
		return
	}
	filters, err := outboxFilters(r.Filters, r.Options)
	if err != nil {
		fail(err.Error())
		return
	}
	if sys == nil {
		fail("not ready")
		return
	}
	// each relay applies a filter's limit on its own, so the merged result
	// is cut to what the filters asked for in total
	capN := 0
	for _, f := range filters {
		capN += f.Limit
	}
	capN = min(capN, napMaxLimit)

	c.async(func(ctx context.Context) {
		ctx, cancel := context.WithTimeout(ctx, r.Options.timeout())
		defer cancel()

		type found struct {
			evt    nostr.Event
			relays []string
		}
		byID := map[nostr.ID]*found{}
		order := []*found{}
		// the pool drops an event a second relay sends, so the hints are
		// the first relay it came from; one already in the store gets that
		// relay added when the network sends it too
		add := func(evt nostr.Event, relay string) {
			if f, ok := byID[evt.ID]; ok {
				if relay != "" {
					f.relays = nostr.AppendUnique(f.relays, relay)
				}
				return
			}
			if !validEvent(evt) {
				return
			}
			f := &found{evt: evt}
			if relay != "" {
				f.relays = []string{relay}
			}
			byID[evt.ID] = f
			order = append(order, f)
		}

		for _, f := range filters {
			for evt := range sys.Store.QueryEvents(f, f.Limit) {
				add(evt, "")
			}
		}
		if dfs := outboxRoutes(ctx, filters, r.Options); len(dfs) > 0 {
			for re := range sys.Pool.BatchedQueryMany(ctx, dfs, nostr.SubscriptionOptions{Label: "napplet-outbox"}) {
				if _, seen := byID[re.Event.ID]; !seen {
					sys.Publisher.Publish(ctx, re.Event)
				}
				add(re.Event, re.Relay.URL)
			}
		}

		// newest first, as a feed reads, then the limit
		slices.SortStableFunc(order, func(a, b *found) int {
			return int(b.evt.CreatedAt) - int(a.evt.CreatedAt)
		})
		if len(order) > capN {
			order = order[:capN]
		}
		events := make([]any, 0, len(order))
		for _, f := range order {
			events = append(events, outboxResult(f.evt, f.relays))
		}
		res := map[string]any{"events": events}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			res["incomplete"] = true
		}
		c.reply(res)
	})
}

// ─── subscribe / close ───────────────────────────────────────────

// outboxSubKey keeps outbox subscriptions apart from relay ones in the
// session's table: the napplet picks both ids.
func outboxSubKey(subID string) string { return "outbox:" + subID }

func napOutboxSubscribe(c *napCall) {
	var r struct {
		SubID   string            `json:"subId"`
		Filters json.RawMessage   `json:"filters"`
		Options outboxReadOptions `json:"options"`
	}
	err := c.decode(&r)
	if r.SubID == "" {
		// nothing to answer on: the shim always sends one
		var id struct {
			SubID string `json:"subId"`
		}
		if json.Unmarshal(c.raw, &id) != nil || id.SubID == "" {
			return
		}
		r.SubID = id.SubID
	}
	closed := func(reason string) {
		c.ci.napPushGen(c.gen, map[string]any{"type": "outbox.closed", "subId": r.SubID, "reason": reason})
	}
	if err != nil {
		closed("invalid filter")
		return
	}
	filters, err := outboxFilters(r.Filters, r.Options)
	if err != nil {
		closed(err.Error())
		return
	}
	if sys == nil {
		closed("not ready")
		return
	}

	key := outboxSubKey(r.SubID)
	s := c.ci.nap
	s.mu.Lock()
	if _, dup := s.subs[key]; dup || len(s.subs) >= napMaxSubs {
		s.mu.Unlock()
		closed("too many subscriptions")
		return
	}
	ctx, cancel := context.WithCancel(c.ctx)
	s.subs[key] = cancel
	s.mu.Unlock()

	c.async(func(context.Context) {
		defer func() {
			s.mu.Lock()
			delete(s.subs, key)
			s.mu.Unlock()
			cancel()
		}()
		reason := napOutboxPump(ctx, c, r.SubID, filters, outboxRoutes(ctx, filters, r.Options))
		if ctx.Err() == nil {
			closed(reason)
		}
	})
}

// napOutboxPump streams a subscription: the local store, then the routed
// relays, in batches. It returns, with the reason to give the napplet, once
// no relay is left to listen to; a cancelled ctx means outbox.close or a
// session gone, and nothing more is said.
func napOutboxPump(ctx context.Context, c *napCall, subID string, filters []nostr.Filter, dfs []nostr.DirectedFilter) string {
	seen := map[nostr.ID]bool{}
	pending := []any{}
	flush := func() {
		if len(pending) > 0 {
			c.ci.napPushGen(c.gen, pending...)
			pending = []any{}
		}
	}
	add := func(evt nostr.Event, relays []string) {
		if seen[evt.ID] || !validEvent(evt) {
			return
		}
		seen[evt.ID] = true
		pending = append(pending, map[string]any{
			"type": "outbox.event", "subId": subID,
			"result": outboxResult(evt, relays),
		})
		if len(pending) >= 64 {
			flush()
		}
	}

	for _, f := range filters {
		for evt := range sys.Store.QueryEvents(f, f.Limit) {
			add(evt, nil)
		}
	}
	flush()
	if len(dfs) == 0 {
		return "no relays to ask"
	}

	events := sys.Pool.BatchedSubscribeMany(ctx, dfs, nostr.SubscriptionOptions{Label: "napplet-outbox"})
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ""
		case re, more := <-events:
			if !more {
				flush()
				return "relays closed"
			}
			if !seen[re.Event.ID] {
				sys.Publisher.Publish(ctx, re.Event)
			}
			add(re.Event, []string{re.Relay.URL})
		case <-ticker.C:
			flush()
		}
	}
}

func napOutboxClose(c *napCall) {
	var r struct {
		SubID string `json:"subId"`
	}
	if err := c.decode(&r); err != nil || r.SubID == "" {
		return
	}
	key := outboxSubKey(r.SubID)
	s := c.ci.nap
	s.mu.Lock()
	cancel := s.subs[key]
	delete(s.subs, key)
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	// every request is answered (NAP-OUTBOX); the shim has already dropped
	// the handle, so this only confirms the end of the stream
	c.ci.napPushGen(c.gen, map[string]any{"type": "outbox.closed", "subId": r.SubID, "reason": "closed"})
}

// ─── publish ─────────────────────────────────────────────────────

func napOutboxPublish(c *napCall) {
	var r struct {
		Event   napTemplate `json:"event"`
		Options struct {
			Relays    []string `json:"relays"`
			ToOutbox  *bool    `json:"toOutbox"`
			ToInboxes []string `json:"toInboxes"`
		} `json:"options"`
	}
	fail := func(msg string) { c.reply(map[string]any{"ok": false, "error": msg}) }
	if err := c.decode(&r); err != nil {
		fail("invalid event")
		return
	}
	toOutbox := r.Options.ToOutbox == nil || *r.Options.ToOutbox
	var inboxes []nostr.PubKey
	for _, s := range r.Options.ToInboxes {
		pk, ok := npubOrHex(s)
		if !ok {
			fail("invalid recipient")
			return
		}
		if !slices.Contains(inboxes, pk) {
			inboxes = append(inboxes, pk)
		}
	}

	c.async(func(ctx context.Context) {
		user, ok := currentUser()
		if !ok {
			fail("not-signed-in")
			return
		}
		if sys == nil {
			fail("not ready")
			return
		}

		// every required relay is known before the user is asked: a
		// recipient with no inbox fails the publish, not just their copy
		tctx, cancel := context.WithTimeout(ctx, 8*time.Second)
		targets, required, err := outboxFanout(tctx, nip65Lists, napExplicitRelay,
			user, toOutbox, inboxes, r.Options.Relays, Relays())
		cancel()
		if err != nil {
			fail(err.Error())
			return
		}

		evt, res, err := napApprovePublish(ctx, c, r.Event.event(user), nostr.ZeroPK, "", targets)
		if err != nil {
			if err.Error() == "user-denied" {
				// NAP-OUTBOX's name for it
				err = errors.New("publish denied")
			}
			fail(err.Error())
			return
		}

		accepted := map[string]bool{}
		relays := map[string]bool{}
		if per, ok := res["relays"].(map[string]any); ok {
			for u, v := range per {
				m, _ := v.(map[string]any)
				okv, _ := m["ok"].(bool)
				relays[u] = okv
				accepted[nostr.NormalizeURL(u)] = okv
			}
		}
		out := map[string]any{"ok": true, "event": evt, "eventId": evt.ID.Hex(), "relays": relays}
		if n, _ := res["published"].(int); n == 0 {
			out["ok"], out["error"] = false, "publish-failed"
		}
		// delivering to a recipient was asked for, so one of their inboxes
		// has to have taken it
		for _, urls := range required {
			if !slices.ContainsFunc(urls, func(u string) bool { return accepted[u] }) {
				out["ok"], out["error"] = false, "publish-failed: a recipient's inbox relays refused the event"
				break
			}
		}
		c.reply(out)
	})
}

// ─── resolveRelays ───────────────────────────────────────────────

func napOutboxResolveRelays(c *napCall) {
	var r struct {
		Target struct {
			Authors   []string `json:"authors"`
			Pubkey    string   `json:"pubkey"`
			Direction string   `json:"direction"`
		} `json:"target"`
	}
	if err := c.decode(&r); err != nil {
		c.reply(map[string]any{"error": "invalid target"})
		return
	}
	dir := r.Target.Direction
	if dir == "" {
		dir = "write"
	}
	if dir != "write" && dir != "read" {
		c.reply(map[string]any{"error": "invalid target"})
		return
	}
	var authors []nostr.PubKey
	for _, s := range append(r.Target.Authors, r.Target.Pubkey) {
		if s == "" {
			continue
		}
		pk, ok := npubOrHex(s)
		if !ok {
			c.reply(map[string]any{"error": "invalid target"})
			return
		}
		if !slices.Contains(authors, pk) {
			authors = append(authors, pk)
		}
	}
	if len(authors) == 0 {
		pk, ok := currentUser()
		if !ok {
			c.reply(map[string]any{"error": "no authors"})
			return
		}
		authors = []nostr.PubKey{pk}
	}
	if sys == nil {
		c.reply(map[string]any{"error": "not ready"})
		return
	}
	c.async(func(ctx context.Context) {
		ctx, cancel := context.WithTimeout(ctx, outboxDefaultTimeout)
		defer cancel()
		c.reply(map[string]any{"plan": outboxPlan(ctx, nip65Lists, authors, dir, Relays())})
	})
}
