package backend

import (
	"context"
	"errors"
	"slices"
	"sync"

	"fiatjaf.com/nostr"
)

// Outbox routing (NIP-65) for NAP-OUTBOX: where to read an author's events
// (their write relays), where to reach a person (their read relays) and where
// one signed event has to go. The napplet only says what it wants; every
// relay is picked or validated here.

const (
	// outboxMaxAuthors bounds how many authors' relay lists one request
	// looks up; the rest are asked on the fallback relays
	outboxMaxAuthors = 256
	// outboxRelaysPerAuthor is how many of an author's relays are asked
	outboxRelaysPerAuthor = 3
	// outboxMaxDirected bounds the relay connections one filter fans out to
	outboxMaxDirected = 60
	// outboxMaxInboxes bounds the people one publish delivers to
	outboxMaxInboxes = 32
	// outboxLookups is how many relay lists are looked up at once
	outboxLookups = 16
)

// relayListFunc is an author's NIP-65 relay list: write (outbox) and read
// (inbox) relays, and whether a list was found at all.
type relayListFunc func(ctx context.Context, pk nostr.PubKey) (write, read []string, found bool)

// nip65Lists reads relay lists through the sdk (local store, cache, then the
// relay-list relays).
func nip65Lists(ctx context.Context, pk nostr.PubKey) (write, read []string, found bool) {
	rl := sys.FetchRelayList(ctx, pk)
	for _, r := range rl.Items {
		u := nostr.NormalizeURL(r.URL)
		if u == "" {
			continue
		}
		if r.Outbox {
			write = nostr.AppendUnique(write, u)
		}
		if r.Inbox {
			read = nostr.AppendUnique(read, u)
		}
	}
	return write, read, len(rl.Items) > 0
}

// outboxRelayPlan is NAP-OUTBOX's OutboxRelayPlan.
type outboxRelayPlan struct {
	Relays         []string `json:"relays"`
	Source         string   `json:"source"`
	MissingAuthors []string `json:"missingAuthors,omitempty"`
}

// outboxPlan is where authors write ("write") or read ("read"), per their
// NIP-65 lists. With no list at all it is the fallback relays.
func outboxPlan(ctx context.Context, lists relayListFunc, authors []nostr.PubKey, direction string, fallback []string) outboxRelayPlan {
	plan := outboxRelayPlan{Relays: []string{}, Source: "nip65"}
	found := false
	for i, pk := range authors {
		if i >= outboxMaxAuthors {
			plan.MissingAuthors = append(plan.MissingAuthors, pk.Hex())
			continue
		}
		write, read, ok := lists(ctx, pk)
		if !ok {
			plan.MissingAuthors = append(plan.MissingAuthors, pk.Hex())
			continue
		}
		found = true
		urls := write
		if direction == "read" {
			urls = read
		}
		for _, u := range urls {
			plan.Relays = nostr.AppendUnique(plan.Relays, u)
		}
	}
	if !found {
		plan.Source = "fallback"
		for _, u := range fallback {
			if u = nostr.NormalizeURL(u); u != "" {
				plan.Relays = nostr.AppendUnique(plan.Relays, u)
			}
		}
	}
	return plan
}

// directFilter splits a filter by relay, the outbox model proper: each
// author's relays get the filter for just the authors who write there, and
// every relay in full gets the whole filter. Authors no kept relay covers
// (no relays known, or dropped past outboxMaxDirected) go to the fallback
// relays, as does a filter with nowhere else to go.
func directFilter(f nostr.Filter, outboxes map[nostr.PubKey][]string, full, fallback []string) []nostr.DirectedFilter {
	full = normalizedUnique(full)
	byRelay := map[string][]nostr.PubKey{}
	for _, pk := range f.Authors {
		for _, u := range outboxes[pk] {
			if u = nostr.NormalizeURL(u); u != "" && !slices.Contains(full, u) {
				byRelay[u] = append(byRelay[u], pk)
			}
		}
	}

	// the busiest relays first, so the cap drops the ones that serve the
	// fewest authors; ties by url, for a stable order
	relays := make([]string, 0, len(byRelay))
	for u := range byRelay {
		relays = append(relays, u)
	}
	slices.SortFunc(relays, func(a, b string) int {
		if d := len(byRelay[b]) - len(byRelay[a]); d != 0 {
			return d
		}
		if a < b {
			return -1
		}
		return 1
	})
	budget := outboxMaxDirected - len(full)
	if budget < 0 {
		budget = 0
	}
	if len(relays) > budget {
		relays = relays[:budget]
	}

	out := make([]nostr.DirectedFilter, 0, len(relays)+len(full))
	covered := map[nostr.PubKey]bool{}
	for _, u := range relays {
		sub := f.Clone()
		sub.Authors = byRelay[u]
		for _, pk := range sub.Authors {
			covered[pk] = true
		}
		out = append(out, nostr.DirectedFilter{Filter: sub, Relay: u})
	}
	for _, u := range full {
		out = append(out, nostr.DirectedFilter{Filter: f, Relay: u})
	}

	var uncovered []nostr.PubKey
	for _, pk := range f.Authors {
		if !covered[pk] {
			uncovered = append(uncovered, pk)
		}
	}
	rest := f
	switch {
	case len(uncovered) > 0:
		rest = f.Clone()
		rest.Authors = uncovered
	case len(out) > 0:
		return out
	}
	for _, u := range normalizedUnique(fallback) {
		if !slices.Contains(full, u) {
			out = append(out, nostr.DirectedFilter{Filter: rest, Relay: u})
		}
	}
	return out
}

// outboxDirected is where a napplet's filter is asked: its authors on their
// own relays, the tagged people and hinted authors on theirs, the user's
// inbox when nobody is named, and the napplet's validated relay hints.
func outboxDirected(ctx context.Context, f nostr.Filter, hintAuthors []nostr.PubKey, hintRelays []string) []nostr.DirectedFilter {
	authors := f.Authors
	if len(authors) > outboxMaxAuthors {
		authors = authors[:outboxMaxAuthors]
	}
	outboxes := lookupAll(ctx, authors, func(ctx context.Context, pk nostr.PubKey) []string {
		return sys.FetchOutboxRelays(ctx, pk, outboxRelaysPerAuthor)
	})

	full := append([]string(nil), hintRelays...)
	if len(f.Authors) == 0 {
		if len(hintAuthors) > outboxMaxAuthors {
			hintAuthors = hintAuthors[:outboxMaxAuthors]
		}
		for _, urls := range lookupAll(ctx, hintAuthors, func(ctx context.Context, pk nostr.PubKey) []string {
			return sys.FetchOutboxRelays(ctx, pk, outboxRelaysPerAuthor)
		}) {
			full = append(full, urls...)
		}
	}
	if ps, ok := f.Tags["p"]; ok {
		var tagged []nostr.PubKey
		for _, p := range ps {
			if pk, err := nostr.PubKeyFromHex(p); err == nil && len(tagged) < 12 {
				tagged = append(tagged, pk)
			}
		}
		for _, urls := range lookupAll(ctx, tagged, func(ctx context.Context, pk nostr.PubKey) []string {
			return sys.FetchInboxRelays(ctx, pk, outboxRelaysPerAuthor)
		}) {
			full = append(full, urls...)
		}
	}
	if len(f.Authors) == 0 && len(full) == 0 {
		if pk, ok := currentUser(); ok {
			_, read, _ := nip65Lists(ctx, pk)
			full = append(full, read...)
		}
	}
	return directFilter(f, outboxes, full, Relays())
}

// lookupAll runs a relay lookup for many people at once (the sdk batches the
// relay-list fetches underneath).
func lookupAll(ctx context.Context, pks []nostr.PubKey, fn func(context.Context, nostr.PubKey) []string) map[nostr.PubKey][]string {
	out := make(map[nostr.PubKey][]string, len(pks))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, outboxLookups)
	for _, pk := range pks {
		wg.Add(1)
		sem <- struct{}{}
		go func(pk nostr.PubKey) {
			defer func() { <-sem; wg.Done() }()
			urls := fn(ctx, pk)
			mu.Lock()
			out[pk] = urls
			mu.Unlock()
		}(pk)
	}
	wg.Wait()
	return out
}

func normalizedUnique(urls []string) []string {
	out := make([]string, 0, len(urls))
	for _, u := range urls {
		if u = nostr.NormalizeURL(u); u != "" {
			out = nostr.AppendUnique(out, u)
		}
	}
	return out
}

// errNoInbox is a toInboxes author with no NIP-65 read relays to deliver to.
var errNoInbox = errors.New("relay list unavailable")

// outboxFanout is the relay set for one outbox.publish: the user's write
// relays (toOutbox), every toInboxes person's read relays, and the
// napplet's explicit relays. Each inbox relay comes from someone else's
// relay list, so it gets the same public-host check as an explicit one;
// inboxes maps each person to the relays kept for them. A person with no
// usable inbox is an error: delivering to them was asked, not hoped for.
func outboxFanout(ctx context.Context, lists relayListFunc, check func(context.Context, string) (string, error),
	user nostr.PubKey, toOutbox bool, toInboxes []nostr.PubKey, explicit []string,
) (targets []string, inboxes map[nostr.PubKey][]string, err error) {
	targets = []string{}
	if toOutbox {
		write, _, _ := lists(ctx, user)
		for _, u := range write {
			targets = nostr.AppendUnique(targets, u)
		}
	}
	if len(toInboxes) > outboxMaxInboxes {
		return nil, nil, errors.New("too many recipients")
	}
	inboxes = make(map[nostr.PubKey][]string, len(toInboxes))
	for _, pk := range toInboxes {
		_, read, _ := lists(ctx, pk)
		for _, u := range read {
			if v, err := check(ctx, u); err == nil {
				inboxes[pk] = nostr.AppendUnique(inboxes[pk], v)
				targets = nostr.AppendUnique(targets, v)
			}
		}
		if len(inboxes[pk]) == 0 {
			return nil, nil, errNoInbox
		}
	}
	for _, raw := range explicit {
		u, err := check(ctx, raw)
		if err != nil {
			return nil, nil, err
		}
		targets = nostr.AppendUnique(targets, u)
	}
	if len(targets) == 0 {
		return nil, nil, errors.New("no relays to publish to")
	}
	return targets, inboxes, nil
}
