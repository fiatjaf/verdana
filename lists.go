package main

import (
	"context"
	"encoding/binary"
	"strconv"
	"strings"
	"sync"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip11"
	"fiatjaf.com/nostr/nip19"
	"fiatjaf.com/nostr/sdk"
	"fiatjaf.com/nostr/sdk/hints"
)

// The launcher promises napps the same data-loading surface the web launcher
// gets from @nostr/gadgets, so the shapes here mirror that package's items
// exactly (see env.d.ts): a napp written against one runs on the other.
//
// Underneath it's all the sdk: sys.Store is the local eventstore every
// fetched event lands in, sys.KVStore remembers when we last went to the
// network for a given kind+author, sys.Hints/FetchOutboxRelays decide where
// to ask, and a small in-memory cache absorbs the repeat calls napps make
// while rendering.

const (
	listCacheTTL     = 6 * time.Hour
	listRefreshAfter = 3 * 24 * 60 * 60 // seconds before we ask relays again
)

type replCacheEntry struct {
	events  []nostr.Event
	expires time.Time
}

var (
	replCacheMu sync.Mutex
	replCache   = make(map[string]replCacheEntry)
	// one fetch at a time per kind+author, so a napp rendering a list of
	// profiles doesn't open the same subscription twenty times
	replLocks sync.Map // string -> *sync.Mutex
)

func replCacheKey(kind nostr.Kind, pubkey nostr.PubKey) string {
	return strconv.Itoa(int(kind)) + ":" + pubkey.Hex()
}

func replLock(key string) *sync.Mutex {
	v, _ := replLocks.LoadOrStore(key, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// lastFetchKey namespaces our own "when did we last try" marks so they don't
// collide with the sdk's.
func lastFetchKey(kind nostr.Kind, pubkey nostr.PubKey) []byte {
	key := make([]byte, 0, 3+2+8)
	key = append(key, 'v', 'd', 'n')
	key = binary.BigEndian.AppendUint16(key, uint16(kind))
	key = append(key, pubkey[0:8]...)
	return key
}

func triedRecently(kind nostr.Kind, pubkey nostr.PubKey) bool {
	if sys.KVStore == nil {
		return false
	}
	data, _ := sys.KVStore.Get(lastFetchKey(kind, pubkey))
	if data == nil || len(data) < 4 {
		return false
	}
	last := nostr.Timestamp(binary.BigEndian.Uint32(data))
	return nostr.Now()-last < listRefreshAfter
}

func markTried(kind nostr.Kind, pubkey nostr.PubKey) {
	if sys.KVStore == nil {
		return
	}
	buf := binary.BigEndian.AppendUint32(nil, uint32(nostr.Now()))
	sys.KVStore.Set(lastFetchKey(kind, pubkey), buf)
}

// relaysForKind is where to ask for a given replaceable kind: the author's
// outbox, plus the sdk's dedicated streams for the kinds that have them.
func relaysForKind(ctx context.Context, kind nostr.Kind, pubkey nostr.PubKey) []string {
	relays := make([]string, 0, 5)
	for _, url := range sys.FetchOutboxRelays(ctx, pubkey, 3) {
		relays = nostr.AppendUnique(relays, url)
	}
	switch kind {
	case 0:
		relays = nostr.AppendUnique(relays, sys.MetadataRelays.Next())
	case 3:
		relays = nostr.AppendUnique(relays, sys.FollowListRelays.Next())
	case 10002:
		relays = nostr.AppendUnique(relays, sys.RelayListRelays.Next())
	}
	if len(relays) < 2 {
		relays = nostr.AppendUnique(relays, sys.FallbackRelays.Next())
	}
	return relays
}

// loadEvents fetches every event of an author for one replaceable (one event)
// or addressable (one per "d" tag) kind. It answers from the in-memory cache,
// then the local eventstore, and only then goes to relays — and whatever it
// gets from relays is stored locally for next time.
func loadReplaceables(ctx context.Context, kind nostr.Kind, pubkey nostr.PubKey, addressable bool) []nostr.Event {
	if sys == nil {
		return nil
	}
	key := replCacheKey(kind, pubkey)

	lock := replLock(key)
	lock.Lock()
	defer lock.Unlock()

	replCacheMu.Lock()
	entry, ok := replCache[key]
	replCacheMu.Unlock()
	if ok && time.Now().Before(entry.expires) {
		return entry.events
	}

	maxLocal := 1
	if addressable {
		maxLocal = 200
	}
	stored := make([]nostr.Event, 0, maxLocal)
	for evt := range sys.Store.QueryEvents(nostr.Filter{
		Kinds:   []nostr.Kind{kind},
		Authors: []nostr.PubKey{pubkey},
	}, maxLocal) {
		stored = append(stored, evt)
	}

	if len(stored) > 0 && triedRecently(kind, pubkey) {
		replCacheMu.Lock()
		replCache[key] = replCacheEntry{events: stored, expires: time.Now().Add(listCacheTTL)}
		replCacheMu.Unlock()
		return stored
	}

	fetched := fetchReplaceablesFromRelays(ctx, kind, pubkey, addressable)
	markTried(kind, pubkey)

	result := stored
	if len(fetched) > 0 {
		result = mergeNewest(stored, fetched, addressable)
		for _, evt := range fetched {
			if _, err := sys.Store.ReplaceEvent(evt); err != nil {
				log.Debug().Err(err).Uint16("kind", uint16(kind)).Msg("failed to store fetched event")
			}
		}
	}

	replCacheMu.Lock()
	replCache[key] = replCacheEntry{events: result, expires: time.Now().Add(listCacheTTL)}
	replCacheMu.Unlock()
	return result
}

func fetchReplaceablesFromRelays(ctx context.Context, kind nostr.Kind, pubkey nostr.PubKey, addressable bool) []nostr.Event {
	relays := relaysForKind(ctx, kind, pubkey)
	if len(relays) == 0 {
		return nil
	}
	fetchCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()

	filter := nostr.Filter{Kinds: []nostr.Kind{kind}, Authors: []nostr.PubKey{pubkey}}
	out := make([]nostr.Event, 0, 4)
	sys.Pool.FetchManyReplaceable(fetchCtx, relays, filter, nostr.SubscriptionOptions{
		Label: "napp-list-" + strconv.Itoa(int(kind)),
	}).Range(func(_ nostr.ReplaceableKey, evt nostr.Event) bool {
		out = append(out, evt)
		return true
	})
	if !addressable && len(out) > 1 {
		newest := out[0]
		for _, evt := range out[1:] {
			if evt.CreatedAt > newest.CreatedAt {
				newest = evt
			}
		}
		out = []nostr.Event{newest}
	}
	return out
}

// mergeNewest keeps, per "d" tag (or just one for replaceables), whichever of
// the stored and the fetched events is newer.
func mergeNewest(stored, fetched []nostr.Event, addressable bool) []nostr.Event {
	best := make(map[string]nostr.Event, len(stored)+len(fetched))
	order := make([]string, 0, len(stored)+len(fetched))
	add := func(evt nostr.Event) {
		d := ""
		if addressable {
			d = evt.Tags.GetD()
		}
		if prev, ok := best[d]; ok {
			if evt.CreatedAt > prev.CreatedAt {
				best[d] = evt
			}
			return
		}
		best[d] = evt
		order = append(order, d)
	}
	for _, evt := range stored {
		add(evt)
	}
	for _, evt := range fetched {
		add(evt)
	}
	out := make([]nostr.Event, 0, len(order))
	for _, d := range order {
		out = append(out, best[d])
	}
	return out
}

// invalidateList drops the cached list for a kind+author. Called after
// publishing so the next load* reflects what the napp just wrote without
// waiting for the relays to echo it back.
func invalidateList(kind nostr.Kind, pubkey nostr.PubKey) {
	replCacheMu.Lock()
	delete(replCache, replCacheKey(kind, pubkey))
	replCacheMu.Unlock()

	// the sdk keeps its own caches for the kinds it loads itself
	switch kind {
	case 0:
		if sys.MetadataCache != nil {
			sys.MetadataCache.Delete(pubkey)
		}
	case 3:
		if sys.FollowListCache != nil {
			sys.FollowListCache.Delete(pubkey)
		}
	case 10002:
		if sys.RelayListCache != nil {
			sys.RelayListCache.Delete(pubkey)
		}
	}
}

// ─── list results ────────────────────────────────────────────────

// listResult is the { event, items } shape napps get from every load*.
func listResult(evt *nostr.Event, items []any) map[string]any {
	if items == nil {
		items = []any{}
	}
	return map[string]any{"event": evt, "items": items}
}

func emptyListResult() map[string]any { return listResult(nil, nil) }

func newestOf(events []nostr.Event) *nostr.Event {
	if len(events) == 0 {
		return nil
	}
	newest := &events[0]
	for i := range events[1:] {
		if events[i+1].CreatedAt > newest.CreatedAt {
			newest = &events[i+1]
		}
	}
	return newest
}

// loadList fetches a NIP-51 list and turns its tags into items with the given
// per-tag parser (exactly how gadgets' itemsFromTags works).
func loadList(ctx context.Context, kind nostr.Kind, pubkey nostr.PubKey, parse func(nostr.Tag) (any, bool)) map[string]any {
	evt := newestOf(loadReplaceables(ctx, kind, pubkey, false))
	if evt == nil {
		return emptyListResult()
	}
	return listResult(evt, itemsFromTags(*evt, parse))
}

func itemsFromTags(evt nostr.Event, parse func(nostr.Tag) (any, bool)) []any {
	items := make([]any, 0, len(evt.Tags))
	for _, tag := range evt.Tags {
		if item, ok := parse(tag); ok {
			items = append(items, item)
		}
	}
	return items
}

// ─── item parsers (mirroring @nostr/gadgets/lists) ───────────────

func pubkeyItem(tagName string) func(nostr.Tag) (any, bool) {
	return func(tag nostr.Tag) (any, bool) {
		if len(tag) >= 2 && tag[0] == tagName && isHex64(tag[1]) {
			return strings.ToLower(tag[1]), true
		}
		return nil, false
	}
}

func relayURLItem(tag nostr.Tag) (any, bool) {
	if len(tag) >= 2 && tag[0] == "relay" && tag[1] != "" {
		return nostr.NormalizeURL(tag[1]), true
	}
	return nil, false
}

// addressPointer is the { identifier, pubkey, kind, relays } item shape.
func addressPointer(coord string, hint string, wantKind int) (any, bool) {
	spl := strings.SplitN(coord, ":", 3)
	if len(spl) < 3 || !isHex64(spl[1]) {
		return nil, false
	}
	kind, err := strconv.Atoi(spl[0])
	if err != nil {
		return nil, false
	}
	if wantKind >= 0 && kind != wantKind {
		return nil, false
	}
	relays := []string{}
	if hint != "" {
		relays = append(relays, hint)
	}
	return map[string]any{
		"identifier": spl[2],
		"pubkey":     strings.ToLower(spl[1]),
		"kind":       kind,
		"relays":     relays,
	}, true
}

func hint(tag nostr.Tag, i int) string {
	if len(tag) > i {
		return tag[i]
	}
	return ""
}

func relayListItems(evt nostr.Event) []any {
	return itemsFromTags(evt, func(tag nostr.Tag) (any, bool) {
		if len(tag) < 2 || tag[0] != "r" || tag[1] == "" {
			return nil, false
		}
		url := nostr.NormalizeURL(tag[1])
		switch {
		case len(tag) == 2:
			return map[string]any{"url": url, "read": true, "write": true}, true
		case tag[2] == "read":
			return map[string]any{"url": url, "read": true, "write": false}, true
		case tag[2] == "write":
			return map[string]any{"url": url, "read": false, "write": true}, true
		}
		return nil, false
	})
}

// ─── the load* surface ───────────────────────────────────────────

func loadRelayList(ctx context.Context, pubkey nostr.PubKey) map[string]any {
	// kind:10002 has the sdk's own dedicated relay stream and cache, and it
	// is what every outbox decision downstream depends on.
	rl := sys.FetchRelayList(ctx, pubkey)
	if rl.Event == nil {
		return emptyListResult()
	}
	return listResult(rl.Event, relayListItems(*rl.Event))
}

func loadFollowsList(ctx context.Context, pubkey nostr.PubKey) map[string]any {
	fl := sys.FetchFollowList(ctx, pubkey)
	if fl.Event == nil {
		return emptyListResult()
	}
	return listResult(fl.Event, itemsFromTags(*fl.Event, pubkeyItem("p")))
}

func loadMuteList(ctx context.Context, pubkey nostr.PubKey) map[string]any {
	return loadList(ctx, 10000, pubkey, func(tag nostr.Tag) (any, bool) {
		if len(tag) < 2 {
			return nil, false
		}
		switch tag[0] {
		case "p":
			if isHex64(tag[1]) {
				return map[string]any{"label": "pubkey", "value": strings.ToLower(tag[1])}, true
			}
		case "e":
			if isHex64(tag[1]) {
				return map[string]any{"label": "thread", "value": strings.ToLower(tag[1])}, true
			}
		case "t":
			return map[string]any{"label": "hashtag", "value": tag[1]}, true
		case "word":
			return map[string]any{"label": "word", "value": tag[1]}, true
		}
		return nil, false
	})
}

func loadBookmarks(ctx context.Context, pubkey nostr.PubKey) map[string]any {
	return loadList(ctx, 10003, pubkey, func(tag nostr.Tag) (any, bool) {
		if len(tag) >= 2 && (tag[0] == "e" || tag[0] == "a") && tag[1] != "" {
			return tag[1], true
		}
		return nil, false
	})
}

func loadPins(ctx context.Context, pubkey nostr.PubKey) map[string]any {
	return loadList(ctx, 10001, pubkey, func(tag nostr.Tag) (any, bool) {
		if len(tag) >= 2 && tag[0] == "e" && tag[1] != "" {
			return tag[1], true
		}
		return nil, false
	})
}

func loadBlossomServers(ctx context.Context, pubkey nostr.PubKey) map[string]any {
	return loadList(ctx, 10063, pubkey, func(tag nostr.Tag) (any, bool) {
		if len(tag) >= 2 && tag[0] == "server" && tag[1] != "" {
			url, err := nostr.NormalizeHTTPURL(tag[1])
			if err != nil {
				return nil, false
			}
			return url, true
		}
		return nil, false
	})
}

func loadEmojis(ctx context.Context, pubkey nostr.PubKey) map[string]any {
	return loadList(ctx, 10030, pubkey, func(tag nostr.Tag) (any, bool) {
		if len(tag) < 2 {
			return nil, false
		}
		if tag[0] == "a" {
			return addressPointer(tag[1], hint(tag, 2), 30030)
		}
		if len(tag) >= 3 && tag[0] == "emoji" {
			return map[string]any{"shortcode": tag[1], "url": tag[2]}, true
		}
		return nil, false
	})
}

func loadFavoriteRelays(ctx context.Context, pubkey nostr.PubKey) map[string]any {
	return loadList(ctx, 10012, pubkey, func(tag nostr.Tag) (any, bool) {
		if len(tag) < 2 {
			return nil, false
		}
		switch tag[0] {
		case "relay":
			return nostr.NormalizeURL(tag[1]), true
		case "a":
			return addressPointer(tag[1], hint(tag, 2), 30002)
		}
		return nil, false
	})
}

func loadBlockedRelays(ctx context.Context, pubkey nostr.PubKey) map[string]any {
	return loadList(ctx, 10006, pubkey, relayURLItem)
}

func loadSearchRelays(ctx context.Context, pubkey nostr.PubKey) map[string]any {
	return loadList(ctx, 10007, pubkey, relayURLItem)
}

func loadDmRelays(ctx context.Context, pubkey nostr.PubKey) map[string]any {
	return loadList(ctx, 10050, pubkey, relayURLItem)
}

func loadWikiAuthors(ctx context.Context, pubkey nostr.PubKey) map[string]any {
	return loadList(ctx, 10101, pubkey, pubkeyItem("p"))
}

func loadWikiRelays(ctx context.Context, pubkey nostr.PubKey) map[string]any {
	return loadList(ctx, 10102, pubkey, relayURLItem)
}

func loadFavoriteFollowSets(ctx context.Context, pubkey nostr.PubKey) map[string]any {
	return loadList(ctx, 10021, pubkey, func(tag nostr.Tag) (any, bool) {
		if len(tag) >= 2 && tag[0] == "a" {
			return addressPointer(tag[1], hint(tag, 2), 30000)
		}
		return nil, false
	})
}

func loadFavoriteScrolls(ctx context.Context, pubkey nostr.PubKey) map[string]any {
	return loadList(ctx, 10027, pubkey, func(tag nostr.Tag) (any, bool) {
		if len(tag) < 2 || tag[0] != "e" || !isHex64(tag[1]) {
			return nil, false
		}
		item := map[string]any{"id": strings.ToLower(tag[1]), "kind": 1227}
		if h := hint(tag, 2); h != "" {
			item["relays"] = []string{nostr.NormalizeURL(h)}
		}
		if a := hint(tag, 3); isHex64(a) {
			item["author"] = strings.ToLower(a)
		}
		return item, true
	})
}

func loadProfileBadges(ctx context.Context, pubkey nostr.PubKey) map[string]any {
	return loadList(ctx, 10008, pubkey, func(tag nostr.Tag) (any, bool) {
		if len(tag) < 2 {
			return nil, false
		}
		switch tag[0] {
		case "a":
			return addressPointer(tag[1], hint(tag, 2), -1)
		case "e":
			if isHex64(tag[1]) {
				return strings.ToLower(tag[1]), true
			}
		}
		return nil, false
	})
}

func loadSimpleGroups(ctx context.Context, pubkey nostr.PubKey) map[string]any {
	return loadList(ctx, 10009, pubkey, func(tag nostr.Tag) (any, bool) {
		if len(tag) >= 3 && tag[0] == "group" {
			item := map[string]any{"groupId": tag[1], "relay": nostr.NormalizeURL(tag[2])}
			if name := hint(tag, 3); name != "" {
				item["name"] = name
			}
			return item, true
		}
		return nil, false
	})
}

func loadGitAuthors(ctx context.Context, pubkey nostr.PubKey) map[string]any {
	return loadList(ctx, 10017, pubkey, pubkeyItem("p"))
}

func loadGitRepositories(ctx context.Context, pubkey nostr.PubKey) map[string]any {
	return loadList(ctx, 10018, pubkey, func(tag nostr.Tag) (any, bool) {
		if len(tag) >= 2 && tag[0] == "a" && tag[1] != "" {
			return tag[1], true
		}
		return nil, false
	})
}

func loadMediaFollows(ctx context.Context, pubkey nostr.PubKey) map[string]any {
	return loadList(ctx, 10020, pubkey, pubkeyItem("p"))
}

func loadFavoritePodcasts(ctx context.Context, pubkey nostr.PubKey) map[string]any {
	return loadList(ctx, 10054, pubkey, func(tag nostr.Tag) (any, bool) {
		if len(tag) < 2 {
			return nil, false
		}
		if tag[0] == "p" && isHex64(tag[1]) {
			return strings.ToLower(tag[1]), true
		}
		if tag[0] == "url" {
			return tag[1], true
		}
		return nil, false
	})
}

func loadAuthoredPodcasts(ctx context.Context, pubkey nostr.PubKey) map[string]any {
	return loadList(ctx, 10064, pubkey, pubkeyItem("p"))
}

// ─── addressable sets ────────────────────────────────────────────

// loadSets returns the gadgets SetResult shape: { [dTag]: ResolvedSet }.
func loadSets(ctx context.Context, kind nostr.Kind, pubkey nostr.PubKey, parse func(nostr.Tag) (any, bool)) map[string]any {
	events := loadReplaceables(ctx, kind, pubkey, true)
	out := make(map[string]any, len(events))
	for i := range events {
		evt := events[i]
		d := evt.Tags.GetD()
		if prev, ok := out[d]; ok {
			if pm, ok := prev.(map[string]any); ok {
				if pe, ok := pm["event"].(*nostr.Event); ok && pe.CreatedAt >= evt.CreatedAt {
					continue
				}
			}
		}
		out[d] = resolvedSet(evt, kind, d, itemsFromTags(evt, parse))
	}
	return out
}

func resolvedSet(evt nostr.Event, kind nostr.Kind, d string, items []any) map[string]any {
	if items == nil {
		items = []any{}
	}
	title := tagValue(evt.Tags, "title")
	if title == "" {
		title = tagValue(evt.Tags, "name")
	}
	if title == "" {
		title = d
	}
	set := map[string]any{
		"pointer": map[string]any{
			"identifier": d,
			"pubkey":     evt.PubKey.Hex(),
			"kind":       int(kind),
			"relays":     []string{},
		},
		"event": &evt,
		"items": items,
		"title": title,
	}
	if image := tagValue(evt.Tags, "image"); image != "" {
		set["image"] = image
	}
	if desc := tagValue(evt.Tags, "description"); desc != "" {
		set["description"] = desc
	}
	return set
}

func loadFollowSets(ctx context.Context, pubkey nostr.PubKey) map[string]any {
	return loadSets(ctx, 30000, pubkey, pubkeyItem("p"))
}

func loadRelaySets(ctx context.Context, pubkey nostr.PubKey) map[string]any {
	return loadSets(ctx, 30002, pubkey, relayURLItem)
}

func loadEmojiSets(ctx context.Context, pubkey nostr.PubKey) map[string]any {
	return loadSets(ctx, 30030, pubkey, func(tag nostr.Tag) (any, bool) {
		if len(tag) >= 3 && tag[0] == "emoji" {
			return map[string]any{"shortcode": tag[1], "url": tag[2]}, true
		}
		return nil, false
	})
}

// ─── composite helpers ───────────────────────────────────────────

// resolveSetPointer fetches the addressable event an "a" item points at and
// shapes it as a ResolvedSet, so a napp gets the set's contents inline.
func resolveSetPointer(ctx context.Context, item any, parse func(nostr.Tag) (any, bool)) (any, bool) {
	m, ok := item.(map[string]any)
	if !ok {
		return nil, false
	}
	pubkeyHex, _ := m["pubkey"].(string)
	identifier, _ := m["identifier"].(string)
	kindNum, _ := m["kind"].(int)
	pk, err := nostr.PubKeyFromHex(pubkeyHex)
	if err != nil {
		return nil, false
	}
	kind := nostr.Kind(kindNum)
	for _, evt := range loadReplaceables(ctx, kind, pk, true) {
		if evt.Tags.GetD() == identifier {
			return resolvedSet(evt, kind, identifier, itemsFromTags(evt, parse)), true
		}
	}
	return nil, false
}

// fetchFavoriteRelaysWithSets flattens kind:10012 into urls and resolved
// kind:30002 sets.
func fetchFavoriteRelaysWithSets(ctx context.Context, pubkey nostr.PubKey) []any {
	res := loadFavoriteRelays(ctx, pubkey)
	items, _ := res["items"].([]any)
	out := make([]any, 0, len(items))
	for _, item := range items {
		if _, isStr := item.(string); isStr {
			out = append(out, item)
			continue
		}
		if set, ok := resolveSetPointer(ctx, item, relayURLItem); ok {
			out = append(out, set)
		}
	}
	return out
}

func fetchEmojisWithSets(ctx context.Context, pubkey nostr.PubKey) []any {
	res := loadEmojis(ctx, pubkey)
	items, _ := res["items"].([]any)
	emojiTag := func(tag nostr.Tag) (any, bool) {
		if len(tag) >= 3 && tag[0] == "emoji" {
			return map[string]any{"shortcode": tag[1], "url": tag[2]}, true
		}
		return nil, false
	}
	out := make([]any, 0, len(items))
	for _, item := range items {
		if m, ok := item.(map[string]any); ok {
			if _, isEmoji := m["shortcode"]; isEmoji {
				out = append(out, item)
				continue
			}
		}
		if set, ok := resolveSetPointer(ctx, item, emojiTag); ok {
			out = append(out, set)
		}
	}
	return out
}

func fetchFavoriteFollowSetsWithSets(ctx context.Context, pubkey nostr.PubKey) []any {
	res := loadFavoriteFollowSets(ctx, pubkey)
	items, _ := res["items"].([]any)
	out := make([]any, 0, len(items))
	for _, item := range items {
		if set, ok := resolveSetPointer(ctx, item, pubkeyItem("p")); ok {
			out = append(out, set)
		}
	}
	return out
}

// ─── profile metadata ────────────────────────────────────────────

// nostrUser is the NostrUser shape from env.d.ts.
func nostrUser(pm sdk.ProfileMetadata) map[string]any {
	metadata := map[string]any{}
	if pm.Name != "" {
		metadata["name"] = pm.Name
	}
	if pm.DisplayName != "" {
		metadata["display_name"] = pm.DisplayName
	}
	if pm.About != "" {
		metadata["about"] = pm.About
	}
	if pm.Website != "" {
		metadata["website"] = pm.Website
	}
	if pm.Picture != "" {
		metadata["picture"] = pm.Picture
	}
	if pm.Banner != "" {
		metadata["banner"] = pm.Banner
	}
	if pm.NIP05 != "" {
		metadata["nip05"] = pm.NIP05
	}
	if pm.LUD16 != "" {
		metadata["lud16"] = pm.LUD16
	}

	user := map[string]any{
		"pubkey":      pm.PubKey.Hex(),
		"npub":        pm.Npub(),
		"shortName":   pm.ShortName(),
		"metadata":    metadata,
		"lastUpdated": 0,
	}
	if pm.Picture != "" {
		user["image"] = pm.Picture
	}
	if pm.Event != nil {
		user["lastUpdated"] = int64(pm.Event.CreatedAt)
	}
	return user
}

// loadNostrUser accepts a hex pubkey, npub, nprofile or nip05 and always
// answers with a NostrUser (an empty-ish one when nothing was found).
func loadNostrUser(ctx context.Context, input string, extraRelays []string) (map[string]any, error) {
	pp := sdk.InputToProfile(ctx, strings.TrimSpace(input))
	if pp == nil {
		return nil, errNotFound("could not decode " + preview(input, 40))
	}
	for _, r := range append(pp.Relays, extraRelays...) {
		if r != "" && !sdk.IsVirtualRelay(r) {
			sys.Hints.Save(pp.PublicKey, nostr.NormalizeURL(r), hints.LastInHint, nostr.Now())
		}
	}
	pm := sys.FetchProfileMetadata(ctx, pp.PublicKey)
	user := nostrUser(pm)
	indexUser(pm)
	return user, nil
}

// ─── event fetching ──────────────────────────────────────────────

func loadEvent(ctx context.Context, code string, relays []string, author string) *nostr.Event {
	code = strings.TrimSpace(code)
	if code == "" || sys == nil {
		return nil
	}

	var pointer nostr.Pointer
	if prefix, data, err := nip19.Decode(code); err == nil {
		switch prefix {
		case "nevent":
			ep := data.(nostr.EventPointer)
			ep.Relays = append(ep.Relays, relays...)
			pointer = ep
		case "naddr":
			ap := data.(nostr.EntityPointer)
			ap.Relays = append(ap.Relays, relays...)
			pointer = ap
		case "note":
			pointer = nostr.EventPointer{ID: data.(nostr.ID), Relays: relays}
		}
	}
	if pointer == nil {
		id, err := nostr.IDFromHex(code)
		if err != nil {
			log.Debug().Str("code", preview(code, 40)).Msg("loadEvent: not an event reference")
			return nil
		}
		ep := nostr.EventPointer{ID: id, Relays: relays}
		if pk, err := nostr.PubKeyFromHex(author); err == nil {
			ep.Author = pk
		}
		pointer = ep
	}

	fetchCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	evt, _, err := sys.FetchSpecificEvent(fetchCtx, pointer, sdk.FetchSpecificEventParameters{
		SaveToLocalStore: true,
	})
	if err != nil {
		log.Debug().Err(err).Str("code", preview(code, 40)).Msg("loadEvent failed")
		return nil
	}
	return evt
}

// loadEventsByID is the batched by-id fetch: the local store answers what it
// can, then one REQ over the union of the missing ids.
func loadEventsByID(ctx context.Context, ids []string) []nostr.Event {
	want := make([]nostr.ID, 0, len(ids))
	seen := make(map[nostr.ID]bool, len(ids))
	for _, raw := range ids {
		id, err := nostr.IDFromHex(strings.TrimSpace(raw))
		if err != nil || seen[id] {
			continue
		}
		seen[id] = true
		want = append(want, id)
	}
	if len(want) == 0 {
		return nil
	}

	out := make([]nostr.Event, 0, len(want))
	found := make(map[nostr.ID]bool, len(want))
	for evt := range sys.Store.QueryEvents(nostr.Filter{IDs: want}, len(want)) {
		found[evt.ID] = true
		out = append(out, evt)
	}

	missing := make([]nostr.ID, 0, len(want))
	for _, id := range want {
		if !found[id] {
			missing = append(missing, id)
		}
	}
	if len(missing) == 0 {
		return out
	}

	relays := make([]string, 0, 4)
	relays = append(relays, sys.JustIDRelays.URLs...)
	relays = nostr.AppendUnique(relays, sys.FallbackRelays.Next())

	fetchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for ie := range sys.Pool.FetchMany(fetchCtx, relays,
		nostr.Filter{IDs: missing},
		nostr.SubscriptionOptions{Label: "loadEvents"},
	) {
		sys.Publisher.Publish(fetchCtx, ie.Event)
		out = append(out, ie.Event)
	}
	return out
}

// ─── relay info ──────────────────────────────────────────────────

type relayInfoEntry struct {
	doc     map[string]any
	expires time.Time
}

var (
	relayInfoMu    sync.Mutex
	relayInfoCache = make(map[string]relayInfoEntry)
)

func loadRelayInfo(ctx context.Context, url string) map[string]any {
	url = strings.TrimSpace(url)
	if url == "" {
		return nil
	}
	normalized := nostr.NormalizeURL(url)

	relayInfoMu.Lock()
	entry, ok := relayInfoCache[normalized]
	relayInfoMu.Unlock()
	if ok && time.Now().Before(entry.expires) {
		return entry.doc
	}

	fetchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	info, err := nip11.Fetch(fetchCtx, normalized)
	if err != nil {
		log.Debug().Err(err).Str("relay", normalized).Msg("loadRelayInfo failed")
		return nil
	}

	doc := map[string]any{"url": info.URL}
	if info.Name != "" {
		doc["name"] = info.Name
	}
	if info.Description != "" {
		doc["description"] = info.Description
	}
	if info.Icon != "" {
		doc["icon"] = info.Icon
	}
	if info.Contact != "" {
		doc["contact"] = info.Contact
	}
	if info.Software != "" {
		doc["software"] = info.Software
	}
	if info.Version != "" {
		doc["version"] = info.Version
	}
	if info.PubKey != nil {
		doc["pubkey"] = info.PubKey.Hex()
	}
	if info.Self != nil {
		doc["self"] = info.Self.Hex()
	}
	if len(info.SupportedNIPs) > 0 {
		nips := make([]any, 0, len(info.SupportedNIPs))
		for _, n := range info.SupportedNIPs {
			nips = append(nips, n)
		}
		doc["supported_nips"] = nips
	}

	relayInfoMu.Lock()
	relayInfoCache[normalized] = relayInfoEntry{doc: doc, expires: time.Now().Add(6 * time.Hour)}
	relayInfoMu.Unlock()
	return doc
}

// ─── small helpers ───────────────────────────────────────────────

func isHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < 64; i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}

type errNotFound string

func (e errNotFound) Error() string { return string(e) }
