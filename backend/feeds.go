package backend

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/sdk"
)

// A feed is a live subscription a napp holds: window.napp.feeds.*(…) returns
// a handle, we push batches of events into the callback and set `synced` once
// the relays have caught up. Everything that comes in is stored locally, so
// the next launch renders from disk before a single websocket is open.

type feedParams struct {
	Pubkey     json.RawMessage  `json:"pubkey"`
	Pubkeys    json.RawMessage  `json:"pubkeys"`
	Source     string           `json:"source"`
	Kinds      []nostr.Kind     `json:"kinds"`
	CallbackID int              `json:"callbackId"`
	Since      *nostr.Timestamp `json:"since"`
	Until      *nostr.Timestamp `json:"until"`
	Limit      int              `json:"limit"`
}

// pubkeyList accepts both `"<hex>"` and `["<hex>", …]`, as env.d.ts allows.
func pubkeyList(raw json.RawMessage) []nostr.PubKey {
	if len(raw) == 0 {
		return nil
	}
	out := make([]nostr.PubKey, 0, 1)
	var one string
	if err := json.Unmarshal(raw, &one); err == nil {
		if pk, err := nostr.PubKeyFromHex(strings.TrimSpace(one)); err == nil {
			out = append(out, pk)
		}
		return out
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err == nil {
		for _, s := range many {
			if pk, err := nostr.PubKeyFromHex(strings.TrimSpace(s)); err == nil {
				out = append(out, pk)
			}
		}
	}
	return out
}

func (p feedParams) filter(authors []nostr.PubKey, inbox bool) nostr.Filter {
	filter := nostr.Filter{Kinds: p.Kinds, Limit: p.Limit}
	if filter.Limit <= 0 {
		filter.Limit = 100
	}
	if inbox {
		hexes := make([]string, 0, len(authors))
		for _, pk := range authors {
			hexes = append(hexes, pk.Hex())
		}
		filter.Tags = nostr.TagMap{"p": hexes}
	} else {
		filter.Authors = authors
	}
	if p.Since != nil {
		filter.Since = *p.Since
	}
	if p.Until != nil {
		filter.Until = *p.Until
	}
	return filter
}

// startFeed resolves what to ask and from where, then runs the pump.
func startFeed(ctx context.Context, ci *Instance, method string, p feedParams) {
	if sys == nil {
		return
	}

	var (
		authors []nostr.PubKey
		inbox   bool
	)

	switch method {
	case "napp.feeds.profile":
		authors = pubkeyList(p.Pubkey)
	case "napp.feeds.following":
		source := sdkPubkey(p.Source)
		if source == nil {
			log.Debug().Str("source", preview(p.Source, 40)).Msg("following feed: bad source")
			return
		}
		res := loadFollowsList(ctx, *source)
		items, _ := res["items"].([]any)
		for _, item := range items {
			if hex, ok := item.(string); ok {
				if pk, err := nostr.PubKeyFromHex(hex); err == nil {
					authors = append(authors, pk)
				}
			}
		}
	case "napp.feeds.inbox":
		authors = pubkeyList(p.Pubkey)
		inbox = true
	case "napp.feeds.outbox":
		authors = pubkeyList(p.Pubkeys)
		if len(authors) == 0 {
			authors = pubkeyList(p.Pubkey)
		}
	}

	if len(authors) == 0 {
		log.Debug().Str("method", method).Msg("feed has no pubkeys to follow")
		return
	}

	filter := p.filter(authors, inbox)

	// relays: where those people write (or, for an inbox, where they read)
	relays := make([]string, 0, 8)
	for _, pk := range authors {
		var found []string
		if inbox {
			found = sys.FetchInboxRelays(ctx, pk, 3)
		} else {
			found = sys.FetchOutboxRelays(ctx, pk, 3)
		}
		for _, url := range found {
			relays = nostr.AppendUnique(relays, url)
		}
		if len(relays) >= 20 {
			break
		}
	}
	if len(relays) == 0 {
		relays = nostr.AppendUnique(relays, sys.FallbackRelays.Next())
	}

	log.Debug().Str("method", method).Int("authors", len(authors)).
		Int("relays", len(relays)).Int("callback", p.CallbackID).Msg("starting feed")

	go feedPump(ctx, ci, p.CallbackID, filter, relays)
}

func sdkPubkey(input string) *nostr.PubKey {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil
	}
	if pk, err := nostr.PubKeyFromHex(input); err == nil {
		return &pk
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if pp := sdk.InputToProfile(ctx, input); pp != nil {
		return &pp.PublicKey
	}
	return nil
}

// feedPump serves the local store first (instant render), then subscribes and
// keeps pushing. Events are batched so a napp isn't called once per event.
func feedPump(ctx context.Context, ci *Instance, callbackID int, filter nostr.Filter, relays []string) {
	batch := make([]nostr.Event, 0, 64)
	seen := make(map[nostr.ID]bool, 128)
	synced := false

	flush := func() {
		if len(batch) == 0 {
			return
		}
		ci.deliverFeed(callbackID, batch, synced)
		batch = batch[:0]
	}

	// whatever we already have locally
	for evt := range sys.Store.QueryEvents(filter, filter.Limit) {
		if !seen[evt.ID] {
			seen[evt.ID] = true
			batch = append(batch, evt)
		}
	}
	flush()

	events, eose := sys.Pool.SubscribeManyNotifyEOSE(ctx, relays, filter,
		nostr.SubscriptionOptions{Label: "napp-feed"})

	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			flush()
			return
		case <-eose:
			eose = nil
			synced = true
			flush()
			ci.deliverFeed(callbackID, nil, true)
		case ie, more := <-events:
			if !more {
				flush()
				return
			}
			if seen[ie.ID] {
				continue
			}
			seen[ie.ID] = true
			sys.Publisher.Publish(ctx, ie.Event)
			batch = append(batch, ie.Event)
			if len(batch) >= 64 {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

// deliverFeed hands a batch to the napp's callback inside its webview.
func (ci *Instance) deliverFeed(callbackID int, events []nostr.Event, synced bool) {
	if events == nil {
		events = []nostr.Event{}
	}
	payload, err := json.Marshal(events)
	if err != nil {
		log.Error().Err(err).Msg("failed to marshal feed events")
		return
	}
	ci.eval("window.__bridge_feed_callback(" +
		jsNumber(callbackID) + "," + jsString(string(payload)) + "," + jsBool(synced) + ")")
}
