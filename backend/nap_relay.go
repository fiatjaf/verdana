package backend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip19"
)

// NAP-RELAY: the napplet's only way to the nostr network. It hands over
// filters and unsigned templates; the launcher picks relays (the outbox
// model, as for napp feeds), signs with the user's key behind a prompt, and
// streams events back. A napplet never sees a key, a socket or a relay it
// wasn't told about. Events reach it exactly as signed: encrypted content
// stays encrypted, since a decrypted copy would no longer match its id
// and signature.

func init() {
	handleNap(map[string]napHandler{
		"relay.subscribe":        napRelaySubscribe,
		"relay.close":            napRelayClose,
		"relay.query":            napRelayQuery,
		"relay.publish":          napRelayPublish,
		"relay.publishEncrypted": napRelayPublishEncrypted,
	})
}

const (
	// napMaxFilters and napMaxLimit keep one napplet from asking relays for
	// the world in one go
	napMaxFilters = 10
	napMaxLimit   = 500
	// napMaxSubs caps open subscriptions per napplet window
	napMaxSubs = 32
)

type napRelayReq struct {
	SubID   string          `json:"subId"`
	Filters json.RawMessage `json:"filters"`
	Relay   string          `json:"relay"`
}

// napFilters parses and bounds a napplet's filters.
func napFilters(raw json.RawMessage) ([]nostr.Filter, error) {
	filters, err := parseFilters(raw)
	if err != nil {
		return nil, err
	}
	if len(filters) == 0 || len(filters) > napMaxFilters {
		return nil, errors.New("invalid filters")
	}
	for i := range filters {
		if filters[i].Limit <= 0 || filters[i].Limit > napMaxLimit {
			filters[i].Limit = napMaxLimit
		}
	}
	return filters, nil
}

// napExplicitRelay validates a relay a napplet named: a public ws(s) relay,
// never something on the user's machine or network.
func napExplicitRelay(ctx context.Context, raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "wss" && u.Scheme != "ws") || u.Host == "" || u.User != nil {
		return "", errors.New("invalid relay url")
	}
	if err := publicHost(ctx, u.Hostname()); err != nil {
		return "", fmt.Errorf("relay not allowed: %w", err)
	}
	return nostr.NormalizeURL(u.String()), nil
}

// napRelaysFor is where a filter is asked: the authors' outboxes, the
// tagged people's inboxes, and the user's own read relays, falling back to
// the launcher's relays.
func napRelaysFor(ctx context.Context, f nostr.Filter) []string {
	urls := []string{}
	add := func(list ...string) {
		for _, u := range list {
			urls = nostr.AppendUnique(urls, u)
		}
	}
	for i, pk := range f.Authors {
		if i >= 12 {
			break
		}
		add(sys.FetchOutboxRelays(ctx, pk, 2)...)
	}
	if ps, ok := f.Tags["p"]; ok {
		for i, p := range ps {
			if i >= 12 {
				break
			}
			if pk, err := nostr.PubKeyFromHex(p); err == nil {
				add(sys.FetchInboxRelays(ctx, pk, 2)...)
			}
		}
	}
	if pk, ok := currentUser(); ok {
		for _, r := range sys.FetchRelayList(ctx, pk).Items {
			if r.Inbox {
				add(r.URL)
			}
		}
	}
	if len(urls) == 0 {
		add(Relays()...)
	}
	return urls
}

// relayEventResult is NAP's RelayEventResult: the event, no sidecar (the
// launcher never pre-resolves resources; NAP-RELAY keeps that off by default).
func relayEventResult(evt nostr.Event) map[string]any {
	return map[string]any{"event": evt}
}

// ─── subscribe / close ───────────────────────────────────────────

func napRelaySubscribe(c *napCall) {
	var r napRelayReq
	if err := c.decode(&r); err != nil || r.SubID == "" {
		return
	}
	closed := func(reason string) {
		c.ci.napPushGen(c.gen, map[string]any{"type": "relay.closed", "subId": r.SubID, "reason": reason})
	}
	filters, err := napFilters(r.Filters)
	if err != nil {
		closed("invalid: " + err.Error())
		return
	}
	if sys == nil {
		closed("error: not ready")
		return
	}

	s := c.ci.nap
	s.mu.Lock()
	if _, dup := s.subs[r.SubID]; dup || len(s.subs) >= napMaxSubs {
		s.mu.Unlock()
		closed("error: too many subscriptions")
		return
	}
	ctx, cancel := context.WithCancel(c.ctx)
	s.subs[r.SubID] = cancel
	s.mu.Unlock()

	c.async(func(context.Context) {
		defer func() {
			s.mu.Lock()
			delete(s.subs, r.SubID)
			s.mu.Unlock()
			cancel()
		}()
		var explicit []string
		if r.Relay != "" {
			relay, err := napExplicitRelay(ctx, r.Relay)
			if err != nil {
				closed("blocked: " + err.Error())
				return
			}
			explicit = []string{relay}
		}
		napRelayPump(ctx, c, r.SubID, filters, explicit)
	})
}

// napRelayPump streams a subscription: what the local store has, then the
// relays, with one relay.eose once every filter is caught up. Events go out
// in batches, so a busy feed is not one eval per event.
func napRelayPump(ctx context.Context, c *napCall, subID string, filters []nostr.Filter, explicit []string) {
	type item struct {
		evt  nostr.Event
		eose bool
	}
	merged := make(chan item, 256)
	seen := map[nostr.ID]bool{}

	pending := []map[string]any{}
	flush := func() {
		if len(pending) == 0 {
			return
		}
		envs := make([]any, len(pending))
		for i, p := range pending {
			envs[i] = p
		}
		c.ci.napPushGen(c.gen, envs...)
		pending = pending[:0]
	}
	add := func(evt nostr.Event) {
		if seen[evt.ID] {
			return
		}
		seen[evt.ID] = true
		pending = append(pending, map[string]any{"type": "relay.event", "subId": subID, "result": relayEventResult(evt)})
		if len(pending) >= 64 {
			flush()
		}
	}

	for _, f := range filters {
		for evt := range sys.Store.QueryEvents(f, f.Limit) {
			add(evt)
		}
	}
	flush()

	for _, f := range filters {
		relays := explicit
		if relays == nil {
			rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			relays = napRelaysFor(rctx, f)
			cancel()
		}
		go func(f nostr.Filter, relays []string) {
			events, eose := sys.Pool.SubscribeManyNotifyEOSE(ctx, relays, f,
				nostr.SubscriptionOptions{Label: "napplet-relay"})
			for {
				select {
				case <-ctx.Done():
					return
				case <-eose:
					eose = nil
					select {
					case merged <- item{eose: true}:
					case <-ctx.Done():
						return
					}
				case ie, more := <-events:
					if !more {
						if eose != nil {
							select {
							case merged <- item{eose: true}:
							case <-ctx.Done():
							}
						}
						return
					}
					select {
					case merged <- item{evt: ie.Event}:
					case <-ctx.Done():
						return
					}
				}
			}
		}(f, relays)
	}

	waiting := len(filters)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case it := <-merged:
			if it.eose {
				waiting--
				if waiting == 0 {
					flush()
					c.ci.napPushGen(c.gen, map[string]any{"type": "relay.eose", "subId": subID})
				}
				continue
			}
			if !seen[it.evt.ID] {
				sys.Publisher.Publish(ctx, it.evt)
			}
			add(it.evt)
		case <-ticker.C:
			flush()
		}
	}
}

func napRelayClose(c *napCall) {
	var r napRelayReq
	if err := c.decode(&r); err != nil || r.SubID == "" {
		return
	}
	s := c.ci.nap
	s.mu.Lock()
	cancel := s.subs[r.SubID]
	delete(s.subs, r.SubID)
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// ─── query ───────────────────────────────────────────────────────

func napRelayQuery(c *napCall) {
	var r napRelayReq
	if err := c.decode(&r); err != nil {
		c.reply(map[string]any{"events": []any{}, "error": "invalid request"})
		return
	}
	filters, err := napFilters(r.Filters)
	if err != nil {
		c.reply(map[string]any{"events": []any{}, "error": err.Error()})
		return
	}
	if sys == nil {
		c.reply(map[string]any{"events": []any{}, "error": "not ready"})
		return
	}
	c.async(func(ctx context.Context) {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		var explicit []string
		if r.Relay != "" {
			relay, err := napExplicitRelay(ctx, r.Relay)
			if err != nil {
				c.reply(map[string]any{"events": []any{}, "error": "blocked: " + err.Error()})
				return
			}
			explicit = []string{relay}
		}
		seen := map[nostr.ID]bool{}
		out := []any{}
		add := func(evt nostr.Event) {
			if !seen[evt.ID] && len(out) < napMaxLimit {
				seen[evt.ID] = true
				out = append(out, relayEventResult(evt))
			}
		}
		for _, f := range filters {
			for evt := range sys.Store.QueryEvents(f, f.Limit) {
				add(evt)
			}
			relays := explicit
			if relays == nil {
				relays = napRelaysFor(ctx, f)
			}
			for re := range sys.Pool.FetchMany(ctx, relays, f, nostr.SubscriptionOptions{Label: "napplet-query"}) {
				add(re.Event)
			}
		}
		c.reply(map[string]any{"events": out})
	})
}

// ─── publish ─────────────────────────────────────────────────────

type napTemplate struct {
	Kind      nostr.Kind      `json:"kind"`
	Content   string          `json:"content"`
	Tags      nostr.Tags      `json:"tags"`
	CreatedAt nostr.Timestamp `json:"created_at"`

	// ask, when set, is what the approval prompt says instead of the generic
	// "sign and publish an event": the launcher built this event, so it can
	// name the action (NAP-COMMON's follow, react, report).
	ask *napAsk
}

// napAsk is an approval prompt's wording for a launcher-built event.
type napAsk struct {
	Title, Detail, Code string
}

func (t napTemplate) event(author nostr.PubKey) nostr.Event {
	evt := nostr.Event{Kind: t.Kind, Content: t.Content, Tags: t.Tags, CreatedAt: t.CreatedAt, PubKey: author}
	if evt.Tags == nil {
		evt.Tags = nostr.Tags{}
	}
	if evt.CreatedAt == 0 {
		evt.CreatedAt = nostr.Now()
	}
	return evt
}

func napRelayPublish(c *napCall) {
	var r struct {
		Event napTemplate `json:"event"`
		Relay string      `json:"relay"`
	}
	if err := c.decode(&r); err != nil {
		c.replyAs("relay.publish.error", map[string]any{"ok": false, "error": "invalid event"})
		return
	}
	c.async(func(ctx context.Context) {
		evt, err := napSignAndPublish(ctx, c, r.Event, "", "", r.Relay)
		if err != nil {
			c.reply(map[string]any{"ok": false, "error": err.Error()})
			return
		}
		c.reply(map[string]any{"ok": true, "event": evt, "eventId": evt.ID.Hex()})
	})
}

func napRelayPublishEncrypted(c *napCall) {
	var r struct {
		Event      napTemplate `json:"event"`
		Recipient  string      `json:"recipient"`
		Encryption string      `json:"encryption"`
	}
	if err := c.decode(&r); err != nil {
		c.reply(map[string]any{"ok": false, "error": "invalid event"})
		return
	}
	if r.Encryption == "" {
		r.Encryption = "nip44"
	}
	if r.Encryption != "nip44" && r.Encryption != "nip04" {
		c.reply(map[string]any{"ok": false, "error": "unsupported encryption"})
		return
	}
	c.async(func(ctx context.Context) {
		evt, err := napSignAndPublish(ctx, c, r.Event, r.Recipient, r.Encryption, "")
		if err != nil {
			c.reply(map[string]any{"ok": false, "error": err.Error()})
			return
		}
		c.reply(map[string]any{"ok": true, "event": evt, "eventId": evt.ID.Hex()})
	})
}

// napSignAndPublish is every napplet write: one prompt that shows exactly
// what will go out (the plaintext, before any encryption), then the
// encryption and signature with the user's key, then the relays. Signing
// after the approval means a signer app is only bothered for writes the user
// already allowed.
func napSignAndPublish(ctx context.Context, c *napCall, t napTemplate, recipient, encryption, relay string) (nostr.Event, error) {
	pk, ok := currentUser()
	if !ok {
		return nostr.Event{}, errors.New("not-signed-in")
	}
	if sys == nil {
		return nostr.Event{}, errors.New("not ready")
	}
	evt := t.event(pk)

	var to nostr.PubKey
	if encryption != "" {
		var ok bool
		if to, ok = npubOrHex(recipient); !ok {
			return nostr.Event{}, errors.New("invalid recipient")
		}
	}

	tctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	var requested []string
	if relay != "" {
		r, err := napExplicitRelay(tctx, relay)
		if err != nil {
			cancel()
			return nostr.Event{}, err
		}
		requested = []string{r}
	}
	if encryption != "" && len(requested) == 0 {
		// the message is for the recipient: their inbox, and our outbox
		requested = append(sys.FetchInboxRelays(tctx, to, 3), sys.FetchWriteRelays(tctx, pk)...)
	}
	targets := publishTargets(tctx, evt, requested)
	cancel()
	if len(targets) == 0 {
		return nostr.Event{}, errors.New("no relays to publish to")
	}

	evt, res, err := napApprovePublish(ctx, c, evt, to, encryption, targets, t.ask)
	if err != nil {
		return evt, err
	}
	if n, _ := res["published"].(int); n == 0 {
		return evt, errors.New("publish-failed")
	}
	return evt, nil
}

// napApprovePublish asks once, then encrypts (when encryption is set), signs
// and publishes evt to targets, reporting per relay as publishSigned does.
func napApprovePublish(ctx context.Context, c *napCall, evt nostr.Event, to nostr.PubKey, encryption string, targets []string, ask *napAsk) (nostr.Event, map[string]any, error) {
	title := "sign and publish an event"
	detail := fmt.Sprintf("Kind %d to %d relay(s): %s", evt.Kind, len(targets),
		preview(strings.Join(stripSchemes(targets), ", "), 160))
	code := preview(evt.Content, 200)
	switch {
	case encryption != "":
		title = "encrypt, sign and publish a message"
		detail = fmt.Sprintf("Kind %d, encrypted (%s) for %s, to %d relay(s).",
			evt.Kind, encryption, nip19.EncodeNpub(to), len(targets))
	case ask != nil:
		title, code = ask.Title, ask.Code
		detail = fmt.Sprintf("%s (kind %d, to %d relay(s))", ask.Detail, evt.Kind, len(targets))
	}
	if !askApproval(c.ci, PermPublish, title, detail, code) {
		return nostr.Event{}, nil, errors.New("user-denied")
	}

	sctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if encryption != "" {
		var (
			ciphertext string
			err        error
		)
		if encryption == "nip04" {
			ciphertext, err = userKeyer.Nip04Encrypt(sctx, evt.Content, to)
		} else {
			ciphertext, err = userKeyer.Encrypt(sctx, evt.Content, to)
		}
		if err != nil {
			return nostr.Event{}, nil, keyerErr(err)
		}
		evt.Content = ciphertext
	}
	if err := userKeyer.SignEvent(sctx, &evt); err != nil {
		return nostr.Event{}, nil, keyerErr(err)
	}

	pctx, pcancel := context.WithTimeout(ctx, 10*time.Second)
	defer pcancel()
	return evt, publishSigned(pctx, evt, targets), nil
}
