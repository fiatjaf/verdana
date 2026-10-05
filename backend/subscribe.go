package backend

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"fiatjaf.com/nostr"
)

// A subscription is window.napp.utils.subscribe(relays, filter, opts): a plain
// REQ to the relays the napp names, with no outbox logic in between. Unlike a
// feed it is not batched on a timer: everything stored arrives in one go at
// EOSE, and after that every live event is handed over on its own.
//
// The pool gives us either an EOSE channel or the CLOSED reasons, never both,
// so each relay is driven here directly.

// DefaultMaxEoseTimeout is how long a subscription waits for every relay's
// EOSE before handing over what it has anyway.
const DefaultMaxEoseTimeout = 20 * time.Second

type subscribeParams struct {
	Relays []string     `json:"relays"`
	Filter nostr.Filter `json:"filter"`
	Label  string       `json:"label"`
	// MaxEoseTimeout is in milliseconds, 0 for the default.
	MaxEoseTimeout int `json:"maxEoseTimeout"`
	CallbackID     int `json:"callbackId"`
}

// nappSubscriptionLabel is the label a subscription gets when the napp names
// none: "<author prefix>-<d tag>".
func nappSubscriptionLabel(n Napp) string {
	return n.Author.Hex()[:16] + "-" + n.D
}

func startSubscription(ctx context.Context, ci *Instance, p subscribeParams) error {
	if sys == nil {
		return errors.New("no nostr system")
	}

	relays := make([]string, 0, len(p.Relays))
	for _, url := range p.Relays {
		if url = strings.TrimSpace(url); url != "" {
			relays = nostr.AppendUnique(relays, nostr.NormalizeURL(url))
		}
	}
	if len(relays) == 0 {
		return errors.New("no relays to subscribe to")
	}

	label := p.Label
	if label == "" {
		label = nappSubscriptionLabel(ci.napp)
	}
	timeout := DefaultMaxEoseTimeout
	if p.MaxEoseTimeout > 0 {
		timeout = time.Duration(p.MaxEoseTimeout) * time.Millisecond
	}

	log.Debug().Str("label", label).Int("relays", len(relays)).
		Int("callback", p.CallbackID).Msg("starting subscription")

	s := &subscription{
		ci:         ci,
		callbackID: p.CallbackID,
		seen:       make(map[nostr.ID]bool, 128),
		stored:     make([]nostr.Event, 0, 64),
		pending:    len(relays),
		live:       len(relays),
		reasons:    make(map[string]string, len(relays)),
	}

	timer := time.AfterFunc(timeout, s.eose)
	go func() {
		<-ctx.Done()
		timer.Stop()
	}()

	opts := nostr.SubscriptionOptions{Label: label, MaxWaitForEOSE: timeout}
	for _, url := range relays {
		go s.drive(ctx, url, p.Filter, opts)
	}
	return nil
}

type subscription struct {
	ci         *Instance
	callbackID int

	mu      sync.Mutex
	seen    map[nostr.ID]bool
	eosed   bool
	stored  []nostr.Event
	pending int // relays that have not sent EOSE (or ended) yet
	live    int // relays that have not ended yet
	reasons map[string]string
}

// drive keeps one relay's REQ open until ctx is done or the relay ends it.
// A connection that drops after EOSE is reopened for what is new since.
func (s *subscription) drive(ctx context.Context, url string, filter nostr.Filter, opts nostr.SubscriptionOptions) {
	eosed := false
	defer func() {
		if !eosed {
			s.relayEosed()
		}
	}()

	interval := 3 * time.Second
	for {
		if ctx.Err() != nil {
			return
		}

		relay, err := sys.Pool.EnsureRelay(url)
		if err != nil {
			if !eosed {
				s.relayEnded(url, "error: "+err.Error())
				return
			}
			goto reconnect
		}

		{
			hasAuthed := false
		subscribe:
			sub, err := relay.Subscribe(ctx, filter, opts)
			if err != nil {
				if !eosed {
					s.relayEnded(url, "error: "+err.Error())
					return
				}
				goto reconnect
			}
			interval = 3 * time.Second

			for {
				select {
				case <-ctx.Done():
					return
				case <-sub.EndOfStoredEvents:
					if !eosed {
						eosed = true
						s.relayEosed()
					}
				case reason := <-sub.ClosedReason:
					if strings.HasPrefix(reason, "auth-required:") && sys.Pool.AuthRequiredHandler != nil && !hasAuthed {
						if err := relay.Auth(ctx, sys.Pool.AuthRequiredHandler); err == nil {
							hasAuthed = true
							goto subscribe
						}
					}
					s.relayEnded(url, reason)
					return
				case evt, more := <-sub.Events:
					if !more {
						// the connection went away: ask again for whatever
						// comes from now on
						filter.Since = nostr.Now()
						goto reconnect
					}
					if mh := sys.Pool.EventMiddleware; mh != nil {
						mh(nostr.RelayEvent{Event: evt, Relay: relay})
					}
					s.event(ctx, evt)
				}
			}
		}

	reconnect:
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
		interval = min(10*time.Minute, interval*17/10)
	}
}

func (s *subscription) event(ctx context.Context, evt nostr.Event) {
	s.mu.Lock()
	if s.seen[evt.ID] {
		s.mu.Unlock()
		return
	}
	s.seen[evt.ID] = true
	if !s.eosed {
		s.stored = append(s.stored, evt)
		s.mu.Unlock()
	} else {
		s.mu.Unlock()
		s.deliver(subscriptionMsg{Type: "event", Event: &evt})
	}
	sys.Publisher.Publish(ctx, evt)
}

func (s *subscription) relayEosed() {
	s.mu.Lock()
	s.pending--
	done := s.pending <= 0
	s.mu.Unlock()
	if done {
		s.eose()
	}
}

// eose hands over everything stored, once: when the last relay sends its
// EOSE, or when the timeout says we have waited long enough.
func (s *subscription) eose() {
	s.mu.Lock()
	if s.eosed {
		s.mu.Unlock()
		return
	}
	s.eosed = true
	events := s.stored
	s.stored = nil
	s.mu.Unlock()
	if events == nil {
		events = []nostr.Event{}
	}
	s.deliver(subscriptionMsg{Type: "eose", Events: events})
}

// relayEnded records why a relay ended its part; once every relay has, the
// napp hears the reasons.
func (s *subscription) relayEnded(url, reason string) {
	s.mu.Lock()
	s.reasons[url] = reason
	s.live--
	var reasons map[string]string
	if s.live <= 0 {
		reasons = s.reasons
	}
	s.mu.Unlock()
	if reasons != nil {
		// stored events always come before the closing
		s.eose()
		s.deliver(subscriptionMsg{Type: "closed", Reasons: reasons})

		// nothing more can come: forget it
		s.ci.subMu.Lock()
		if cancel, ok := s.ci.subs[s.callbackID]; ok {
			cancel()
			delete(s.ci.subs, s.callbackID)
		}
		s.ci.subMu.Unlock()
	}
}

// subscriptionMsg is one thing a subscription tells the napp.
type subscriptionMsg struct {
	Type    string            `json:"type"` // "eose", "event" or "closed"
	Events  []nostr.Event     `json:"events,omitempty"`
	Event   *nostr.Event      `json:"event,omitempty"`
	Reasons map[string]string `json:"reasons,omitempty"`
}

func (s *subscription) deliver(m subscriptionMsg) {
	ci := s.ci
	ci.subMu.Lock()
	_, open := ci.subs[s.callbackID]
	ci.subMu.Unlock()
	if !open {
		return
	}

	raw, err := json.Marshal(m)
	if err != nil {
		log.Error().Err(err).Msg("failed to marshal subscription message")
		return
	}
	if ci.napp.IsWasm() {
		ci.send(WireMsg{T: "subscription", ID: s.callbackID, Params: string(raw)})
		return
	}
	ci.eval("window.__bridge_sub_callback(" + jsNumber(s.callbackID) + "," + jsString(string(raw)) + ")")
}
