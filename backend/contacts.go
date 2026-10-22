package backend

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"fiatjaf.com/nostr"
)

// The "contacts" and "mine" modes of the napps tab: the napps published by
// the people the logged-in user follows, and the ones the user published.
// Both are only ever read from the local store (refreshContacts). What gets
// them into the store is a slow sync that runs for as long as someone is
// logged in: one contact at a time, always the one scanned longest ago, each
// asked for its napps on its own write relays (plus the discovery relays).
// When everybody was scanned in the past hour it rests for an hour.
//
// When each pubkey was last scanned is kept in contact_scans.json, so a
// restart picks up where the last run stopped. Opening someone's profile
// page scans them too (FetchAuthorNapps), and counts if they are a contact.

const (
	// contactsPace is the pause between two contacts' scans.
	contactsPace = time.Second

	// contactsRescan is how old the oldest scan must be before the sync
	// does anything again.
	contactsRescan = time.Hour
)

var (
	contactsMu     sync.Mutex
	cancelContacts context.CancelFunc

	// contactsSet are the pubkeys whose scans count: the follows of the
	// logged-in user (contactsList, sorted) and the user themselves.
	contactsSet  map[nostr.PubKey]struct{}
	contactsList []nostr.PubKey

	// contactScans is when each pubkey was last scanned, in unix seconds,
	// mirrored to contactScansPath.
	contactScansMu   sync.Mutex
	contactScans     = make(map[string]int64)
	contactScansPath string
)

func loadContactScans() {
	contactScansPath = filepath.Join(dataDir, "contact_scans.json")
	data, err := os.ReadFile(contactScansPath)
	if err != nil {
		return
	}
	contactScansMu.Lock()
	defer contactScansMu.Unlock()
	if err := json.Unmarshal(data, &contactScans); err != nil || contactScans == nil {
		contactScans = make(map[string]int64)
	}
}

func lastScanned(pk nostr.PubKey) time.Time {
	contactScansMu.Lock()
	defer contactScansMu.Unlock()
	return time.Unix(contactScans[pk.Hex()], 0)
}

// contactsHas says whether pk is a contact of the logged-in user, or the user.
func contactsHas(pk nostr.PubKey) bool {
	contactsMu.Lock()
	defer contactsMu.Unlock()
	_, ok := contactsSet[pk]
	return ok
}

// markScanned records a scan of pk that just finished, if pk is a contact (or
// the user).
func markScanned(pk nostr.PubKey) {
	if !contactsHas(pk) {
		return
	}

	contactScansMu.Lock()
	defer contactScansMu.Unlock()
	contactScans[pk.Hex()] = time.Now().Unix()
	data, err := json.Marshal(contactScans)
	if err != nil {
		return
	}
	if err := os.WriteFile(contactScansPath, data, 0600); err != nil {
		log.Error().Err(err).Msg("failed to write contact scans")
	}
}

// startContactsSync (re)starts the sync for the user just logged in.
func startContactsSync(me nostr.PubKey) {
	contactsMu.Lock()
	if cancelContacts != nil {
		cancelContacts()
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancelContacts = cancel
	contactsSet = map[nostr.PubKey]struct{}{me: {}}
	contactsList = nil
	contactsMu.Unlock()

	ls.mu.Lock()
	ls.contactsFor = me
	ls.contacts = nil
	ls.mine = nil
	ls.contactsFetching = false
	ls.mu.Unlock()

	go contactsSync(ctx, me)
}

func contactsSync(ctx context.Context, me nostr.PubKey) {
	// the user's own napps are known before the follow list is
	refreshContacts(me)

	for ctx.Err() == nil {
		fctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		follows := sys.FetchFollowList(fctx, me).Items
		cancel()
		if ctx.Err() != nil {
			return
		}

		set := map[nostr.PubKey]struct{}{me: {}}
		list := make([]nostr.PubKey, 0, len(follows))
		for _, f := range follows {
			if _, dup := set[f.Pubkey]; dup {
				continue
			}
			set[f.Pubkey] = struct{}{}
			list = append(list, f.Pubkey)
		}
		sort.Slice(list, func(i, j int) bool { return list[i].Hex() < list[j].Hex() })
		contactsMu.Lock()
		contactsSet, contactsList = set, list
		contactsMu.Unlock()
		refreshContacts(me)

		// the user's own napps are scanned like a contact's
		next, oldest := me, lastScanned(me)
		for _, pk := range list {
			if t := lastScanned(pk); t.Before(oldest) {
				next, oldest = pk, t
			}
		}
		if time.Since(oldest) < contactsRescan {
			log.Info().Int("contacts", len(list)).Msg("all contacts scanned in the past hour, resting")
			if !sleepCtx(ctx, contactsRescan) {
				return
			}
			continue
		}

		setContactsFetching(me, true)
		scanAuthorNapps(ctx, next)
		setContactsFetching(me, false)
		refreshContacts(me)

		if !sleepCtx(ctx, contactsPace) {
			return
		}
	}
}

// scanAuthorNapps asks pk's write relays and the discovery relays for the
// napps pk published and saves them all in the store.
func scanAuthorNapps(ctx context.Context, pk nostr.PubKey) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	urls := sys.FetchWriteRelays(ctx, pk)
	for _, r := range Relays() {
		urls = nostr.AppendUnique(urls, r)
	}
	n := 0
	for re := range sys.Pool.FetchMany(ctx, urls, nostr.Filter{
		Kinds:   []nostr.Kind{KindWeb, KindWasm},
		Authors: []nostr.PubKey{pk},
	}, nostr.SubscriptionOptions{Label: "verdana-author-napps"}) {
		if _, err := saveEvent(re.Event); err != nil {
			log.Warn().Err(err).Msg("failed to save a napp event")
		}
		n++
	}
	// a canceled scan (logout) didn't really happen
	if context.Cause(ctx) == context.Canceled {
		return
	}
	log.Debug().Str("pubkey", pk.Hex()).Int("events", n).Msg("scanned napps")
	markScanned(pk)
}

// storedNapps is every napp the store has from these authors, newest version
// per napp id.
func storedNapps(authors []nostr.PubKey) []Napp {
	if len(authors) == 0 {
		return []Napp{}
	}
	byID := make(map[string]Napp)
	for evt := range sys.Store.QueryEvents(nostr.Filter{
		Kinds:   []nostr.Kind{KindWeb, KindWasm},
		Authors: authors,
	}, 5000) {
		n := nappFromEvent(evt)
		if prev, ok := byID[n.ID]; ok && prev.CreatedAt >= n.CreatedAt {
			continue
		}
		byID[n.ID] = n
	}
	list := make([]Napp, 0, len(byID))
	for _, n := range byID {
		rememberNapp(n)
		list = append(list, n)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].CreatedAt != list[j].CreatedAt {
			return list[i].CreatedAt > list[j].CreatedAt
		}
		return list[i].ID < list[j].ID
	})
	return list
}

// RefreshContacts reads the contacts' napps and the user's own from the
// local store again. Cheap: nothing goes out to the relays.
func RefreshContacts() {
	if userPubkey != nostr.ZeroPK && sys != nil {
		refreshContacts(userPubkey)
	}
}

func refreshContacts(me nostr.PubKey) {
	contactsMu.Lock()
	follows := append([]nostr.PubKey(nil), contactsList...)
	contactsMu.Unlock()

	contacts := storedNapps(follows)
	mine := storedNapps([]nostr.PubKey{me})

	ls.mu.Lock()
	if ls.contactsFor != me {
		ls.mu.Unlock()
		return
	}
	ls.contacts = contacts
	ls.mine = mine
	ls.sortDiscovery()
	ls.mu.Unlock()
	notifyState()
}

func setContactsFetching(me nostr.PubKey, fetching bool) {
	ls.mu.Lock()
	if ls.contactsFor == me {
		ls.contactsFetching = fetching
	}
	ls.mu.Unlock()
	notifyState()
}

// sleepCtx waits d, or less if ctx ends first (and then says false).
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// resetContacts stops the sync and forgets the contacts' napps (on logout:
// they were someone else's contacts).
func resetContacts() {
	contactsMu.Lock()
	if cancelContacts != nil {
		cancelContacts()
		cancelContacts = nil
	}
	contactsSet, contactsList = nil, nil
	contactsMu.Unlock()

	ls.mu.Lock()
	ls.contacts = nil
	ls.mine = nil
	ls.contactsFor = nostr.ZeroPK
	ls.contactsFetching = false
	ls.mu.Unlock()
}
