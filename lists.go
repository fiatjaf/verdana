package main

import (
	"context"
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
// Everything else is the sdk's job. Every load* below is a call to the sdk
// loader for its kind (sys.Fetch*List / sys.Fetch*Sets / sys.Fetch*WithSets),
// which does the local eventstore, the kvstore mark that decides when to hit
// the network again, the author's outbox relays, batched REQs, the 6h cache
// and the tag parsing. All that is left in this file is turning the sdk's
// typed items into the JSON shapes napps expect.

// ─── cache invalidation ──────────────────────────────────────────

// invalidateList drops what the sdk cached for a kind+author. Called after
// publishing (the event is stored locally first), so the next load* reflects
// what the napp just wrote without waiting for the relays to echo it back.
func invalidateList(kind nostr.Kind, pubkey nostr.PubKey) {
	if sys == nil {
		return
	}

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
	case 10009:
		dropCached(sys.SimpleGroupsListCache, pubkey)
	case 10012:
		dropCached(sys.RelayFeedsListCache, pubkey)
		dropCached(sys.FavoriteRelaysWithSetsListCache, pubkey)
	case 10015:
		dropCached(sys.TopicListCache, pubkey)
	case 10017:
		dropCached(sys.GitAuthorListCache, pubkey)
	case 10018:
		dropCached(sys.GitRepositoryListCache, pubkey)
	case 10020:
		dropCached(sys.MediaFollowListCache, pubkey)
	case 10021:
		dropCached(sys.FavoriteFollowSetsListCache, pubkey)
	case 10027:
		dropCached(sys.FavoriteScrollsListCache, pubkey)
	case 10030:
		dropCached(sys.EmojiListCache, pubkey)
		dropCached(sys.EmojisWithSetsListCache, pubkey)
	case 10050:
		dropCached(sys.DMRelayListCache, pubkey)
	case 10054:
		dropCached(sys.PodcastFavoriteListCache, pubkey)
	case 10063:
		dropCached(sys.BlossomServerListCache, pubkey)
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

// ─── result shapes ───────────────────────────────────────────────

// listResult is the { event, items } shape napps get from every load*.
func listResult(evt *nostr.Event, items []any) map[string]any {
	if items == nil {
		items = []any{}
	}
	return map[string]any{"event": evt, "items": items}
}

func emptyListResult() map[string]any { return listResult(nil, nil) }

// fromSDKList shapes one of the sdk's typed lists into a list result, with
// conv mapping each sdk item to the shape napps expect.
func fromSDKList[V comparable, I sdk.TagItemWithValue[V]](
	list sdk.GenericList[V, I],
	conv func(I) any,
) map[string]any {
	if list.Event == nil {
		return emptyListResult()
	}
	return listResult(list.Event, convItems(list.Items, conv))
}

func convItems[I any](items []I, conv func(I) any) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		if v := conv(item); v != nil {
			out = append(out, v)
		}
	}
	return out
}

// resolvedSet is gadgets' ResolvedSet: an addressable set with its contents.
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

// ─── item shapes (mirroring @nostr/gadgets/lists) ────────────────

func profileRefItem(ref sdk.ProfileRef) any { return ref.Pubkey.Hex() }

func relayURLValue(url sdk.RelayURL) any { return string(url) }

func blossomURLValue(url sdk.BlossomURL) any { return string(url) }

// relayItem is gadgets' RelayItem: the sdk's inbox/outbox as read/write.
func relayItem(r sdk.Relay) any {
	return map[string]any{"url": r.URL, "read": r.Inbox, "write": r.Outbox}
}

func emojiItem(e sdk.Emoji) any {
	return map[string]any{"shortcode": e.Shortcode, "url": e.ImageURL}
}

func groupRefItem(g sdk.GroupRef) any {
	item := map[string]any{"groupId": g.GroupId, "relay": g.Relay}
	if g.Name != "" {
		item["name"] = g.Name
	}
	return item
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

// eventRefValue is gadgets' plain tag reference: an id, or a "kind:pubkey:d".
func eventRefValue(ref sdk.EventRef) any { return ref.Value() }

// eventRefItem is gadgets' `string | AddressPointer`: an event id as hex, an
// addressable event as a pointer object.
func eventRefItem(ref sdk.EventRef) any {
	switch p := ref.Pointer.(type) {
	case nostr.EventPointer:
		return p.ID.Hex()
	case nostr.EntityPointer:
		return addressPointerItem(p)
	}
	return nil
}

// eventPointerItem is the EventPointer shape from env.d.ts.
func eventPointerItem(ref sdk.EventRef) any {
	p, ok := ref.Pointer.(nostr.EventPointer)
	if !ok {
		return nil
	}
	item := map[string]any{"id": p.ID.Hex()}
	if p.Kind != 0 {
		item["kind"] = int(p.Kind)
	}
	if len(p.Relays) > 0 {
		item["relays"] = p.Relays
	}
	if p.Author != nostr.ZeroPK {
		item["author"] = p.Author.Hex()
	}
	return item
}

// addressPointerItem is the { identifier, pubkey, kind, relays } item shape.
func addressPointerItem(p nostr.EntityPointer) any {
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

// entityPointerItem is eventRefItem restricted to addressable refs, for the
// lists that only ever point at sets (kind 10021).
func entityPointerItem(ref sdk.EventRef) any {
	p, ok := ref.Pointer.(nostr.EntityPointer)
	if !ok {
		return nil
	}
	return addressPointerItem(p)
}

// ─── the load* surface ───────────────────────────────────────────

func loadRelayList(ctx context.Context, pubkey nostr.PubKey) map[string]any {
	return fromSDKList(sys.FetchRelayList(ctx, pubkey), relayItem)
}

func loadFollowsList(ctx context.Context, pubkey nostr.PubKey) map[string]any {
	return fromSDKList(sys.FetchFollowList(ctx, pubkey), profileRefItem)
}

func loadMuteList(ctx context.Context, pubkey nostr.PubKey) map[string]any {
	// only the muted pubkeys, like the sdk: threads, hashtags and words are
	// ignored
	return fromSDKList(sys.FetchMuteList(ctx, pubkey), profileRefItem)
}

func loadBookmarks(ctx context.Context, pubkey nostr.PubKey) map[string]any {
	return fromSDKList(sys.FetchBookmarkList(ctx, pubkey), eventRefValue)
}

func loadPins(ctx context.Context, pubkey nostr.PubKey) map[string]any {
	return fromSDKList(sys.FetchPinList(ctx, pubkey), eventRefValue)
}

func loadBlossomServers(ctx context.Context, pubkey nostr.PubKey) map[string]any {
	return fromSDKList(sys.FetchBlossomServerList(ctx, pubkey), blossomURLValue)
}

func loadEmojis(ctx context.Context, pubkey nostr.PubKey) map[string]any {
	return fromSDKList(sys.FetchEmojiList(ctx, pubkey), emojiItem)
}

func loadFavoriteRelays(ctx context.Context, pubkey nostr.PubKey) map[string]any {
	// kind:10012, plain "relay" items only -- the "a" tags pointing at relay
	// sets are what fetchFavoriteRelaysWithSets is for
	return fromSDKList(sys.FetchRelayFeedsList(ctx, pubkey), relayURLValue)
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
	return fromSDKList(sys.FetchFavoriteFollowSetsList(ctx, pubkey), entityPointerItem)
}

func loadFavoriteScrolls(ctx context.Context, pubkey nostr.PubKey) map[string]any {
	return fromSDKList(sys.FetchFavoriteScrollsList(ctx, pubkey), eventPointerItem)
}

func loadProfileBadges(ctx context.Context, pubkey nostr.PubKey) map[string]any {
	return fromSDKList(sys.FetchProfileBadgesList(ctx, pubkey), eventRefItem)
}

func loadSimpleGroups(ctx context.Context, pubkey nostr.PubKey) map[string]any {
	return fromSDKList(sys.FetchSimpleGroupsList(ctx, pubkey), groupRefItem)
}

func loadGitAuthors(ctx context.Context, pubkey nostr.PubKey) map[string]any {
	return fromSDKList(sys.FetchGitAuthorList(ctx, pubkey), profileRefItem)
}

func loadGitRepositories(ctx context.Context, pubkey nostr.PubKey) map[string]any {
	return fromSDKList(sys.FetchGitRepositoryList(ctx, pubkey), eventRefValue)
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

// setsResult is the gadgets SetResult shape: { [dTag]: ResolvedSet }.
func setsResult[V comparable, I sdk.TagItemWithValue[V]](
	kind nostr.Kind,
	sets sdk.GenericSets[V, I],
	conv func(I) any,
) map[string]any {
	out := make(map[string]any, len(sets.Events))
	for i := range sets.Events {
		evt := sets.Events[i]
		d := evt.Tags.GetD()
		if prev, ok := out[d].(map[string]any); ok {
			if pe, ok := prev["event"].(*nostr.Event); ok && pe.CreatedAt >= evt.CreatedAt {
				continue
			}
		}
		out[d] = resolvedSet(evt, kind, d, convItems(sets.Sets[d], conv))
	}
	return out
}

func loadFollowSets(ctx context.Context, pubkey nostr.PubKey) map[string]any {
	return setsResult(30000, sys.FetchFollowSets(ctx, pubkey), profileRefItem)
}

func loadRelaySets(ctx context.Context, pubkey nostr.PubKey) map[string]any {
	return setsResult(30002, sys.FetchRelaySets(ctx, pubkey), relayURLValue)
}

func loadEmojiSets(ctx context.Context, pubkey nostr.PubKey) map[string]any {
	return setsResult(30030, sys.FetchEmojiSets(ctx, pubkey), emojiItem)
}

// ─── composite helpers ───────────────────────────────────────────

// itemsOrSets flattens one of the sdk's with-sets lists: plain items stay
// items, set references become ResolvedSets with their contents inline.
func itemsOrSets[I sdk.TagItemWithValue[string]](
	list sdk.GenericList[string, sdk.ListItemOrSet[I]],
	conv func(I) any,
) []any {
	out := make([]any, 0, len(list.Items))
	for _, los := range list.Items {
		if los.Pointer == nil {
			if v := conv(los.Item); v != nil {
				out = append(out, v)
			}
			continue
		}
		p, ok := los.Pointer.(nostr.EntityPointer)
		if !ok || los.Set.Event == nil {
			continue
		}
		out = append(out, resolvedSet(*los.Set.Event, p.Kind, p.Identifier, convItems(los.Set.Items, conv)))
	}
	return out
}

// fetchFavoriteRelaysWithSets flattens kind:10012 into urls and resolved
// kind:30002 sets.
func fetchFavoriteRelaysWithSets(ctx context.Context, pubkey nostr.PubKey) []any {
	return itemsOrSets(sys.FetchFavoriteRelaysWithSets(ctx, pubkey), relayURLValue)
}

// fetchEmojisWithSets flattens kind:10030 into emojis and resolved kind:30030
// sets.
func fetchEmojisWithSets(ctx context.Context, pubkey nostr.PubKey) []any {
	return itemsOrSets(sys.FetchEmojisWithSets(ctx, pubkey), emojiItem)
}

// fetchFavoriteFollowSetsWithSets resolves every kind:10021 pointer into the
// follow set it references. The sdk has no with-sets loader for this kind, but
// each pointer names an author + d, so its kind:30000 loader answers it (and
// caches all of that author's sets while it's there).
func fetchFavoriteFollowSetsWithSets(ctx context.Context, pubkey nostr.PubKey) []any {
	list := sys.FetchFavoriteFollowSetsList(ctx, pubkey)
	out := make([]any, 0, len(list.Items))
	for _, ref := range list.Items {
		p, ok := ref.Pointer.(nostr.EntityPointer)
		if !ok {
			continue
		}
		sets := sys.FetchFollowSets(ctx, p.PublicKey)
		for i := range sets.Events {
			evt := sets.Events[i]
			if evt.Tags.GetD() != p.Identifier {
				continue
			}
			out = append(out, resolvedSet(evt, 30000, p.Identifier,
				convItems(sets.Sets[p.Identifier], profileRefItem)))
			break
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

type errNotFound string

func (e errNotFound) Error() string { return string(e) }
