package backend

import (
	"testing"
	"time"

	"fiatjaf.com/nostr"
)

type discoveryPublish struct {
	list []Napp
	done bool
}

func testNappEvent(pk nostr.PubKey, d, title string, at nostr.Timestamp) nostr.RelayEvent {
	return nostr.RelayEvent{Event: nostr.Event{
		Kind:      KindNapp,
		PubKey:    pk,
		CreatedAt: at,
		Tags:      nostr.Tags{{"d", d}, {"title", title}},
	}}
}

func TestDiscoveryShowsNappsBeforeEOSE(t *testing.T) {
	pk := nostr.Generate().Public()
	events := make(chan nostr.RelayEvent)
	eose := make(chan struct{})
	published := make(chan discoveryPublish, 16)

	go func() {
		collectDiscovery(events, eose, 10*time.Millisecond, func(list []Napp, done bool) {
			published <- discoveryPublish{list, done}
		})
		close(published)
	}()

	// a slow relay holds back EOSE; what the fast ones sent still shows up
	events <- testNappEvent(pk, "notes", "Notes", 1)
	select {
	case p := <-published:
		if p.done || len(p.list) != 1 || p.list[0].Name != "Notes" {
			t.Fatalf("early publish = %+v, want just Notes, not done", p)
		}
	case <-time.After(time.Second):
		t.Fatal("napp was held back until EOSE")
	}

	close(eose)
	if p := <-published; !p.done || len(p.list) != 1 {
		t.Fatalf("EOSE publish = %+v, want Notes, done", p)
	}

	// events after EOSE keep coming in
	events <- testNappEvent(pk, "chat", "Chat", 1)
	if p := <-published; len(p.list) != 2 {
		t.Fatalf("live publish = %+v, want two napps", p)
	}
	close(events)
	for range published {
	}
}

func TestDiscoveryKeepsNewestVersion(t *testing.T) {
	pk := nostr.Generate().Public()
	events := make(chan nostr.RelayEvent, 4)
	events <- testNappEvent(pk, "notes", "Notes v2", 2)
	events <- testNappEvent(pk, "notes", "Notes v1", 1)
	events <- testNappEvent(pk, "notes", "Notes v3", 3)
	close(events)

	var last discoveryPublish
	collectDiscovery(events, make(chan struct{}), time.Hour, func(list []Napp, done bool) {
		last = discoveryPublish{list, done}
	})
	if !last.done || len(last.list) != 1 || last.list[0].Name != "Notes v3" {
		t.Fatalf("final publish = %+v, want only Notes v3, done", last)
	}
}
