package main

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip11"
	"fiatjaf.com/nostr/nip19"
	"fiatjaf.com/nostr/sdk"
	"fiatjaf.com/nostr/sdk/cache"
	"fiatjaf.com/nostr/sdk/hints"
)

// The launcher promises napps the same data-loading surface the web launcher
// gets from @nostr/gadgets, so the item shapes here mirror that package's
// exactly (see env.d.ts): a napp written against one runs on the other.
//
// Getting the events, though, is entirely the sdk's job: every load* below
// goes through the sdk loader for its kind (sys.Fetch*List / sys.Fetch*Sets),
// which already does the local eventstore, the kvstore mark that decides when
// to hit the network again, the author's outbox relays, batched REQs and a 6h
// cache. All that is left here is turning tags into the items napps expect.

const listCacheTTL = 6 * time.Hour

// ─── getting the events (all sdk) ────────────────────────────────

// listEvent returns an author's event for a NIP-51 list kind.
func listEvent(ctx context.Context, kind nostr.Kind, pubkey nostr.PubKey) *nostr.Event {
	if sys == nil {
		return nil
	}
	switch kind {
	case 3:
		return sys.FetchFollowList(ctx, pubkey).Event
	case 10000:
		return sys.FetchMuteList(ctx, pubkey).Event
	case 10001:
		return sys.FetchPinList(ctx, pubkey).Event
	case 10002:
		return sys.FetchRelayList(ctx, pubkey).Event
	case 10003:
		return sys.FetchBookmarkList(ctx, pubkey).Event
	case 10006:
		return sys.FetchBlockedRelayList(ctx, pubkey).Event
	case 10007:
		return sys.FetchSearchRelayList(ctx, pubkey).Event
	case 10008:
		return sys.FetchProfileBadgesList(ctx, pubkey).Event
	case 10012:
		return sys.FetchRelayFeedsList(ctx, pubkey).Event
	case 10015:
		return sys.FetchTopicList(ctx, pubkey).Event
	case 10017:
		return sys.FetchGitAuthorList(ctx, pubkey).Event
	case 10018:
		return sys.FetchGitRepositoryList(ctx, pubkey).Event
	case 10020:
		return sys.FetchMediaFollowList(ctx, pubkey).Event
	case 10030:
		return sys.FetchEmojiList(ctx, pubkey).Event
	case 10050:
		return sys.FetchDMRelayList(ctx, pubkey).Event
	case 10054:
		return sys.FetchFavoritePodcastsList(ctx, pubkey).Event
	case 10064:
		return sys.FetchAuthoredPodcastsList(ctx, pubkey).Event
	case 10101:
		return sys.FetchGoodWikiAuthorList(ctx, pubkey).Event
	case 10102:
		return sys.FetchGoodWikiRelayList(ctx, pubkey).Event
	}
	return otherListEvent(ctx, kind, pubkey)
}

type otherListEntry struct {
	event   *nostr.Event
	expires time.Time
}

var (
	otherListMu    sync.Mutex
	otherListCache = make(map[string]otherListEntry)
)

// otherListEvent covers the kinds the sdk has no loader for -- 10009, 10021,
// 10027 and 10063 (its FetchBlossomServerList reads kind 10101, so we don't
// use it). It is still the sdk fetching: the local store first, then the
// author's relays, saving whatever it finds. What it lacks is the kvstore
// refresh gate, so we memo the answer for a while instead, which also keeps a
// napp rendering many rows from asking the network once per row.
func otherListEvent(ctx context.Context, kind nostr.Kind, pubkey nostr.PubKey) *nostr.Event {
	key := listCacheKey(kind, pubkey)

	otherListMu.Lock()
	entry, ok := otherListCache[key]
	otherListMu.Unlock()
	if ok && time.Now().Before(entry.expires) {
		return entry.event
	}

	fetchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	evt, _, err := sys.FetchSpecificEvent(fetchCtx,
		nostr.EntityPointer{PublicKey: pubkey, Kind: kind},
		sdk.FetchSpecificEventParameters{SaveToLocalStore: true},
	)
	if err != nil {
		log.Debug().Err(err).Uint16("kind", uint16(kind)).Msg("list fetch failed")
	}

	otherListMu.Lock()
	otherListCache[key] = otherListEntry{event: evt, expires: time.Now().Add(listCacheTTL)}
	otherListMu.Unlock()
	return evt
}

func listCacheKey(kind nostr.Kind, pubkey nostr.PubKey) string {
	return strconv.Itoa(int(kind)) + ":" + pubkey.Hex()
}

// sdkSetEvents returns an author's addressable events of a set kind, when the
// sdk has a loader for it.
func sdkSetEvents(ctx context.Context, kind nostr.Kind, pubkey nostr.PubKey) ([]nostr.Event, bool) {
	if sys == nil {
		return nil, false
	}
	switch kind {
	case 30000:
		return sys.FetchFollowSets(ctx, pubkey).Events, true
	case 30002:
		return sys.FetchRelaySets(ctx, pubkey).Events, true
	case 30015:
		return sys.FetchTopicSets(ctx, pubkey).Events, true
	case 30030:
		return sys.FetchEmojiSets(ctx, pubkey).Events, true
	}
	return nil, false
}

// setEvent is one addressable event by author+d, for resolving the "a" items
// that show up inside NIP-51 lists.
func setEvent(ctx context.Context, kind nostr.Kind, pubkey nostr.PubKey, identifier string) *nostr.Event {
	if events, ok := sdkSetEvents(ctx, kind, pubkey); ok {
		for i := range events {
			if events[i].Tags.GetD() == identifier {
				return &events[i]
			}
		}
		return nil
	}
	fetchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	evt, _, err := sys.FetchSpecificEvent(fetchCtx,
		nostr.EntityPointer{PublicKey: pubkey, Kind: kind, Identifier: identifier},
		sdk.FetchSpecificEventParameters{SaveToLocalStore: true},
	)
	if err != nil {
		log.Debug().Err(err).Uint16("kind", uint16(kind)).Msg("set fetch failed")
	}
	return evt
}

// invalidateList drops what the sdk cached for a kind+author. Called after
// publishing (the event is stored locally first), so the next load* reflects
// what the napp just wrote without waiting for the relays to echo it back.
func invalidateList(kind nostr.Kind, pubkey nostr.PubKey) {
	if sys == nil {
		return
	}

	otherListMu.Lock()
	delete(otherListCache, listCacheKey(kind, pubkey))
	otherListMu.Unlock()

	switch kind {
	case 0:
		dropCached(sys.MetadataCache, pubkey)
	case 3:
		dropCached(sys.FollowListCache, pubkey)
	case 10000:
		dropCached(sys.MuteListCache, pubkey)
	case 10001:
		dropCached(sys.PinListCache, pubkey)
	case 10002:
		dropCached(sys.RelayListCache, pubkey)
	case 10003:
		dropCached(sys.BookmarkListCache, pubkey)
	case 10006:
		dropCached(sys.BlockedRelayListCache, pubkey)
	case 10007:
		dropCached(sys.SearchRelayListCache, pubkey)
	case 10008:
		dropCached(sys.ProfileBadgesListCache, pubkey)
	case 10012:
		dropCached(sys.RelayFeedsListCache, pubkey)
	case 10015:
		dropCached(sys.TopicListCache, pubkey)
	case 10017:
		dropCached(sys.GitAuthorListCache, pubkey)
	case 10018:
		dropCached(sys.GitRepositoryListCache, pubkey)
	case 10020:
		dropCached(sys.MediaFollowListCache, pubkey)
	case 10030:
		dropCached(sys.EmojiListCache, pubkey)
	case 10050:
		dropCached(sys.DMRelayListCache, pubkey)
	case 10054:
		dropCached(sys.PodcastFavoriteListCache, pubkey)
	case 10064:
		dropCached(sys.AuthoredPodcastListCache, pubkey)
	case 10101:
		dropCached(sys.GoodWikiAuthorListCache, pubkey)
	case 10102:
		dropCached(sys.GoodWikiRelayListCache, pubkey)
	case 30000:
		dropCached(sys.FollowSetsCache, pubkey)
	case 30002:
		dropCached(sys.RelaySetsCache, pubkey)
	case 30015:
		dropCached(sys.TopicSetsCache, pubkey)
	case 30030:
		dropCached(sys.EmojiSetsCache, pubkey)
	}
}

// dropCached exists because the sdk creates each of its caches lazily.
func dropCached[V any](c cache.Cache32[V], pubkey nostr.PubKey) {
	if c != nil {
		c.Delete(pubkey)
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

// loadList fetches a NIP-51 list through the sdk and turns its tags into items
// with the given per-tag parser (exactly how gadgets' itemsFromTags works).
func loadList(ctx context.Context, kind nostr.Kind, pubkey nostr.PubKey, parse func(nostr.Tag) (any, bool)) map[string]any {
	evt := listEvent(ctx, kind, pubkey)
	if evt == nil {
		return emptyListResult()
	}
	return listResult(evt, itemsFromTags(*evt, parse))
}

// fromSDKList shapes one of the sdk's own typed lists into a list result, with
// conv mapping each sdk item to the shape napps expect. Used for the kinds
// where the sdk's parser already produces everything gadgets would.
func fromSDKList[V comparable, I sdk.TagItemWithValue[V]](
	list sdk.GenericList[V, I],
	conv func(I) any,
) map[string]any {
	if list.Event == nil {
		return emptyListResult()
	}
	items := make([]any, 0, len(list.Items))
	for _, item := range list.Items {
		if v := conv(item); v != nil {
			items = append(items, v)
		}
	}
	return listResult(list.Event, items)
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

// ─── item shapes (mirroring @nostr/gadgets/lists) ────────────────

func profileRefItem(ref sdk.ProfileRef) any { return ref.Pubkey.Hex() }

func relayURLValue(url sdk.RelayURL) any { return string(url) }

// eventRefItem is gadgets' `string | AddressPointer`: an event id as hex, an
// addressable event as a pointer object.
func eventRefItem(ref sdk.EventRef) any {
	switch p := ref.Pointer.(type) {
	case nostr.EventPointer:
		return p.ID.Hex()
	case nostr.EntityPointer:
		relays := p.Relays
		if relays == nil {
			relays = []string{}
		}
		return map[string]any{
			"identifier": p.Identifier,
			"pubkey":     p.PublicKey.Hex(),
			"kind":       int(p.Kind),
			"relays":     relays,
		}
	}
	return nil
}

func podcastRefItem(ref sdk.PodcastRef) any {
	if ref.PubKey != nostr.ZeroPK {
		return ref.PubKey.Hex()
	}
	if ref.URL == "" {
		return nil
	}
	return ref.URL
}

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

func emojiItem(tag nostr.Tag) (any, bool) {
	if len(tag) >= 3 && tag[0] == "emoji" {
		return map[string]any{"shortcode": tag[1], "url": tag[2]}, true
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
	// the sdk's Relay item carries the same information, but napps expect
	// gadgets' { url, read, write }
	rl := sys.FetchRelayList(ctx, pubkey)
	if rl.Event == nil {
		return emptyListResult()
	}
	return listResult(rl.Event, relayListItems(*rl.Event))
}

func loadFollowsList(ctx context.Context, pubkey nostr.PubKey) map[string]any {
	return fromSDKList(sys.FetchFollowList(ctx, pubkey), profileRefItem)
}

func loadMuteList(ctx context.Context, pubkey nostr.PubKey) map[string]any {
	// the sdk only keeps the muted pubkeys; gadgets (and the napps written
	// against it) want the threads, hashtags and words too
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
	// EventRef.Value() is the tag reference: an id, or a "kind:pubkey:d"
	return fromSDKList(sys.FetchBookmarkList(ctx, pubkey), func(ref sdk.EventRef) any {
		return ref.Value()
	})
}

func loadPins(ctx context.Context, pubkey nostr.PubKey) map[string]any {
	return fromSDKList(sys.FetchPinList(ctx, pubkey), func(ref sdk.EventRef) any {
		return ref.Value()
	})
}

func loadBlossomServers(ctx context.Context, pubkey nostr.PubKey) map[string]any {
	// not sys.FetchBlossomServerList: that one reads kind 10101
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
	// the sdk drops the "a" tags pointing at kind:30030 sets, which
	// fetchEmojisWithSets needs
	return loadList(ctx, 10030, pubkey, func(tag nostr.Tag) (any, bool) {
		if len(tag) < 2 {
			return nil, false
		}
		if tag[0] == "a" {
			return addressPointer(tag[1], hint(tag, 2), 30030)
		}
		return emojiItem(tag)
	})
}

func loadFavoriteRelays(ctx context.Context, pubkey nostr.PubKey) map[string]any {
	// same here: the "a" tags point at kind:30002 relay sets
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
	return fromSDKList(sys.FetchBlockedRelayList(ctx, pubkey), relayURLValue)
}

func loadSearchRelays(ctx context.Context, pubkey nostr.PubKey) map[string]any {
	return fromSDKList(sys.FetchSearchRelayList(ctx, pubkey), relayURLValue)
}

func loadDmRelays(ctx context.Context, pubkey nostr.PubKey) map[string]any {
	return fromSDKList(sys.FetchDMRelayList(ctx, pubkey), relayURLValue)
}

func loadWikiAuthors(ctx context.Context, pubkey nostr.PubKey) map[string]any {
	return fromSDKList(sys.FetchGoodWikiAuthorList(ctx, pubkey), profileRefItem)
}

func loadWikiRelays(ctx context.Context, pubkey nostr.PubKey) map[string]any {
	return fromSDKList(sys.FetchGoodWikiRelayList(ctx, pubkey), relayURLValue)
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
	return fromSDKList(sys.FetchProfileBadgesList(ctx, pubkey), eventRefItem)
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
	return fromSDKList(sys.FetchGitAuthorList(ctx, pubkey), profileRefItem)
}

func loadGitRepositories(ctx context.Context, pubkey nostr.PubKey) map[string]any {
	return fromSDKList(sys.FetchGitRepositoryList(ctx, pubkey), func(ref sdk.EventRef) any {
		return ref.Value()
	})
}

func loadMediaFollows(ctx context.Context, pubkey nostr.PubKey) map[string]any {
	return fromSDKList(sys.FetchMediaFollowList(ctx, pubkey), profileRefItem)
}

func loadFavoritePodcasts(ctx context.Context, pubkey nostr.PubKey) map[string]any {
	return fromSDKList(sys.FetchFavoritePodcastsList(ctx, pubkey), podcastRefItem)
}

func loadAuthoredPodcasts(ctx context.Context, pubkey nostr.PubKey) map[string]any {
	return fromSDKList(sys.FetchAuthoredPodcastsList(ctx, pubkey), podcastRefItem)
}

// ─── addressable sets ────────────────────────────────────────────

// loadSets returns the gadgets SetResult shape: { [dTag]: ResolvedSet }.
func loadSets(ctx context.Context, kind nostr.Kind, pubkey nostr.PubKey, parse func(nostr.Tag) (any, bool)) map[string]any {
	events, _ := sdkSetEvents(ctx, kind, pubkey)
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
	return loadSets(ctx, 30030, pubkey, emojiItem)
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
	evt := setEvent(ctx, kind, pk, identifier)
	if evt == nil {
		return nil, false
	}
	return resolvedSet(*evt, kind, identifier, itemsFromTags(*evt, parse)), true
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
	out := make([]any, 0, len(items))
	for _, item := range items {
		if m, ok := item.(map[string]any); ok {
			if _, isEmoji := m["shortcode"]; isEmoji {
				out = append(out, item)
				continue
			}
		}
		if set, ok := resolveSetPointer(ctx, item, emojiItem); ok {
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
