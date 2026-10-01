package backend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
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

// The error strings NAP-IDENTITY names for results the shell can't fulfill.
var (
	errIdentityTimeout  = errors.New("relay timeout")
	errUnknownListType  = errors.New("unsupported list type")
	errIdentityInternal = errors.New("internal error")
)

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
// the current user, or with empty when there is no user or nothing came. A
// fetch error rides along as the result's error field; every request gets
// its result, even when fetch panics.
func napIdentity(field string, empty any, fetch func(context.Context, nostr.PubKey) (any, error)) napHandler {
	return func(c *napCall) {
		pk, ok := currentUser()
		if !ok || sys == nil {
			c.reply(map[string]any{field: empty})
			return
		}
		c.async(func(ctx context.Context) {
			ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			v, err := identityFetch(ctx, pk, fetch)
			c.reply(identityResult(field, empty, v, err))
		})
	}
}

// identityFetch runs fetch, turning a panic into an error so the caller can
// still answer.
func identityFetch(ctx context.Context, pk nostr.PubKey, fetch func(context.Context, nostr.PubKey) (any, error)) (v any, err error) {
	defer func() {
		if r := recover(); r != nil {
			log.Error().Interface("panic", r).Msg("NAP identity fetch panicked")
			v, err = nil, errIdentityInternal
		}
	}()
	return fetch(ctx, pk)
}

// identityResult is a result's fields: the value (or its empty default) and
// the error, if there was one.
func identityResult(field string, empty, v any, err error) map[string]any {
	if v == nil {
		v = empty
	}
	out := map[string]any{field: v}
	if err != nil {
		out["error"] = err.Error()
	}
	return out
}

// timedOut is the error for an answer that came up empty because our own
// deadline ran out before the relays did.
func timedOut(ctx context.Context) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return errIdentityTimeout
	}
	return nil
}

func identityRelays(ctx context.Context, pk nostr.PubKey) (any, error) {
	list := sys.FetchRelayList(ctx, pk)
	out := map[string]any{}
	for _, r := range list.Items {
		out[r.URL] = map[string]bool{"read": r.Inbox, "write": r.Outbox}
	}
	if list.Event == nil {
		return out, timedOut(ctx)
	}
	return out, nil
}

func identityProfile(ctx context.Context, pk nostr.PubKey) (any, error) {
	pm := sys.FetchProfileMetadata(ctx, pk)
	if pm.Event == nil {
		// no kind 0 is not an error, only running out of time is
		return nil, timedOut(ctx)
	}
	return profileData(pm), nil
}

func identityFollows(ctx context.Context, pk nostr.PubKey) (any, error) {
	list := sys.FetchFollowList(ctx, pk)
	if list.Event == nil {
		return []string{}, timedOut(ctx)
	}
	return profileRefHexes(list.Items), nil
}

func identityMutes(ctx context.Context, pk nostr.PubKey) (any, error) {
	list := sys.FetchMuteList(ctx, pk)
	if list.Event == nil {
		return []string{}, timedOut(ctx)
	}
	return profileRefHexes(list.Items), nil
}

// identityBlocked: the launcher keeps no separate block list beyond mutes,
// so there is nothing more to report than an empty list.
func identityBlocked(context.Context, nostr.PubKey) (any, error) { return []string{}, nil }

// identityZaps lists the zap receipts (kind 9735) sent to the user. Anyone
// can publish a receipt that p-tags the user, so only those signed by the
// zap provider behind the user's lightning address count (NIP-57 appendix F).
func identityZaps(ctx context.Context, pk nostr.PubKey) (any, error) {
	zaps := []any{}
	pm := sys.FetchProfileMetadata(ctx, pk)
	if pm.LUD16 == "" {
		if pm.Event == nil {
			return zaps, timedOut(ctx)
		}
		// no lightning address, no zaps
		return zaps, nil
	}
	provider, ok, err := zapProvider(ctx, pm.LUD16)
	if err != nil {
		return zaps, err
	}
	if !ok {
		// the address doesn't take nostr zaps
		return zaps, nil
	}

	filter := nostr.Filter{
		Kinds:   []nostr.Kind{9735},
		Authors: []nostr.PubKey{provider},
		Tags:    nostr.TagMap{"p": []string{pk.Hex()}},
		Limit:   100,
	}
	type receipt struct {
		at nostr.Timestamp
		z  map[string]any
	}
	var found []receipt
	seen := map[nostr.ID]bool{}
	add := func(evt nostr.Event) {
		if seen[evt.ID] {
			return
		}
		seen[evt.ID] = true
		if z, ok := zapReceipt(evt, provider, pk); ok {
			found = append(found, receipt{evt.CreatedAt, z})
		}
	}
	for evt := range sys.Store.QueryEvents(filter, 100) {
		add(evt)
	}
	relays := sys.FetchInboxRelays(ctx, pk, 4)
	for re := range sys.Pool.FetchMany(ctx, relays, filter, nostr.SubscriptionOptions{Label: "verdana-nap-zaps"}) {
		add(re.Event)
	}
	sort.SliceStable(found, func(i, j int) bool { return found[i].at > found[j].at })
	for _, r := range found {
		zaps = append(zaps, r.z)
	}
	return zaps, nil
}

// zapReceipt reads a kind 9735 receipt from provider for recipient: who
// zapped (the embedded, signed zap request) and how much (the invoice, in
// millisats). eventId is the receipt's own id.
func zapReceipt(evt nostr.Event, provider, recipient nostr.PubKey) (map[string]any, bool) {
	if evt.Kind != 9735 || evt.PubKey != provider {
		return nil, false
	}
	desc := evt.Tags.Find("description")
	if desc == nil || len(desc) < 2 {
		return nil, false
	}
	var req nostr.Event
	if err := req.UnmarshalJSON([]byte(desc[1])); err != nil {
		return nil, false
	}
	if req.Kind != 9734 || !req.CheckID() || !req.VerifySignature() {
		return nil, false
	}
	if p := req.Tags.Find("p"); p == nil || len(p) < 2 || p[1] != recipient.Hex() {
		return nil, false
	}

	var amount int64
	if b := evt.Tags.Find("bolt11"); b != nil && len(b) >= 2 {
		amount, _ = bolt11Msats(b[1])
	}
	if amount == 0 {
		if amt := req.Tags.Find("amount"); amt != nil && len(amt) >= 2 {
			amount, _ = strconv.ParseInt(amt[1], 10, 64)
		}
	}

	z := map[string]any{"eventId": evt.ID.Hex(), "sender": req.PubKey.Hex(), "amount": amount}
	if req.Content != "" {
		z["content"] = req.Content
	}
	return z, true
}

// bolt11Msats reads the amount a BOLT-11 invoice's human-readable part
// names (ln<currency><amount><multiplier>1<data>), in millisats.
func bolt11Msats(invoice string) (int64, bool) {
	inv := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(invoice)), "lightning:")
	// '1' isn't in the bech32 data charset, so the last one is the separator
	sep := strings.LastIndexByte(inv, '1')
	if !strings.HasPrefix(inv, "ln") || sep < 2 {
		return 0, false
	}
	hrp := inv[2:sep]
	// the currency prefix (bc, tb, bcrt, ...) is letters; the amount starts
	// at the first digit, and an invoice with none names no amount
	start := strings.IndexAny(hrp, "0123456789")
	if start < 0 {
		return 0, false
	}
	num, mult := hrp[start:], byte(0)
	if last := num[len(num)-1]; last < '0' || last > '9' {
		num, mult = num[:len(num)-1], last
	}
	n, err := strconv.ParseInt(num, 10, 64)
	if err != nil || n <= 0 {
		return 0, false
	}
	// msats per unit of the multiplier; a whole bitcoin is 1e11 msats
	var per int64
	switch mult {
	case 0:
		per = 100_000_000_000
	case 'm':
		per = 100_000_000
	case 'u':
		per = 100_000
	case 'n':
		per = 100
	case 'p':
		// a pico-bitcoin is a tenth of a millisat
		if n%10 != 0 {
			return 0, false
		}
		return n / 10, true
	default:
		return 0, false
	}
	if n > math.MaxInt64/per {
		return 0, false
	}
	return n * per, true
}

// zapProviders caches lightning address lookups: lud16 → zapProviderEntry.
var zapProviders sync.Map

type zapProviderEntry struct {
	pk nostr.PubKey
	ok bool
	at time.Time
}

const zapProviderTTL = time.Hour

// zapProvider is the pubkey that signs zap receipts for a lightning address,
// read from its LNURL-pay endpoint. ok is false when the address doesn't
// take nostr zaps.
func zapProvider(ctx context.Context, lud16 string) (nostr.PubKey, bool, error) {
	if v, hit := zapProviders.Load(lud16); hit {
		if e := v.(zapProviderEntry); time.Since(e.at) < zapProviderTTL {
			return e.pk, e.ok, nil
		}
	}
	endpoint, valid := lnurlpURL(lud16)
	if !valid {
		return nostr.ZeroPK, false, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nostr.ZeroPK, false, nil
	}
	resp, err := resourceClient.Do(req)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nostr.ZeroPK, false, errIdentityTimeout
		}
		return nostr.ZeroPK, false, fmt.Errorf("zap provider unreachable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nostr.ZeroPK, false, fmt.Errorf("zap provider unreachable")
	}
	var params struct {
		AllowsNostr bool   `json:"allowsNostr"`
		NostrPubkey string `json:"nostrPubkey"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&params); err != nil {
		return nostr.ZeroPK, false, fmt.Errorf("zap provider unreachable")
	}
	e := zapProviderEntry{at: time.Now()}
	if params.AllowsNostr {
		if pk, err := nostr.PubKeyFromHex(params.NostrPubkey); err == nil {
			e.pk, e.ok = pk, true
		}
	}
	zapProviders.Store(lud16, e)
	return e.pk, e.ok, nil
}

// lnurlpURL is a lightning address's LNURL-pay endpoint (LUD-16).
func lnurlpURL(lud16 string) (string, bool) {
	name, domain, found := strings.Cut(strings.ToLower(strings.TrimSpace(lud16)), "@")
	if !found || name == "" || domain == "" || strings.ContainsAny(domain, "/?#@\\ ") {
		return "", false
	}
	u := url.URL{Scheme: "https", Host: domain, Path: "/.well-known/lnurlp/" + name}
	return u.String(), true
}

// identityBadges lists the badges awarded to the user (NIP-58 kind 8
// awards), newest first. An award only counts when it comes from the
// definition's own author, who is then the badge's awardedBy.
func identityBadges(ctx context.Context, pk nostr.PubKey) (any, error) {
	badges := []any{}
	type award struct {
		addr   string
		issuer nostr.PubKey
		d      string
		at     nostr.Timestamp
	}
	var awards []*award
	byAddr := map[string]*award{}
	consider := func(evt nostr.Event) {
		a := evt.Tags.Find("a")
		if evt.Kind != 8 || a == nil || len(a) < 2 {
			return
		}
		parts := strings.SplitN(a[1], ":", 3)
		if len(parts) != 3 || parts[0] != "30009" || parts[1] != evt.PubKey.Hex() {
			return
		}
		if prev := byAddr[a[1]]; prev != nil {
			// awarded more than once: keep the newest
			if evt.CreatedAt > prev.at {
				prev.at = evt.CreatedAt
			}
			return
		}
		aw := &award{addr: a[1], issuer: evt.PubKey, d: parts[2], at: evt.CreatedAt}
		byAddr[a[1]] = aw
		awards = append(awards, aw)
	}

	filter := nostr.Filter{Kinds: []nostr.Kind{8}, Tags: nostr.TagMap{"p": []string{pk.Hex()}}, Limit: 200}
	for evt := range sys.Store.QueryEvents(filter, 200) {
		consider(evt)
	}
	relays := nostr.AppendUnique(sys.FetchInboxRelays(ctx, pk, 4), Relays()...)
	for re := range sys.Pool.FetchMany(ctx, relays, filter, nostr.SubscriptionOptions{Label: "verdana-nap-badges"}) {
		consider(re.Event)
	}
	if len(awards) == 0 {
		return badges, timedOut(ctx)
	}
	sort.SliceStable(awards, func(i, j int) bool { return awards[i].at > awards[j].at })
	if len(awards) > 50 {
		awards = awards[:50]
	}

	// definitions in parallel: each is its own issuer's lookup
	out := make([]map[string]any, len(awards))
	var wg sync.WaitGroup
	for i, aw := range awards {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b := map[string]any{"id": aw.addr, "awardedBy": aw.issuer.Hex()}
			if def := fetchAddressable(ctx, 30009, aw.issuer, aw.d); def != nil {
				badgeDefinition(b, def)
			}
			out[i] = b
		}()
	}
	wg.Wait()
	for _, b := range out {
		badges = append(badges, b)
	}
	return badges, nil
}

// badgeDefinition copies a kind 30009 definition's metadata onto b.
func badgeDefinition(b map[string]any, def *nostr.Event) {
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

// listKind resolves a getList type: a name from listKinds, or a standard
// list's kind number.
func listKind(listType string) (nostr.Kind, bool) {
	name := strings.ToLower(strings.TrimSpace(listType))
	if kind, ok := listKinds[name]; ok {
		return kind, true
	}
	if n, err := strconv.Atoi(name); err == nil && n >= 10000 && n < 20000 {
		return nostr.Kind(n), true
	}
	return 0, false
}

func napIdentityGetList(c *napCall) {
	var r struct {
		ListType string `json:"listType"`
		Type     string `json:"type"`
	}
	_ = c.decode(&r)
	listType := r.ListType
	if strings.TrimSpace(listType) == "" {
		listType = r.Type
	}
	kind, known := listKind(listType)
	if !known {
		c.reply(identityResult("entries", []string{}, nil, errUnknownListType))
		return
	}
	napIdentity("entries", []string{}, func(ctx context.Context, pk nostr.PubKey) (any, error) {
		evt := fetchReplaceable(ctx, kind, pk)
		if evt == nil {
			return []string{}, timedOut(ctx)
		}
		entries := []string{}
		for _, tag := range evt.Tags {
			if len(tag) >= 2 && tag[0] != "d" && tag[0] != "title" && tag[0] != "description" {
				entries = append(entries, tag[1])
			}
		}
		return entries, nil
	})(c)
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
