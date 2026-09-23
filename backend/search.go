package backend

import (
	"context"
	"strings"
	"sync"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/sdk"
	"github.com/wizenheimer/blaze"
)

// The launcher keeps a full-text index of every profile it has ever seen,
// so napps can offer instant user search without touching the network:
// built from the kind:0s already in the eventstore at startup, and augmented
// by every loadNostrUser/searchUser afterwards.

const searchResultLimit = 20

type indexedUser struct {
	pm sdk.ProfileMetadata
}

var (
	userSearchMu    sync.RWMutex
	userSearchIndex = blaze.NewInvertedIndex()
	indexedUsers    []indexedUser
)

func indexUser(pm sdk.ProfileMetadata) {
	if pm.PubKey == (nostr.PubKey{}) {
		return
	}
	document := strings.Join([]string{
		pm.Name, pm.DisplayName, pm.NIP05, pm.About, pm.Npub(), pm.PubKey.Hex(),
	}, " ")

	userSearchMu.Lock()
	for i := len(indexedUsers) - 1; i >= 0; i-- {
		prev := indexedUsers[i].pm
		if prev.PubKey == pm.PubKey {
			if pm.Event != nil && prev.Event != nil && prev.Event.CreatedAt < pm.Event.CreatedAt {
				break
			}
			if prev.Event != nil && pm.Event != nil && prev.Event.CreatedAt >= pm.Event.CreatedAt {
				userSearchMu.Unlock()
				return
			}
		}
	}
	docID := len(indexedUsers) + 1
	indexedUsers = append(indexedUsers, indexedUser{pm: pm})
	userSearchIndex.Index(docID, document)
	userSearchMu.Unlock()
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

// searchUserLocal searches indexed profiles with Blaze and keeps newest profile
// document when older metadata remains in its append-only index.
func searchUserLocal(term string) []map[string]any {
	q := strings.ToLower(strings.TrimSpace(term))
	if q == "" {
		return []map[string]any{}
	}

	userSearchMu.RLock()
	matches := userSearchIndex.RankBM25(q, searchResultLimit*4)
	hits := make([]indexedUser, 0, min(len(matches), searchResultLimit))
	seen := make(map[nostr.PubKey]bool, len(matches))
	for _, match := range matches {
		if match.DocID <= 0 || match.DocID > len(indexedUsers) {
			continue
		}
		user := indexedUsers[match.DocID-1]
		if seen[user.pm.PubKey] || !isLatestIndexedUser(user.pm.PubKey, match.DocID) {
			continue
		}
		seen[user.pm.PubKey] = true
		hits = append(hits, user)
	}
	userSearchMu.RUnlock()

	out := make([]map[string]any, 0, min(len(hits), searchResultLimit))
	for _, user := range hits {
		out = append(out, nostrUser(user.pm))
		if len(out) >= searchResultLimit {
			break
		}
	}
	return out
}

func isLatestIndexedUser(pubkey nostr.PubKey, docID int) bool {
	for i := len(indexedUsers) - 1; i >= 0; i-- {
		if indexedUsers[i].pm.PubKey == pubkey {
			return i+1 == docID
		}
	}
	return false
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
