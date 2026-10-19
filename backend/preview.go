package backend

import (
	"context"
	"sort"
	"time"
)

// Previews: napps opened without installing them. They are downloaded and
// kept like installed napps (same files, storage, windows, pins, updates) but
// listed apart, in state.PreviewNapps: they stay off the installed list, and
// are only offered as the handler of an action while a window of theirs is
// open. Installing one moves it to the installed list, uninstalling it
// removes it like any other.

// Preview opens a napp without installing it, downloading it first if it isn't
// on disk yet. Blocking: call it from a goroutine (the download shows up as
// IsBusy). A napp that is already installed or previewed is just opened, but
// with checkUpdate a previewed one is first brought up to date: what a card
// asks for, since unlike the napp's own page (which checks when it opens)
// nothing may have looked for a newer version of it in a while.
func Preview(n Napp, checkUpdate bool) {
	if devLookup(n.ID) != nil {
		LaunchDev(n.ID)
		return
	}
	if local, ok := InstalledNapp(n.ID); ok {
		if checkUpdate && IsPreview(n.ID) {
			local = updatePreview(local)
		}
		Launch(local)
		return
	}

	log.Info().Str("napp", n.ID).Str("name", n.Name).Msg("downloading napp for preview")
	setBusy(n.ID, true)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	err := replaceNappFiles(ctx, n, nappBaseDir(n.ID), n.BlossomServers(ctx))
	cancel()
	if err != nil {
		setBusy(n.ID, false)
		log.Error().Err(err).Str("napp", n.ID).Msg("preview failed")
		SetFetchErr("preview failed: " + err.Error())
		return
	}

	stateMu.Lock()
	if _, ok := state.InstalledNapps[n.ID]; !ok {
		// an install that finished meanwhile wins
		state.PreviewNapps[n.ID] = n
		saveState()
	}
	stateMu.Unlock()
	setBusy(n.ID, false)

	Launch(n)
}

// updatePreview looks for a newer version of a previewed napp and installs it
// if there is one, returning the napp as it is on disk afterwards.
func updatePreview(n Napp) Napp {
	setBusy(n.ID, true)
	defer setBusy(n.ID, false)

	CheckForUpdate(n.ID)
	if latest, ok := updateSet.Load().Load(n.ID); ok && latest.CreatedAt > n.CreatedAt {
		log.Info().Str("napp", n.ID).Msg("updating previewed napp before opening it")
		applyUpdate(n, latest)
	}
	if local, ok := InstalledNapp(n.ID); ok {
		return local
	}
	return n
}

// PreviewByID previews a napp the launcher knows about (discovered,
// installed or dev) by id (see Preview for checkUpdate).
func PreviewByID(id string, checkUpdate bool) bool {
	n, ok := LookupNapp(id)
	if !ok {
		return false
	}
	go Preview(n, checkUpdate)
	return true
}

// IsPreview says whether a napp is previewed (on disk, not installed).
func IsPreview(id string) bool {
	stateMu.Lock()
	defer stateMu.Unlock()
	_, ok := state.PreviewNapps[id]
	return ok
}

// previewNapps lists the previewed napps by name.
func previewNapps() []Napp {
	stateMu.Lock()
	list := make([]Napp, 0, len(state.PreviewNapps))
	for _, n := range state.PreviewNapps {
		list = append(list, n)
	}
	stateMu.Unlock()
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	return list
}
