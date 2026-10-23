package backend

import (
	"context"
	"encoding/json"
	"errors"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore"
	"github.com/puzpuzpuz/xsync/v3"
)

// Everything that goes into the store goes through saveEvent (the sdk's own
// saves too, through storePublisher), so that window.nostrdb.subscribe sees
// every event the store did not have before, whoever brought it.

// saveEvent stores evt the way its kind wants (a replaceable or addressable
// one replaces its older version) and answers whether the store took it as
// new. New events are handed to every store subscription they match.
func saveEvent(evt nostr.Event) (bool, error) {
	if evt.Kind.IsEphemeral() {
		return false, nil
	}

	if evt.Kind.IsRegular() {
		if err := sys.Store.SaveEvent(evt); err != nil {
			if errors.Is(err, eventstore.ErrDupEvent) {
				return false, nil
			}
			return false, err
		}
		notifyStoreSubs(evt)
		return true, nil
	}

	// ReplaceEvent doesn't say whether it kept evt or a newer one it had
	if storeHas(evt.ID) {
		return false, nil
	}
	if _, err := sys.Store.ReplaceEvent(evt); err != nil {
		return false, err
	}
	if !storeHas(evt.ID) {
		return false, nil
	}
	notifyStoreSubs(evt)
	return true, nil
}

func storeHas(id nostr.ID) bool {
	for range sys.Store.QueryEvents(nostr.Filter{IDs: []nostr.ID{id}}, 1) {
		return true
	}
	return false
}

// storePublisher is the sdk's sys.Publisher, so that what its loaders fetch
// goes through saveEvent too.
type storePublisher struct{}

func (storePublisher) Publish(_ context.Context, evt nostr.Event) error {
	_, err := saveEvent(evt)
	return err
}

// ─── store subscriptions (nostrdb.subscribe) ────────────────────
// Filters a napp holds open on the store: every event saved from then on that
// matches one of them is handed over, one by one. What was stored before
// comes from nostrdb.query. They live in ci.subs beside the relay
// subscriptions, so they end the same way (nostrdb.unsubscribe, the window
// closing).

type storeSub struct {
	ci         *Instance
	callbackID int
	filters    []nostr.Filter
}

var storeSubs = xsync.NewMapOf[*storeSub, struct{}]()

func startStoreSub(ctx context.Context, ci *Instance, callbackID int, filters []nostr.Filter) {
	sub := &storeSub{ci: ci, callbackID: callbackID, filters: filters}
	storeSubs.Store(sub, struct{}{})
	go func() {
		<-ctx.Done()
		storeSubs.Delete(sub)
	}()
}

// notifyStoreSubs hands a newly saved event to every store subscription it
// matches.
func notifyStoreSubs(evt nostr.Event) {
	var matching []*storeSub
	storeSubs.Range(func(sub *storeSub, _ struct{}) bool {
		for _, f := range sub.filters {
			if f.Matches(evt) {
				matching = append(matching, sub)
				break
			}
		}
		return true
	})
	if len(matching) == 0 {
		return
	}

	raw, err := json.Marshal(evt)
	if err != nil {
		return
	}
	for _, sub := range matching {
		sub.deliver(string(raw))
	}
}

func (s *storeSub) deliver(event string) {
	ci := s.ci
	_, open := ci.subs.Load(s.callbackID)
	if !open {
		return
	}
	if ci.napp.IsWasm() {
		ci.send(WireMsg{T: "stored", ID: s.callbackID, Params: event})
		return
	}
	ci.eval("window.__bridge_store_callback(" + jsNumber(s.callbackID) + "," + jsString(event) + ")")
}
