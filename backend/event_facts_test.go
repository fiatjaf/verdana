package backend

import (
	"slices"
	"testing"

	"fiatjaf.com/nostr"
)

func TestDescribeEvent(t *testing.T) {
	alice := nostr.Generate().Public().Hex()
	bob := nostr.Generate().Public().Hex()
	name := func(hex string) string { return describer{}.name(hex) }

	for _, c := range []struct {
		evt     nostr.Event
		prev    *nostr.Event
		object  string
		says    string
		warning bool
	}{
		{
			evt:    nostr.Event{Kind: 1, Content: "hi", Tags: nostr.Tags{{"p", alice}, {"p", bob}}},
			object: "a note",
			says:   "notifying " + name(alice) + " and " + name(bob),
		},
		{
			evt:    nostr.Event{Kind: 7, Content: "+", Tags: nostr.Tags{{"e", alice}, {"p", bob}, {"k", "30023"}}},
			object: "a reaction",
			says:   "liking an article by " + name(bob),
		},
		{
			evt:     nostr.Event{Kind: 3, Tags: nostr.Tags{{"p", alice}}},
			prev:    &nostr.Event{Kind: 3, Tags: nostr.Tags{{"p", alice}, {"p", bob}}},
			object:  "a follow list",
			says:    "of 1 profile (was 2), unfollowing " + name(bob),
			warning: true,
		},
		{
			evt:    nostr.Event{Kind: 0, Content: `{"name":"x","about":"y"}`},
			prev:   &nostr.Event{Kind: 0, Content: `{"name":"x","picture":"z"}`},
			object: "a profile",
			says:   "changing about, removing picture",
		},
		{
			evt:    nostr.Event{Kind: 10002, Tags: nostr.Tags{{"r", "wss://a.com/", "read"}, {"r", "wss://b.com"}}},
			prev:   &nostr.Event{Kind: 10002, Tags: nostr.Tags{{"r", "wss://a.com"}, {"r", "wss://c.com"}}},
			object: "a relay list",
			says:   "with a.com (read) and b.com (new), dropping c.com",
		},
		{
			evt:    nostr.Event{Kind: 9734, Tags: nostr.Tags{{"amount", "21000"}, {"p", alice}}},
			object: "a zap request",
			says:   "for 21 sats to " + name(alice),
		},
		{
			evt:    nostr.Event{Kind: 12345},
			object: "an event",
		},
	} {
		d := describer{}
		says, warning := d.facts(&c.evt, c.prev)
		if got := aKind(c.evt.Kind, "event"); got != c.object {
			t.Errorf("kind %d: object %q, want %q", c.evt.Kind, got, c.object)
		}
		if says != c.says {
			t.Errorf("kind %d: says %q, want %q", c.evt.Kind, says, c.says)
		}
		if (warning != "") != c.warning {
			t.Errorf("kind %d: warning %q", c.evt.Kind, warning)
		}
	}
}

func TestEventPromptBody(t *testing.T) {
	d := eventDescription{Sentence: "notifying bob"}
	if got := d.body(", to 2 relays:", nil).Detail; got != "Notifying bob, to 2 relays:" {
		t.Errorf("got %q", got)
	}
	d = eventDescription{}
	if got := d.body(", to 2 relays:", nil).Detail; got != "To 2 relays:" {
		t.Errorf("got %q", got)
	}
	if got := d.body(".", nil).Detail; got != "" {
		t.Errorf("got %q", got)
	}
}

func TestAnswerKept(t *testing.T) {
	items := []string{"a", "b", "c"}
	if got := (Answer{}).kept(items); !slices.Equal(got, items) {
		t.Errorf("nothing skipped: got %v", got)
	}
	if got := (Answer{Skip: []int{0, 2, 7}}).kept(items); !slices.Equal(got, []string{"b"}) {
		t.Errorf("got %v", got)
	}
}
