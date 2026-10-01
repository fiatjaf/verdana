package backend

import (
	"context"
	"strconv"
	"strings"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip19"
	"fiatjaf.com/nostr/sdk"
)

// NAP-IDENTITY: read-only facts about the signed-in user. Nothing here signs,
// encrypts or prompts; a napplet with no user simply gets empty answers.

func init() {
	handleNap(map[string]napHandler{
		"identity.getPublicKey": napIdentityGetPublicKey,
		"identity.getRelays":    napIdentity("relays", map[string]any{}, identityRelays),
		"identity.getProfile":   napIdentity("profile", nil, identityProfile),
		"identity.getFollows":   napIdentity("pubkeys", []string{}, identityFollows),
		"identity.getMutes":     napIdentity("pubkeys", []string{}, identityMutes),
		"identity.getBlocked":   napIdentity("pubkeys", []string{}, identityBlocked),
		"identity.getZaps":      napIdentity("zaps", []any{}, identityZaps),
		"identity.getBadges":    napIdentity("badges", []any{}, identityBadges),
		"identity.getList":      napIdentityGetList,
	})
}

// currentUser is the signed-in pubkey, if there is one.
func currentUser() (nostr.PubKey, bool) {
	if userKeyer == nil || userPubkey == nostr.ZeroPK {
		return nostr.ZeroPK, false
	}
	return userPubkey, true
}

func napIdentityGetPublicKey(c *napCall) {
	pk, ok := currentUser()
	if !ok {
		// "" means signed out; this one never carries an error
		c.reply(map[string]any{"pubkey": ""})
		return
	}
	c.reply(map[string]any{"pubkey": pk.Hex()})
}

// napIdentity builds a handler that answers field with fetch's result for
// the current user, or with empty when there is no user or nothing came.
func napIdentity(field string, empty any, fetch func(context.Context, nostr.PubKey) any) napHandler {
	return func(c *napCall) {
		pk, ok := currentUser()
		if !ok || sys == nil {
			c.reply(map[string]any{field: empty})
			return
		}
		c.async(func(ctx context.Context) {
			ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			v := fetch(ctx, pk)
			if v == nil {
				v = empty
			}
			c.reply(map[string]any{field: v})
		})
	}
}

func identityRelays(ctx context.Context, pk nostr.PubKey) any {
	out := map[string]any{}
	for _, r := range sys.FetchRelayList(ctx, pk).Items {
		out[r.URL] = map[string]bool{"read": r.Inbox, "write": r.Outbox}
	}
	return out
}

func identityProfile(ctx context.Context, pk nostr.PubKey) any {
	pm := sys.FetchProfileMetadata(ctx, pk)
	if pm.Event == nil {
		return nil
	}
	return profileData(pm)
}

func identityFollows(ctx context.Context, pk nostr.PubKey) any {
	return profileRefHexes(sys.FetchFollowList(ctx, pk).Items)
}

func identityMutes(ctx context.Context, pk nostr.PubKey) any {
	return profileRefHexes(sys.FetchMuteList(ctx, pk).Items)
}

// identityBlocked: the launcher keeps no separate block list beyond mutes,
// so there is nothing more to report than an empty list.
func identityBlocked(context.Context, nostr.PubKey) any { return []string{} }

// identityZaps lists the zap receipts (kind 9735) sent to the user.
func identityZaps(ctx context.Context, pk nostr.PubKey) any {
	zaps := []any{}
	filter := nostr.Filter{Kinds: []nostr.Kind{9735}, Tags: nostr.TagMap{"p": []string{pk.Hex()}}, Limit: 100}
	seen := map[nostr.ID]bool{}
	add := func(evt nostr.Event) {
		if seen[evt.ID] {
			return
		}
		seen[evt.ID] = true
		if z, ok := zapReceipt(evt); ok {
			zaps = append(zaps, z)
		}
	}
	for evt := range sys.Store.QueryEvents(filter, 100) {
		add(evt)
	}
	relays := sys.FetchInboxRelays(ctx, pk, 4)
	for re := range sys.Pool.FetchMany(ctx, relays, filter, nostr.SubscriptionOptions{Label: "verdana-nap-zaps"}) {
		add(re.Event)
	}
	return zaps
}

// zapReceipt reads a kind 9735 receipt: the zapped event, who zapped (from
// the embedded request) and how much (the request's amount, in millisats).
func zapReceipt(evt nostr.Event) (map[string]any, bool) {
	desc := evt.Tags.Find("description")
	if desc == nil || len(desc) < 2 {
		return nil, false
	}
	var req nostr.Event
	if err := req.UnmarshalJSON([]byte(desc[1])); err != nil {
		return nil, false
	}
	z := map[string]any{"eventId": evt.ID.Hex(), "sender": req.PubKey.Hex(), "amount": 0}
	if amt := req.Tags.Find("amount"); amt != nil && len(amt) >= 2 {
		if n, err := strconv.ParseInt(amt[1], 10, 64); err == nil {
			z["amount"] = n
		}
	}
	if req.Content != "" {
		z["content"] = req.Content
	}
	if e := evt.Tags.Find("e"); e != nil && len(e) >= 2 {
		z["eventId"] = e[1]
	}
	return z, true
}

// identityBadges lists the badges the user displays (NIP-58 profile badges).
func identityBadges(ctx context.Context, pk nostr.PubKey) any {
	badges := []any{}
	list := sys.FetchProfileBadgesList(ctx, pk)
	if list.Event == nil {
		return badges
	}
	for _, tag := range list.Event.Tags {
		if len(tag) < 2 || tag[0] != "a" || !strings.HasPrefix(tag[1], "30009:") {
			continue
		}
		parts := strings.SplitN(tag[1], ":", 3)
		if len(parts) != 3 {
			continue
		}
		b := map[string]any{"id": tag[1], "awardedBy": parts[1]}
		if issuer, err := nostr.PubKeyFromHex(parts[1]); err == nil {
			if def := fetchAddressable(ctx, 30009, issuer, parts[2]); def != nil {
				for _, t := range def.Tags {
					if len(t) < 2 {
						continue
					}
					switch t[0] {
					case "name":
						b["name"] = t[1]
					case "description":
						b["description"] = t[1]
					case "image":
						b["image"] = t[1]
					case "thumb":
						thumbs, _ := b["thumbs"].([]string)
						b["thumbs"] = append(thumbs, t[1])
					}
				}
			}
		}
		badges = append(badges, b)
	}
	return badges
}

// listKinds maps NAP-IDENTITY list types onto the NIP-51 lists they name.
var listKinds = map[string]nostr.Kind{
	"follows":         3,
	"mutes":           10000,
	"pins":            10001,
	"relays":          10002,
	"bookmarks":       10003,
	"communities":     10004,
	"public-chats":    10005,
	"blocked-relays":  10006,
	"search-relays":   10007,
	"simple-groups":   10009,
	"interests":       10015,
	"emojis":          10030,
	"dm-relays":       10050,
	"blossom":         10063,
	"blossom-servers": 10063,
	"media-follows":   10020,
	"git-authors":     10017,
	"git-repos":       10018,
	"wiki-authors":    10101,
	"wiki-relays":     10102,
}

func napIdentityGetList(c *napCall) {
	var r struct {
		ListType string `json:"listType"`
		Type     string `json:"type"`
	}
	_ = c.decode(&r)
	name := strings.ToLower(strings.TrimSpace(r.ListType))
	if name == "" {
		name = strings.ToLower(strings.TrimSpace(r.Type))
	}
	kind, known := listKinds[name]
	if !known {
		if n, err := strconv.Atoi(name); err == nil && n >= 10000 && n < 20000 {
			kind, known = nostr.Kind(n), true
		}
	}
	pk, ok := currentUser()
	if !known || !ok || sys == nil {
		c.reply(map[string]any{"entries": []string{}})
		return
	}
	c.async(func(ctx context.Context) {
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		entries := []string{}
		if evt := fetchReplaceable(ctx, kind, pk); evt != nil {
			for _, tag := range evt.Tags {
				if len(tag) >= 2 && tag[0] != "d" && tag[0] != "title" && tag[0] != "description" {
					entries = append(entries, tag[1])
				}
			}
		}
		c.reply(map[string]any{"entries": entries})
	})
}

// pushIdentityChanged tells every napplet who the user is now ("" when
// signed out).
func pushIdentityChanged() {
	pk := ""
	if p, ok := currentUser(); ok {
		pk = p.Hex()
	}
	for _, ci := range liveNapplets() {
		ci.napPush(map[string]any{"type": "identity.changed", "pubkey": pk})
	}
}

// ─── shared helpers ──────────────────────────────────────────────

func profileRefHexes(items []sdkProfileRef) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.Pubkey.Hex())
	}
	return out
}

// profileData is NAP's profile shape.
func profileData(pm sdkProfileMetadata) map[string]any {
	out := map[string]any{}
	set := func(k, v string) {
		if v != "" {
			out[k] = v
		}
	}
	set("name", pm.Name)
	set("displayName", pm.DisplayName)
	set("about", pm.About)
	set("picture", pm.Picture)
	set("banner", pm.Banner)
	set("nip05", pm.NIP05)
	set("lud16", pm.LUD16)
	set("website", pm.Website)
	return out
}

// fetchReplaceable is the newest kind event by author: the local store
// first, then the author's write relays.
func fetchReplaceable(ctx context.Context, kind nostr.Kind, author nostr.PubKey) *nostr.Event {
	return fetchLatest(ctx, author, nostr.Filter{Kinds: []nostr.Kind{kind}, Authors: []nostr.PubKey{author}})
}

func fetchAddressable(ctx context.Context, kind nostr.Kind, author nostr.PubKey, d string) *nostr.Event {
	return fetchLatest(ctx, author, nostr.Filter{
		Kinds: []nostr.Kind{kind}, Authors: []nostr.PubKey{author}, Tags: nostr.TagMap{"d": []string{d}},
	})
}

func fetchLatest(ctx context.Context, author nostr.PubKey, filter nostr.Filter) *nostr.Event {
	var best *nostr.Event
	consider := func(evt nostr.Event) {
		if best == nil || evt.CreatedAt > best.CreatedAt {
			e := evt
			best = &e
		}
	}
	for evt := range sys.Store.QueryEvents(filter, 1) {
		consider(evt)
	}
	urls := sys.FetchWriteRelays(ctx, author)
	if len(urls) == 0 {
		urls = Relays()
	}
	filter.Limit = 1
	for re := range sys.Pool.FetchMany(ctx, urls, filter, nostr.SubscriptionOptions{Label: "verdana-nap-latest"}) {
		consider(re.Event)
	}
	return best
}

// npubOrHex reads a pubkey given as hex, npub or nprofile.
func npubOrHex(s string) (nostr.PubKey, bool) {
	s = strings.TrimSpace(strings.TrimPrefix(s, "nostr:"))
	if pk, err := nostr.PubKeyFromHex(s); err == nil {
		return pk, true
	}
	prefix, data, err := nip19.Decode(s)
	if err != nil {
		return nostr.ZeroPK, false
	}
	switch prefix {
	case "npub":
		if pk, ok := data.(nostr.PubKey); ok {
			return pk, true
		}
	case "nprofile":
		if pp, ok := data.(nostr.ProfilePointer); ok {
			return pp.PublicKey, true
		}
	}
	return nostr.ZeroPK, false
}

type (
	sdkProfileRef      = sdk.ProfileRef
	sdkProfileMetadata = sdk.ProfileMetadata
)
