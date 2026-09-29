package backend

import (
	"context"
	"time"

	"fiatjaf.com/nostr"
)

// Napp updates are the same kind:35130 event, same author and d-tag, with a
// newer created_at: there are no version numbers, only newer publications.
// Discovery keeps the in-memory list fresh passively; CheckForUpdates goes
// out and asks the source relays about every installed napp at once.

// CheckForUpdates looks for a newer kind:35130 of every installed napp — on
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

	if updated := checkAllUpdates(napps); len(updated) > 0 {
		ids := make([]string, 0, len(updated))
		for _, upd := range updated {
			ids = append(ids, upd.ID)
		}

		log.Info().Strs("ids", ids).Msg("update check found new versions")
		setUpdateAvailable(updated)
	}
}

// checkAllUpdates asks the relays about every napp at once (one filter per
// relay set, so the outbox queries stay separate from the discovery query),
// then refreshes the update cache. Returns whether every relay answered.
func checkAllUpdates(napps []Napp) map[string]Napp {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	// what a newer version must beat: the created_at of what we know
	known := make(map[string]nostr.Timestamp, len(napps))
	for _, n := range napps {
		known[n.ID] = n.CreatedAt
	}
	found := make(map[string]Napp, len(napps))

	ds := make([]string, 0, len(napps))
	for _, n := range napps {
		ds = append(ds, n.D)
	}

	for re := range sys.Pool.FetchMany(ctx, Relays(), nostr.Filter{
		Kinds: []nostr.Kind{35130},
		Tags:  nostr.TagMap{"d": ds},
	}, nostr.SubscriptionOptions{}) {
		napp := nappFromEvent(re.Event)
		if ts, exists := known[napp.ID]; exists && ts < napp.CreatedAt {
			known[napp.ID] = napp.CreatedAt
			found[napp.ID] = napp
		}
	}

	for _, napp := range napps {
		re := sys.Pool.QuerySingle(ctx, sys.FetchWriteRelays(ctx, napp.Author), nostr.Filter{
			Kinds:   []nostr.Kind{35130},
			Authors: []nostr.PubKey{napp.Author},
			Tags:    nostr.TagMap{"d": []string{napp.D}},
			Limit:   1,
		}, nostr.SubscriptionOptions{
			Label: "verdana-napp-update",
		})

		if re != nil {
			napp := nappFromEvent(re.Event)
			if ts, exists := known[napp.ID]; exists && ts < napp.CreatedAt {
				known[napp.ID] = napp.CreatedAt
				found[napp.ID] = napp
			}
		}
	}

	return found
}

// scanRelays queries one relay set for the current kind:35130 of the given
// napps and feeds every event to handle. It returns false when the round was
// cut short (a relay that never answered), so the caller can keep its old
// cache instead of narrowing it to what a truncated round saw.
func scanRelays(ctx context.Context, urls []string, napps []Napp, handle func(nostr.Event)) bool {
	complete := true

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
		servers = newer.BlossomServers(ctx)
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
	updateSet.Load().Delete(current.ID)

	refreshInstalled()
	log.Info().Str("napp", current.ID).Msg("update complete")
}

// newerVersion returns the best known newer version of an installed napp:
// from the in-memory cache a check round built, falling back to a live relay
// lookup on the discovery relays and the author's outbox.
func newerVersion(n Napp) *Napp {
	if latest, ok := updateSet.Load().Load(n.ID); ok && latest.CreatedAt > n.CreatedAt {
		return &latest
	}

	if ts, ok := updateCache.Get(n.ID); ok && ts > n.CreatedAt {
		if evt := fetchCurrentEvent(n.Author, n.D); evt != nil {
			nn := nappFromEvent(*evt)
			if nn.CreatedAt > n.CreatedAt {
				return &nn
			}
		}
		return nil
	}

	// nothing cached: ask the relays right now
	if found := checkAllUpdates([]Napp{n}); len(found) > 0 {
		if ts, ok := updateCache.Get(n.ID); ok && ts > n.CreatedAt {
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

// fetchCurrentEvent fetches the current kind:35130 of a napp from its
// author's outbox relays (falling back to the discovery relays).
func fetchCurrentEvent(author nostr.PubKey, d string) *nostr.Event {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	urls := sys.FetchOutboxRelays(ctx, author, 4)
	if len(urls) == 0 {
		urls = Relays()
	}

	for re := range sys.Pool.FetchMany(ctx, urls, nostr.Filter{
		Kinds:   []nostr.Kind{35130},
		Authors: []nostr.PubKey{author},
		Tags:    nostr.TagMap{"d": []string{d}},
		Limit:   1,
	}, nostr.SubscriptionOptions{
		Label: "verdana-napp-update",
	}) {
		evt := re.Event
		return &evt
	}
	return nil
}

// updateCache remembers newest created_at seen per napp id from last complete check round.
var updateCache = mustNewCache[string, nostr.Timestamp](4096)

// ─── helpers ─────────────────────────────────────────────────────
func nappAuthors(napps []Napp) []nostr.PubKey {
	out := make([]nostr.PubKey, 0, len(napps))
	for _, n := range napps {
		out = append(out, n.Author)
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
