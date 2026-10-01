package backend

import (
	"context"
	"sort"
	"time"

	"fiatjaf.com/nostr"
)

// ProfileDetail is what a profile tab shows: whatever the launcher knows
// about a pubkey, without blocking the render loop to get it.
type ProfileDetail struct {
	Pubkey      string `json:"pubkey"`
	Npub        string `json:"npub"`
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	ShortName   string `json:"shortName"`
	About       string `json:"about"`
	Picture     string `json:"picture"`
	NIP05       string `json:"nip05"`
	Website     string `json:"website"`
}

// FetchProfileDetail returns what the sdk has cached or can fetch about a
// pubkey: name, about, picture, nip05 and friends. It blocks (with its own
// timeout), so call it off the render loop.
func FetchProfileDetail(pubkeyHex string) ProfileDetail {
	pk, err := nostr.PubKeyFromHex(pubkeyHex)
	if err != nil {
		return ProfileDetail{Pubkey: pubkeyHex}
	}
	if sys == nil {
		return ProfileDetail{Pubkey: pk.Hex()}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pm := sys.FetchProfileMetadata(ctx, pk)
	indexUser(pm)
	return ProfileDetail{
		Pubkey:      pk.Hex(),
		Npub:        pm.Npub(),
		Name:        pm.Name,
		DisplayName: pm.DisplayName,
		ShortName:   pm.ShortName(),
		About:       pm.About,
		Picture:     pm.Picture,
		NIP05:       pm.NIP05,
		Website:     pm.Website,
	}
}

// FetchAuthorNapps lists the napps and napplets (kind:35130/35129) an author published: from the
// author's own write relays plus the launcher's discovery relays. Newest
// version wins per d-tag, so an author that republished the same napp shows
// up once. It blocks (with its own timeout), so call it off the render loop.
func FetchAuthorNapps(pubkeyHex string) []Napp {
	pk, err := nostr.PubKeyFromHex(pubkeyHex)
	if err != nil || sys == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	urls := sys.FetchWriteRelays(ctx, pk)
	for _, r := range Relays() {
		urls = nostr.AppendUnique(urls, r)
	}
	if len(urls) == 0 {
		return nil
	}

	byID := make(map[string]Napp)
	collect := func(evt nostr.Event) {
		n, ok := nappFromEvent(evt)
		if !ok {
			return
		}
		// keyed by id, not d: a napp and a napplet may share a d-tag
		if prev, ok := byID[n.ID]; !ok || n.CreatedAt > prev.CreatedAt {
			byID[n.ID] = n
		}
	}

	// whatever is already local renders instantly
	for evt := range sys.Store.QueryEvents(nostr.Filter{
		Kinds:   napKinds,
		Authors: []nostr.PubKey{pk},
	}, 200) {
		collect(evt)
	}

	for re := range sys.Pool.FetchMany(ctx, urls, nostr.Filter{
		Kinds:   napKinds,
		Authors: []nostr.PubKey{pk},
	}, nostr.SubscriptionOptions{Label: "verdana-author-napps"}) {
		collect(re.Event)
	}

	out := make([]Napp, 0, len(byID))
	for _, n := range byID {
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt > out[j].CreatedAt
		}
		return out[i].Name < out[j].Name
	})

	// author names resolve in the background and are stamped here too
	for i := range out {
		if out[i].AuthorName == "" {
			out[i].AuthorName = out[i].AuthorShortName()
		}
	}
	return out
}

// LookupNapp finds a napp the launcher already knows — installed, discovered
// or dev — by id, so a detail tab can refresh its copy every frame.
func LookupNapp(id string) (Napp, bool) {
	if n, ok := InstalledNapp(id); ok {
		return n, true
	}
	if n, ok := DiscoveredNapp(id); ok {
		return n, true
	}
	if d := devLookup(id); d != nil {
		return d.napp, true
	}
	return Napp{}, false
}
