package backend

import (
	"context"
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

// FetchAuthorNapps lists the kind:35130/35131 napps an author published: it
// asks the author's own write relays plus the launcher's discovery relays,
// saves what they have in the store and answers from the store. Newest
// version wins per napp, so an author that republished the same napp shows
// up once. A contact's scan counts for the contacts sync too (see
// contacts.go). It blocks (with its own timeout), so call it off the render
// loop.
func FetchAuthorNapps(pubkeyHex string) []Napp {
	pk, err := nostr.PubKeyFromHex(pubkeyHex)
	if err != nil || sys == nil {
		return nil
	}
	scanAuthorNapps(context.Background(), pk)
	if contactsHas(pk) {
		RefreshContacts()
	}

	out := storedNapps([]nostr.PubKey{pk})
	// author names resolve in the background and are stamped here too
	for i := range out {
		if out[i].AuthorName == "" {
			out[i].AuthorName = out[i].AuthorShortName()
		}
		rememberNapp(out[i])
	}
	return out
}

// LookupNapp finds a napp the launcher already knows — installed, discovered,
// dev or seen elsewhere (see seenNapps) — by id, so a detail tab can
// refresh its copy every frame.
func LookupNapp(id string) (Napp, bool) {
	if n, ok := InstalledNapp(id); ok {
		if newVersion, ok := updateSet.Load().Load(id); ok {
			n.UpdateAvailable = &newVersion
		}
		return n, true
	}
	if n, ok := DiscoveredNapp(id); ok {
		return n, true
	}
	if d := devLookup(id); d != nil {
		return d.napp, true
	}
	return seenNapp(id)
}
