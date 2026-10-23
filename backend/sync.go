package backend

import (
	"context"
	"encoding/binary"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"fiatjaf.com/nostr"
	"github.com/puzpuzpuz/xsync/v3"
)

// napp.sync: bring an (author, kind) range into the store from the author's
// write relays, remembering in the kvstore what is already here so a later
// call for a range we hold asks no relay at all.
//
// A bound is one contiguous [oldest, newest] per author and kind. A call
// fetches only what lies outside it — and the gap between the two when the
// range asked for is disjoint from it, so the bound stays one piece.
//
// With a limit it is the newest that many before until instead: the store
// answers for what lies inside the bound, and the relays for the rest.

const (
	syncPageLimit = 500
	syncMaxPages  = 20
	syncMaxWait   = 6 * time.Second
)

type syncParams struct {
	Authors []string         `json:"authors"`
	Kinds   []nostr.Kind     `json:"kinds"`
	Since   *nostr.Timestamp `json:"since"`
	Until   *nostr.Timestamp `json:"until"`
	Limit   int              `json:"limit"`
}

type syncResult struct {
	Success   bool   `json:"success"`
	NewEvents int    `json:"newEvents"`
	Error     string `json:"error,omitempty"`
}

// every REQ a sync opens, across all calls from every napp, holds one slot
var syncReqSlots = make(chan struct{}, 16)

func withReqSlot[T any](fn func() T) T {
	syncReqSlots <- struct{}{}
	defer func() { <-syncReqSlots }()
	return fn()
}

// ─── bounds ────────────────────────────────────────────────────

type syncBound [2]nostr.Timestamp // oldest, newest

func syncBoundKey(author nostr.PubKey, kind nostr.Kind) []byte {
	key := make([]byte, 1+32+2)
	key[0] = 'B'
	copy(key[1:], author[:])
	binary.BigEndian.PutUint16(key[33:], uint16(kind))
	return key
}

func loadSyncBound(author nostr.PubKey, kind nostr.Kind) (syncBound, bool) {
	data, _ := sys.KVStore.Get(syncBoundKey(author, kind))
	if len(data) != 16 {
		return syncBound{}, false
	}
	return syncBound{
		nostr.Timestamp(binary.BigEndian.Uint64(data[0:8])),
		nostr.Timestamp(binary.BigEndian.Uint64(data[8:16])),
	}, true
}

// extendBound records [lo, hi] as covered for this pair, answering the bound
// now held: the union when they touch, else the newer of the two (one bound
// cannot hold a gap).
func extendBound(author nostr.PubKey, kind nostr.Kind, lo, hi nostr.Timestamp) syncBound {
	next := syncBound{lo, hi}
	if b, ok := loadSyncBound(author, kind); ok {
		if lo <= b[1] && hi >= b[0] {
			next = syncBound{min(lo, b[0]), max(hi, b[1])}
		} else if hi <= b[1] {
			next = b
		}
	}
	data := make([]byte, 16)
	binary.BigEndian.PutUint64(data[0:8], uint64(next[0]))
	binary.BigEndian.PutUint64(data[8:16], uint64(next[1]))
	if err := sys.KVStore.Set(syncBoundKey(author, kind), data); err != nil {
		log.Warn().Err(err).Msg("failed to save sync bound")
	}
	return next
}

// Calls for the same (author, kind) run one after the other, so the second
// sees what the first brought and asks only for what is still missing.
var pairLocks = xsync.NewMapOf[string, *pairLock]()

type pairLock struct {
	sync.Mutex
	users int
}

func lockPair(author nostr.PubKey, kind nostr.Kind) func() {
	key := fmt.Sprintf("%x:%d", author[:], kind)
	l, _ := pairLocks.Compute(key, func(l *pairLock, loaded bool) (*pairLock, bool) {
		if !loaded {
			l = &pairLock{}
		}
		l.users++
		return l, false
	})

	l.Lock()
	return func() {
		l.Unlock()
		pairLocks.Compute(key, func(l *pairLock, _ bool) (*pairLock, bool) {
			l.users--
			return l, l.users == 0
		})
	}
}

// ─── sync ──────────────────────────────────────────────────────

// napp.sync's params as nostrapps reads them: with neither since nor limit,
// just the latest few.
func runSync(ctx context.Context, p syncParams) syncResult {
	authors := make([]nostr.PubKey, 0, len(p.Authors))
	for _, s := range p.Authors {
		if pk, err := nostr.PubKeyFromHex(strings.TrimSpace(s)); err == nil {
			authors = nostr.AppendUnique(authors, pk)
		}
	}
	kinds := make([]nostr.Kind, 0, len(p.Kinds))
	for _, k := range p.Kinds {
		kinds = nostr.AppendUnique(kinds, k)
	}
	if len(authors) == 0 || len(kinds) == 0 {
		return syncResult{Error: "sync: no valid authors or kinds"}
	}

	// nothing past now can be known complete
	until := nostr.Now()
	if p.Until != nil && *p.Until < until {
		until = *p.Until
	}
	limit := p.Limit
	if limit <= 0 {
		limit = 0
		if p.Since == nil {
			limit = 10
		}
	}
	if p.Since != nil && *p.Since > until {
		return syncResult{Success: true}
	}

	var newEvents atomic.Int64
	onEvent := func(evt nostr.Event) {
		if isNew, _ := saveEvent(evt); isNew {
			newEvents.Add(1)
		}
	}

	var errsMu sync.Mutex
	var errs []string
	var wg sync.WaitGroup
	for _, author := range authors {
		for _, kind := range kinds {
			wg.Add(1)
			go func() {
				defer wg.Done()
				unlock := lockPair(author, kind)
				defer unlock()
				var err error
				if limit == 0 {
					err = syncPair(ctx, author, kind, *p.Since, until, onEvent)
				} else {
					err = syncPairLatest(ctx, author, kind, p.Since, until, limit, onEvent)
				}
				if err != nil {
					errsMu.Lock()
					errs = append(errs, fmt.Sprintf("%s/%d: %s", author.Hex()[:8], kind, err))
					errsMu.Unlock()
				}
			}()
		}
	}
	wg.Wait()

	res := syncResult{Success: len(errs) == 0, NewEvents: int(newEvents.Load())}
	if len(errs) > 3 {
		res.Error = fmt.Sprintf("%s (and %d more)", strings.Join(errs[:3], "; "), len(errs)-3)
	} else if len(errs) > 0 {
		res.Error = strings.Join(errs, "; ")
	}
	return res
}

func syncPair(
	ctx context.Context,
	author nostr.PubKey,
	kind nostr.Kind,
	since, until nostr.Timestamp,
	onEvent func(nostr.Event),
) error {
	// the ranges to ask for, newest first
	var segments []syncBound
	if b, ok := loadSyncBound(author, kind); !ok {
		segments = append(segments, syncBound{since, until})
	} else {
		// what lies above the bound, and what lies below it
		if until > b[1] {
			segments = append(segments, syncBound{b[1], until})
		}
		if since < b[0] {
			segments = append(segments, syncBound{since, b[0]})
		}
	}
	if len(segments) == 0 {
		return nil
	}

	relays := sys.FetchOutboxRelays(ctx, author, 2)
	var failures []string
	for _, seg := range segments {
		coveredDownTo, ok := fetchRange(ctx, relays, author, kind, seg[0], seg[1], 0, onEvent)
		if !ok {
			failures = append(failures, fmt.Sprintf("no relay answered for %d–%d", seg[0], seg[1]))
			continue
		}
		extendBound(author, kind, coveredDownTo, seg[1])
	}
	if len(failures) > 0 {
		return fmt.Errorf("%s", strings.Join(failures, ", "))
	}
	return nil
}

// syncPairLatest brings the newest limit events at or before until (and not
// before since), asking the relays only for what the store can't already
// prove it has: whatever is above the bound first, then one REQ for the
// missing ones below it.
func syncPairLatest(
	ctx context.Context,
	author nostr.PubKey,
	kind nostr.Kind,
	since *nostr.Timestamp,
	until nostr.Timestamp,
	limit int,
	onEvent func(nostr.Event),
) error {
	b, hasBound := loadSyncBound(author, kind)
	var relays []string

	// bring the bound up to until, but no further down than limit events
	if hasBound && until > b[1] {
		relays = sys.FetchOutboxRelays(ctx, author, 2)
		coveredDownTo, ok := fetchRange(ctx, relays, author, kind, b[1], until, limit, onEvent)
		if !ok {
			return fmt.Errorf("no relay answered for %d–%d", b[1], until)
		}
		b = extendBound(author, kind, coveredDownTo, until)
	}

	// [oldestBound, until] is in the store; below it is unknown
	held := hasBound && b[0] <= until && until <= b[1]
	oldestBound := until
	if held {
		oldestBound = b[0]
	}
	if held && since != nil && *since >= oldestBound {
		return nil
	}

	have := 0
	if held {
		for range sys.Store.QueryEvents(nostr.Filter{
			Authors: []nostr.PubKey{author},
			Kinds:   []nostr.Kind{kind},
			Since:   oldestBound,
			Until:   until,
		}, limit) {
			have++
		}
	}
	if have >= limit {
		return nil
	}

	// below the oldest second already fetched; with no bound, from until itself
	top := until
	if held {
		if oldestBound == 0 {
			return nil
		}
		top = oldestBound - 1
	}
	if relays == nil {
		relays = sys.FetchOutboxRelays(ctx, author, 2)
	}
	reached, ok := fetchLatest(ctx, relays, author, kind, since, top, limit-have, onEvent)
	if !ok {
		return fmt.Errorf("no relay answered below %d", top)
	}
	extendBound(author, kind, reached, until)
	return nil
}

// fetchRange asks each relay for everything between lo and hi (inclusive),
// paging down from hi, and saves each event as it comes. It answers how far
// down the range is known complete: the least any answering relay got to (so
// nothing one of them still had is claimed), and false when none answered.
// With want > 0, a relay stops once it has sent that many, and is then taken
// to reach down to the oldest second it sent.
func fetchRange(
	ctx context.Context,
	relays []string,
	author nostr.PubKey,
	kind nostr.Kind,
	lo, hi nostr.Timestamp,
	want int,
	onEvent func(nostr.Event),
) (nostr.Timestamp, bool) {
	collect := collector(onEvent)

	coverages := make([]*nostr.Timestamp, len(relays))
	var wg sync.WaitGroup
	for i, url := range relays {
		wg.Add(1)
		go func() {
			defer wg.Done()
			coverages[i] = fetchRangeFrom(ctx, url, author, kind, lo, hi, want, collect)
		}()
	}
	wg.Wait()

	var covered *nostr.Timestamp
	for _, c := range coverages {
		if c != nil && (covered == nil || *c > *covered) {
			covered = c
		}
	}
	if covered == nil {
		return 0, false
	}
	return *covered, true
}

func fetchRangeFrom(
	ctx context.Context,
	url string,
	author nostr.PubKey,
	kind nostr.Kind,
	lo, hi nostr.Timestamp,
	want int,
	collect func(nostr.Event),
) *nostr.Timestamp {
	relay, err := sys.Pool.EnsureRelay(url)
	if err != nil {
		return nil
	}

	until := hi
	var downTo *nostr.Timestamp
	got := 0
	for p := 0; p < syncMaxPages && (want == 0 || got < want); p++ {
		limit := syncPageLimit
		if want > 0 {
			limit = min(limit, want-got)
		}
		count, oldest, ok := withReqSlot(func() pageResult {
			return fetchPage(ctx, relay, nostr.Filter{
				Authors: []nostr.PubKey{author},
				Kinds:   []nostr.Kind{kind},
				Since:   lo,
				Until:   until,
				Limit:   limit,
			}, collect)
		}).unpack()
		if !ok {
			// timed out or CLOSED: keep what earlier pages proved
			return downTo
		}
		// only an empty page means the relay has nothing more: a short one
		// may be the relay capping our limit silently
		if count == 0 {
			return &lo
		}
		if oldest >= until {
			// all from the same second we already asked down to
			if count < limit {
				return &lo
			}
			return downTo
		}
		got += count
		downTo = &oldest
		if oldest == 0 {
			return &lo
		}
		until = oldest - 1
	}
	// stopped at want: down to the oldest second it sent, so the next REQ
	// asks below it
	if want > 0 && got >= want {
		return &until
	}
	return downTo
}

// fetchLatest sends one REQ for the newest limit at or before until (and not
// before since) to each relay. It answers the second the store now reaches
// down to: the newest of the oldest each answering relay sent (since, or 0,
// from one that sent nothing), and false when none answered.
func fetchLatest(
	ctx context.Context,
	relays []string,
	author nostr.PubKey,
	kind nostr.Kind,
	since *nostr.Timestamp,
	until nostr.Timestamp,
	limit int,
	onEvent func(nostr.Event),
) (nostr.Timestamp, bool) {
	collect := collector(onEvent)
	filter := nostr.Filter{
		Authors: []nostr.PubKey{author},
		Kinds:   []nostr.Kind{kind},
		Until:   until,
		Limit:   limit,
	}
	floor := nostr.Timestamp(0)
	if since != nil {
		filter.Since = *since
		floor = *since
	}

	reached := make([]*nostr.Timestamp, len(relays))
	var wg sync.WaitGroup
	for i, url := range relays {
		wg.Add(1)
		go func() {
			defer wg.Done()
			relay, err := sys.Pool.EnsureRelay(url)
			if err != nil {
				return
			}
			count, oldest, ok := withReqSlot(func() pageResult {
				return fetchPage(ctx, relay, filter, collect)
			}).unpack()
			if !ok {
				return
			}
			if count == 0 {
				reached[i] = &floor
			} else {
				reached[i] = &oldest
			}
		}()
	}
	wg.Wait()

	var newest *nostr.Timestamp
	for _, r := range reached {
		if r != nil && (newest == nil || *r > *newest) {
			newest = r
		}
	}
	if newest == nil {
		return 0, false
	}
	return *newest, true
}

// collector saves what a fetch brings, once per event however many relays
// send it.
func collector(onEvent func(nostr.Event)) func(nostr.Event) {
	seen := xsync.NewMapOf[nostr.ID, struct{}]()
	return func(evt nostr.Event) {
		if _, dup := seen.LoadOrStore(evt.ID, struct{}{}); !dup {
			onEvent(evt)
		}
	}
}

type pageResult struct {
	count  int
	oldest nostr.Timestamp
	ok     bool
}

func (r pageResult) unpack() (int, nostr.Timestamp, bool) { return r.count, r.oldest, r.ok }

// fetchPage is one REQ to one relay until EOSE; not ok when it timed out or
// the relay closed it.
func fetchPage(ctx context.Context, relay *nostr.Relay, filter nostr.Filter, collect func(nostr.Event)) pageResult {
	ctx, cancel := context.WithTimeout(ctx, syncMaxWait)
	defer cancel()

	sub, err := relay.Subscribe(ctx, filter, nostr.SubscriptionOptions{Label: "napp-sync"})
	if err != nil {
		return pageResult{}
	}
	defer sub.Unsub()

	res := pageResult{oldest: nostr.Timestamp(1<<62 - 1)}
	for {
		select {
		case <-ctx.Done():
			return pageResult{}
		case <-sub.ClosedReason:
			return pageResult{}
		case <-sub.EndOfStoredEvents:
			res.ok = true
			return res
		case evt, more := <-sub.Events:
			if !more {
				return pageResult{}
			}
			// a relay that ignores the filter doesn't get into the store
			if !filter.Matches(evt) {
				continue
			}
			res.count++
			if evt.CreatedAt < res.oldest {
				res.oldest = evt.CreatedAt
			}
			collect(evt)
		}
	}
}
