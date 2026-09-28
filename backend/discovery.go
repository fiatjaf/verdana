package backend

import (
	"context"
	"strconv"
	"strings"
	"sync/atomic"

	"fiatjaf.com/nostr"
)

var cancelDiscover context.CancelFunc

func Discover() {
	if cancelDiscover != nil {
		cancelDiscover()
	}

	urls := Relays()
	log.Info().Strs("relays", urls).Msg("fetching napps from relays")

	setFetching(true)
	defer setFetching(false)

	ctx, cancel := context.WithCancel(context.Background())
	cancelDiscover = cancel

	var collected []Napp
	var eosed atomic.Bool

	events, eose := sys.Pool.SubscribeManyNotifyEOSE(ctx, urls,
		nostr.Filter{
			Kinds: []nostr.Kind{35130},
		},
		nostr.SubscriptionOptions{
			Label: "verdana-discovery",
		},
	)

	go func() {
		<-eose
		log.Info().Int("count", len(collected)).Msg("fetch complete")
		eosed.Store(true)
		setDiscovery(collected)
	}()

	for re := range events {
		collected = append(collected, nappFromEvent(re.Event))
		if eosed.Load() {
			setDiscovery(collected)
		}
	}

	log.Info().Err(context.Cause(ctx)).Msg("discovery subscription ended")
}

func nappFromEvent(evt nostr.Event) Napp {
	d := evt.Tags.GetD()
	n := Napp{
		D:         d,
		ID:        evt.PubKey.Hex()[:16] + "~" + d,
		Author:    evt.PubKey,
		CreatedAt: evt.CreatedAt,
	}

	for _, tag := range evt.Tags {
		if len(tag) < 1 {
			continue
		}

		if len(tag) < 2 {
			continue
		}
		switch tag[0] {
		case "title":
			n.Name = tag[1]
		case "description":
			n.Description = tag[1]
		case "icon":
			n.Icon = tag[1]
		case "action":
			n.Actions = append(n.Actions, tag[1])
		case "requires":
			n.Requires = append(n.Requires, tag[1])
		case "path":
			if len(tag) < 3 {
				continue
			}
			n.Paths = append(n.Paths, NappPath{Path: tag[1], Sha256: tag[2]})
		case "server":
			n.Servers = append(n.Servers, tag[1])
		case "initial_size", "initial-size":
			if len(tag) >= 3 {
				w, werr := strconv.Atoi(strings.TrimSpace(tag[1]))
				h, herr := strconv.Atoi(strings.TrimSpace(tag[2]))
				if werr == nil && herr == nil {
					if s, ok := sanitizeInitialSize(w, h); ok {
						n.InitialSize = &s
					}
				}
			}
		}
	}

	if n.Name == "" {
		n.Name = d
	}

	return n
}
