package main

import (
	"context"
	"sort"
	"time"

	"fiatjaf.com/nostr"
)

func doFetch(relays []string) {
	urls := relays
	if len(urls) == 0 {
		urls = parseRelays(defaultRelays[0] + "\n" + defaultRelays[1])
	}

	log.Info().Strs("relays", urls).Msg("fetching napps from relays")

	ui.mu.Lock()
	ui.fetching = true
	ui.fetchErr = ""
	ui.discovery = nil
	ui.mu.Unlock()
	gioWin.Invalidate()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	seen := make(map[nostr.ID]bool)
	var collected []Napp

	ch := sys.Pool.FetchMany(ctx, urls,
		nostr.Filter{Kinds: []nostr.Kind{35128}},
		nostr.SubscriptionOptions{},
	)
	for re := range ch {
		if seen[re.ID] {
			continue
		}
		seen[re.ID] = true
		collected = append(collected, nappFromEvent(re.Event))

		ui.mu.Lock()
		ui.discovery = append([]Napp(nil), collected...)
		ui.mu.Unlock()
		gioWin.Invalidate()
	}

	sort.Slice(collected, func(i, j int) bool { return collected[i].Name < collected[j].Name })

	log.Info().Int("count", len(collected)).Msg("fetch complete")

	ui.mu.Lock()
	ui.fetching = false
	ui.mu.Unlock()
	gioWin.Invalidate()
}

func tagValue(tags nostr.Tags, key string) string {
	if t := tags.Find(key); len(t) > 1 {
		return t[1]
	}
	return ""
}

func nappFromEvent(evt nostr.Event) Napp {
	d := evt.Tags.GetD()
	n := Napp{
		D:           d,
		ID:          evt.PubKey.Hex()[:16] + "~" + d,
		Name:        tagValue(evt.Tags, "title"),
		Description: tagValue(evt.Tags, "description"),
		Icon:        tagValue(evt.Tags, "icon"),
		Author:      evt.PubKey,
		CreatedAt:   evt.CreatedAt,
	}
	if n.Name == "" {
		n.Name = d
	}
	n.Singleton = evt.Tags.Has("singleton")
	for t := range evt.Tags.FindAll("action") {
		if len(t) > 1 {
			n.Actions = append(n.Actions, t[1])
		}
	}
	for t := range evt.Tags.FindAll("requires") {
		if len(t) > 1 {
			n.Requires = append(n.Requires, t[1])
		}
	}
	for t := range evt.Tags.FindAll("path") {
		if len(t) > 2 {
			n.Paths = append(n.Paths, NappPath{Path: t[1], Sha256: t[2]})
		}
	}
	for t := range evt.Tags.FindAll("server") {
		if len(t) > 1 {
			n.Servers = append(n.Servers, t[1])
		}
	}
	return n
}
