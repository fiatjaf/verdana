//go:build !linux || (!amd64 && !386)

package backend

import (
	"strings"
	"testing"
	"time"

	"fiatjaf.com/nostr"
)

func TestBoltStoreSaveDuringQuery(t *testing.T) {
	store, closeStore, err := openEventStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	sk := nostr.Generate()
	sign := func(evt nostr.Event) nostr.Event {
		if err := evt.Sign(sk); err != nil {
			t.Fatal(err)
		}
		return evt
	}
	if err := store.SaveEvent(sign(nostr.Event{Kind: 1, CreatedAt: nostr.Now(), Content: "first"})); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		for range store.QueryEvents(nostr.Filter{Kinds: []nostr.Kind{1}}, 10) {
			// a few megabytes, well past the file's first size
			for i := range 200 {
				evt := sign(nostr.Event{Kind: 1, CreatedAt: nostr.Now() + nostr.Timestamp(i), Content: strings.Repeat("x", 30000)})
				if err := store.SaveEvent(evt); err != nil {
					done <- err
					return
				}
			}
		}
		done <- nil
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		// closing would wait on the stuck transaction too
		t.Fatal("saving from inside a query loop deadlocked the store")
	}
	closeStore()
}
