package backend

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/sdk"
)

// The launcher keeps a full-text-ish index of every profile it has ever seen,
// so napps can offer instant user search without touching the network:
// built from the kind:0s already in the eventstore at startup, and augmented
// by every loadNostrUser/searchUser afterwards.

const searchResultLimit = 20

type indexedUser struct {
	pm       sdk.ProfileMetadata
	haystack string
}

var (
	userIndexMu sync.RWMutex
	userIndex   = make(map[nostr.PubKey]indexedUser)
)

func indexUser(pm sdk.ProfileMetadata) {
	if pm.PubKey == (nostr.PubKey{}) {
		return
	}
	haystack := strings.ToLower(strings.Join([]string{
		pm.Name, pm.DisplayName, pm.NIP05, pm.About, pm.Npub(), pm.PubKey.Hex(),
	}, " "))

	userIndexMu.Lock()
	if prev, ok := userIndex[pm.PubKey]; ok && pm.Event != nil && prev.pm.Event != nil &&
		prev.pm.Event.CreatedAt > pm.Event.CreatedAt {
		userIndexMu.Unlock()
		return
	}
	userIndex[pm.PubKey] = indexedUser{pm: pm, haystack: haystack}
	userIndexMu.Unlock()
}

// buildUserIndex indexes every kind:0 in the local store. Fire-and-forget at
// startup — a cold launcher just has an empty index until profiles load.
func buildUserIndex() {
	if sys == nil {
		return
	}
	newest := make(map[nostr.PubKey]nostr.Event)
	for evt := range sys.Store.QueryEvents(nostr.Filter{Kinds: []nostr.Kind{0}}, 20000) {
		if prev, ok := newest[evt.PubKey]; !ok || evt.CreatedAt > prev.CreatedAt {
			newest[evt.PubKey] = evt
		}
	}
	for _, evt := range newest {
		pm, err := sdk.ParseMetadata(evt)
		if err != nil {
			continue
		}
		indexUser(pm)
	}
	log.Info().Int("profiles", len(newest)).Msg("user search index built")
}

// searchUserLocal ranks indexed profiles by where the term matches: a name
// that starts with it first, then any other hit.
func searchUserLocal(term string) []map[string]any {
	q := strings.ToLower(strings.TrimSpace(term))
	if q == "" {
		return []map[string]any{}
	}

	type hit struct {
		user  indexedUser
		score int
	}
	hits := make([]hit, 0, searchResultLimit)

	userIndexMu.RLock()
	for _, u := range userIndex {
		idx := strings.Index(u.haystack, q)
		if idx < 0 {
			continue
		}
		score := idx
		if strings.HasPrefix(strings.ToLower(u.pm.Name), q) ||
			strings.HasPrefix(strings.ToLower(u.pm.DisplayName), q) {
			score = -1
		}
		hits = append(hits, hit{user: u, score: score})
	}
	userIndexMu.RUnlock()

	sort.Slice(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score < hits[j].score
		}
		return hits[i].user.pm.ShortName() < hits[j].user.pm.ShortName()
	})

	out := make([]map[string]any, 0, min(len(hits), searchResultLimit))
	for _, h := range hits {
		out = append(out, nostrUser(h.user.pm))
		if len(out) >= searchResultLimit {
			break
		}
	}
	return out
}

// searchUser runs a NIP-50 kind:0 search on the user's own search relays
// (kind:10007) or, failing that, the sdk's defaults. Everything found joins
// the local index.
func searchUser(ctx context.Context, term string) []map[string]any {
	q := strings.TrimSpace(term)
	if q == "" || sys == nil {
		return []map[string]any{}
	}

	relays := searchRelayURLs(ctx)
	if len(relays) == 0 {
		return []map[string]any{}
	}

	fetchCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()

	newest := make(map[nostr.PubKey]nostr.Event)
	for ie := range sys.Pool.FetchMany(fetchCtx, relays, nostr.Filter{
		Kinds:  []nostr.Kind{0},
		Search: q,
		Limit:  searchResultLimit,
	}, nostr.SubscriptionOptions{Label: "usersearch"}) {
		if prev, ok := newest[ie.PubKey]; !ok || ie.CreatedAt > prev.CreatedAt {
			newest[ie.PubKey] = ie.Event
		}
	}

	out := make([]map[string]any, 0, len(newest))
	for _, evt := range newest {
		pm, err := sdk.ParseMetadata(evt)
		if err != nil {
			continue
		}
		sys.Publisher.Publish(fetchCtx, evt)
		indexUser(pm)
		out = append(out, nostrUser(pm))
		if len(out) >= searchResultLimit {
			break
		}
	}
	return out
}

func searchRelayURLs(ctx context.Context) []string {
	if userPubkey != (nostr.PubKey{}) {
		res := loadSearchRelays(ctx, userPubkey)
		if items, ok := res["items"].([]any); ok && len(items) > 0 {
			urls := make([]string, 0, len(items))
			for _, item := range items {
				if url, ok := item.(string); ok && url != "" {
					urls = append(urls, url)
				}
			}
			if len(urls) > 0 {
				return urls
			}
		}
	}
	return append([]string(nil), sys.UserSearchRelays.URLs...)
}
