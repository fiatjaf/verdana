package backend

import (
	"context"
	"time"

	"fiatjaf.com/nostr"
)

// Napp updates are the same kind:35128 event, same author and d-tag, with a
// newer created_at: there are no version numbers, only newer publications.
// Discovery keeps the in-memory list fresh passively; CheckForUpdates goes
// out and asks the source relays about every installed napp at once.

// CheckForUpdates looks for a newer kind:35128 of every installed napp — on
// the discovery relays and on each author's outbox relays — and marks the
// napps it found new versions for. Non-blocking: watch UpdateCheckRunning and
// the per-napp UpdateAvailable flags in the state for the outcome.
func CheckForUpdates() {
	if len(state.InstalledNapps) == 0 {
		return
	}

	// the check runs from a copy of the installed list: an install or
	// uninstall starting meanwhile doesn't change what this round asks
	stateMu.Lock()
	napps := make([]Napp, 0, len(state.InstalledNapps))
	for _, n := range state.InstalledNapps {
		napps = append(napps, n)
	}
	stateMu.Unlock()

	updateChecking.Store(true)
	defer func() {
		updateChecking.Store(false)
		notifyState()
	}()
	notifyState()

	if ok := checkAllUpdates(napps); ok {
		log.Info().Int("updates", len(updateCache)).Msg("update check found new versions")
	}
	setUpdateAvailable(updateCache.keys())
}

// checkAllUpdates asks the relays about every napp at once (one filter per
// relay set, so the outbox queries stay separate from the discovery query),
// then refreshes the update cache. Returns whether every relay answered.
func checkAllUpdates(napps []Napp) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	// what a newer version must beat: the created_at of what we know
	known := make(map[string]nostr.Timestamp, len(napps))
	for _, n := range napps {
		known[n.ID] = n.CreatedAt
	}

	found := make(map[string]nostr.Timestamp, len(napps))
	complete := true
	handle := func(evt nostr.Event) {
		n := nappFromEvent(evt)
		if kn, ok := known[n.ID]; ok && evt.CreatedAt > kn && evt.CreatedAt > found[n.ID] {
			found[n.ID] = evt.CreatedAt
		}
	}

	urls := Relays()
	if len(urls) == 0 {
		urls = DefaultRelays
	}
	if !scanRelays(ctx, urls, napps, handle) {
		complete = false
	}

	// authors' outbox relays: where the new version is most likely to be
	outbox := make(map[string][]Napp)
	authors := make([]nostr.PubKey, 0, len(napps))
	seen := make(map[string]bool)
	for _, n := range napps {
		if !seen[n.Author.Hex()] {
			seen[n.Author.Hex()] = true
			authors = append(authors, n.Author)
		}
	}
	for _, author := range authors {
		rctx, rcancel := context.WithTimeout(ctx, 10*time.Second)
		relays := sys.FetchOutboxRelays(rctx, author, 4)
		rcancel()
		for _, u := range relays {
			outbox[u] = append(outbox[u], nappsByAuthor(napps, author)...)
		}
	}
	for u, list := range outbox {
		if !scanRelays(ctx, []string{u}, list, handle) {
			complete = false
		}
	}

	if complete {
		// a full round replaces what we believe; a partial one only adds
		updateCache = updateCache.with(found)
	}
	return complete
}

// scanRelays queries one relay set for the current kind:35128 of the given
// napps and feeds every event to handle. It returns false when the round was
// cut short (a relay that never answered), so the caller can keep its old
// cache instead of narrowing it to what a truncated round saw.
func scanRelays(ctx context.Context, urls []string, napps []Napp, handle func(nostr.Event)) bool {
	complete := true
	for re := range sys.Pool.FetchMany(ctx, urls, nostr.Filter{
		Kinds:   []nostr.Kind{35128},
		Authors: nappAuthors(napps),
		Tags:    nostr.TagMap{"d": ds(napps)},
	}, nostr.SubscriptionOptions{}) {
		if re.Relay == nil {
			complete = false
		}
		handle(re.Event)
	}
	return complete
}

// ─── applying an update ──────────────────────────────────────────

// Update re-downloads an installed napp's files from its blossom servers. It
// requires knowing a newer version: the discovery list, a check round, or the
// relay lookup this triggers when neither has one.
func Update(id string) {
	n, ok := InstalledNapp(id)
	if !ok {
		SetFetchErr("napp " + id + " is not installed")
		return
	}

	setBusy(id, true)
	defer setBusy(id, false)

	latest := newerVersion(n)
	if latest == nil {
		SetFetchErr("no update found for " + n.Label())
		return
	}

	applyUpdate(n, *latest)
}

// applyUpdate does the shared re-download: fetch every path of newer into the
// napp's install dir, then record it as the installed version. Called with
// setBusy held. newer needs the full event shape; Paths and Servers are the
// parts that matter for the download itself.
func applyUpdate(current, newer Napp) {
	base := nappBaseDir(current.ID)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	servers := newer.Servers
	if len(servers) == 0 {
		servers = newer.blossomServers(ctx)
	}
	if err := fetchNappAssets(ctx, newer, base, servers); err != nil {
		log.Error().Err(err).Str("napp", current.ID).Msg("update failed")
		SetFetchErr("update failed: " + err.Error())
		return
	}

	// adopt the new event wholesale (new paths, new metadata), keeping the
	// id the launcher knows it by (the id is author~d, so it is already the
	// same — this only guards against a weird event)
	newer.ID = current.ID
	stateMu.Lock()
	state.InstalledNapps[current.ID] = newer
	delete(state.LastLaunched, current.ID)
	saveState()
	stateMu.Unlock()

	// the previously available update is now the installed version
	upd := *updateAvailable.Load()
	delete(upd, current.ID)
	updateAvailable.Store(&upd)

	refreshInstalled()
	log.Info().Str("napp", current.ID).Msg("update complete")
}

// newerVersion returns the best known newer version of an installed napp:
// from the in-memory cache a check round built, falling back to a live relay
// lookup on the discovery relays and the author's outbox.
func newerVersion(n Napp) *Napp {
	if ts, ok := updateCache[n.ID]; ok && ts > n.CreatedAt {
		if evt := fetchCurrentEvent(n.Author, n.D); evt != nil {
			nn := nappFromEvent(*evt)
			if nn.CreatedAt > n.CreatedAt {
				return &nn
			}
		}
		return nil
	}

	// nothing cached: ask the relays right now
	if found := checkAllUpdates([]Napp{n}); found {
		if ts, ok := updateCache[n.ID]; ok && ts > n.CreatedAt {
			if evt := fetchCurrentEvent(n.Author, n.D); evt != nil {
				nn := nappFromEvent(*evt)
				if nn.CreatedAt > n.CreatedAt {
					return &nn
				}
			}
		}
	}
	return nil
}

// fetchCurrentEvent fetches the current kind:35128 of a napp from its
// author's outbox relays (falling back to the discovery relays).
func fetchCurrentEvent(author nostr.PubKey, d string) *nostr.Event {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	urls := sys.FetchOutboxRelays(ctx, author, 4)
	if len(urls) == 0 {
		urls = Relays()
	}
	if len(urls) == 0 {
		urls = DefaultRelays
	}

	for re := range sys.Pool.FetchMany(ctx, urls, nostr.Filter{
		Kinds:   []nostr.Kind{35128},
		Authors: []nostr.PubKey{author},
		Tags:    nostr.TagMap{"d": []string{d}},
		Limit:   1,
	}, nostr.SubscriptionOptions{}) {
		evt := re.Event
		return &evt
	}
	return nil
}

// ─── the update cache ────────────────────────────────────────────

// updateCache remembers the newest created_at seen per napp id from the last
// complete check round. It is only touched from the goroutine running a
// round, so a plain map guarded by discipline is enough.
var updateCache = updatesByTs{}

type updatesByTs map[string]nostr.Timestamp

// with merges found into the cache, keeping the newest timestamp per id.
func (u updatesByTs) with(found map[string]nostr.Timestamp) updatesByTs {
	out := make(updatesByTs, len(u)+len(found))
	for id, ts := range u {
		out[id] = ts
	}
	for id, ts := range found {
		if ts > out[id] {
			out[id] = ts
		}
	}
	return out
}

// keys is the ids the cache holds, for setUpdateAvailable.
func (u updatesByTs) keys() []string {
	out := make([]string, 0, len(u))
	for id := range u {
		out = append(out, id)
	}
	return out
}

// ─── helpers ─────────────────────────────────────────────────────

func nappAuthors(napps []Napp) []nostr.PubKey {
	out := make([]nostr.PubKey, 0, len(napps))
	for _, n := range napps {
		out = append(out, n.Author)
	}
	return out
}

func ds(napps []Napp) []string {
	out := make([]string, 0, len(napps))
	for _, n := range napps {
		out = append(out, n.D)
	}
	return out
}

func nappsByAuthor(napps []Napp, author nostr.PubKey) []Napp {
	out := make([]Napp, 0, 1)
	for _, n := range napps {
		if n.Author == author {
			out = append(out, n)
		}
	}
	return out
}
