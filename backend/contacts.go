package backend

import (
	"context"
	"sort"
	"sync"
	"time"

	"fiatjaf.com/nostr"
)

// The "contacts" mode of the napps tab: the napps published by the people the
// logged-in user follows, each one asked for on its author's own write relays
// (outbox-style) rather than on the discovery relays. Nothing is fetched until
// a UI asks for it (EnsureContacts, when the user first opens that mode).

var (
	contactsMu     sync.Mutex
	cancelContacts context.CancelFunc
)

// EnsureContacts fetches the contacts' napps unless that already happened (or
// is happening) for the logged-in user. Non-blocking.
func EnsureContacts() {
	ls.mu.Lock()
	done := userPubkey != nostr.ZeroPK && ls.contactsFor == userPubkey
	ls.mu.Unlock()
	if !done {
		go DiscoverContacts()
	}
}

// DiscoverContacts (re)fetches the napps published by everybody in the
// logged-in user's follow list. Blocks until the round is over.
func DiscoverContacts() {
	me := userPubkey
	if me == nostr.ZeroPK || sys == nil {
		return
	}

	contactsMu.Lock()
	if cancelContacts != nil {
		cancelContacts()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	cancelContacts = cancel
	contactsMu.Unlock()
	defer cancel()

	ls.mu.Lock()
	ls.contactsFor = me
	ls.contactsFetching = true
	ls.contacts = nil
	ls.mu.Unlock()
	notifyState()
	defer func() {
		ls.mu.Lock()
		// a newer round (or a logout) owns the flag now
		if ctx.Err() != context.Canceled {
			ls.contactsFetching = false
		}
		ls.mu.Unlock()
		notifyState()
	}()

	follows := sys.FetchFollowList(ctx, me).Items
	log.Info().Int("follows", len(follows)).Msg("fetching napps from contacts")
	if len(follows) == 0 {
		return
	}

	// each follow's write relays, a bunch at a time
	relaysOf := make([][]string, len(follows))
	sem := make(chan struct{}, 20)
	var wg sync.WaitGroup
	for i, f := range follows {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			relaysOf[i] = sys.FetchWriteRelays(ctx, f.Pubkey)
		})
	}
	wg.Wait()

	// one filter per relay carrying every author that writes there; the ones
	// with no relay list are looked for on the discovery relays instead
	byRelay := make(map[string][]nostr.PubKey)
	for i, f := range follows {
		urls := relaysOf[i]
		if len(urls) == 0 {
			urls = Relays()
		}
		for _, url := range urls {
			byRelay[url] = append(byRelay[url], f.Pubkey)
		}
	}
	dfs := make([]nostr.DirectedFilter, 0, len(byRelay))
	for url, authors := range byRelay {
		dfs = append(dfs, nostr.DirectedFilter{
			Relay: url,
			Filter: nostr.Filter{
				Kinds:   []nostr.Kind{KindWeb, KindWasm},
				Authors: authors,
			},
		})
	}

	// newest version wins per napp id, the list is republished as it grows
	byID := make(map[string]Napp)
	for re := range sys.Pool.BatchedQueryMany(ctx, dfs, nostr.SubscriptionOptions{
		Label: "verdana-contacts",
	}) {
		n := nappFromEvent(re.Event)
		if prev, ok := byID[n.ID]; ok && prev.CreatedAt >= n.CreatedAt {
			continue
		}
		byID[n.ID] = n
		rememberNapp(n)

		list := make([]Napp, 0, len(byID))
		for _, n := range byID {
			list = append(list, n)
		}
		if !setContacts(me, list) {
			return
		}
	}

	log.Info().Int("count", len(byID)).Msg("contacts fetch complete")
}

// setContacts publishes a contacts list, unless the user changed meanwhile.
func setContacts(owner nostr.PubKey, list []Napp) bool {
	ls.mu.Lock()
	if ls.contactsFor != owner {
		ls.mu.Unlock()
		return false
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].CreatedAt != list[j].CreatedAt {
			return list[i].CreatedAt > list[j].CreatedAt
		}
		return list[i].ID < list[j].ID
	})
	ls.contacts = list
	ls.sortDiscovery()
	ls.mu.Unlock()
	notifyState()
	return true
}

// resetContacts forgets the contacts' napps (on logout: they were someone
// else's contacts).
func resetContacts() {
	contactsMu.Lock()
	if cancelContacts != nil {
		cancelContacts()
		cancelContacts = nil
	}
	contactsMu.Unlock()

	ls.mu.Lock()
	ls.contacts = nil
	ls.contactsFor = nostr.ZeroPK
	ls.contactsFetching = false
	ls.mu.Unlock()
}
