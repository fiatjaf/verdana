package backend

import (
	"testing"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip19"
)

func TestParseNappAddress(t *testing.T) {
	pk := nostr.Generate().Public()
	naddr := nip19.EncodeNaddr(pk, KindNapplet, "noris", []string{"wss://relay.example.com"})
	root := nip19.EncodeNaddr(pk, KindRootNapplet, "", nil)
	napp := nip19.EncodeNaddr(pk, KindNapp, "notes", nil)

	accepts := map[string]nostr.EntityPointer{
		naddr:                                   {PublicKey: pk, Kind: KindNapplet, Identifier: "noris"},
		"nostr:" + naddr:                        {PublicKey: pk, Kind: KindNapplet, Identifier: "noris"},
		"  " + naddr + "\n":                     {PublicKey: pk, Kind: KindNapplet, Identifier: "noris"},
		"https://napplet.run/" + naddr + "?x=1": {PublicKey: pk, Kind: KindNapplet, Identifier: "noris"},
		root:                                    {PublicKey: pk, Kind: KindRootNapplet},
		napp:                                    {PublicKey: pk, Kind: KindNapp, Identifier: "notes"},
		"35129:" + pk.Hex() + ":noris":          {PublicKey: pk, Kind: KindNapplet, Identifier: "noris"},
		"nostr:35130:" + pk.Hex() + ":with:colon": {PublicKey: pk, Kind: KindNapp, Identifier: "with:colon"},
	}
	for in, want := range accepts {
		got, err := ParseNappAddress(in)
		if err != nil {
			t.Errorf("%q rejected: %v", in, err)
			continue
		}
		if got.PublicKey != want.PublicKey || got.Kind != want.Kind || got.Identifier != want.Identifier {
			t.Errorf("%q: got %+v", in, got)
		}
	}

	rejects := []string{
		"",
		"noris",
		"abcdef0123456789~notes", // a napp id, i.e. a bundle token
		"abcdef0123456789~notes +eyJuYW1lIjoieCJ9",  // with an action
		nip19.EncodeNaddr(pk, 30023, "post", nil),   // not a napp kind
		nip19.EncodeNaddr(pk, KindNapplet, "", nil), // addressable with no d
		"1:" + pk.Hex() + ":x",
		"35129:nothex:noris",
		nip19.EncodeNpub(pk),
	}
	for _, in := range rejects {
		if _, err := ParseNappAddress(in); err == nil {
			t.Errorf("%q accepted", in)
		}
		if IsNappAddress(in) {
			t.Errorf("IsNappAddress(%q)", in)
		}
	}
}

func TestNappAddressRoundTrip(t *testing.T) {
	evs := realNapplets(t)
	n, err := nappletFromEvent(evs["noris"])
	if err != nil {
		t.Fatal(err)
	}
	ptr, err := ParseNappAddress(n.Naddr())
	if err != nil {
		t.Fatal(err)
	}
	if !n.matchesAddress(ptr) {
		t.Fatalf("%s doesn't name its own napplet", n.Naddr())
	}
	if !n.MatchesQuery(n.Naddr()) {
		t.Error("the filter doesn't match the napplet's own naddr")
	}

	// same author and d, other kind: a napp and a napplet may share a d tag
	other := nip19.EncodeNaddr(n.Author, KindNapp, n.D, nil)
	if n.MatchesQuery(other) {
		t.Error("a napp address matched a napplet")
	}
	// a plain-text query still filters as before
	if !n.MatchesQuery("noris") {
		t.Error("name filter broke")
	}
}

func TestRootNappletAddress(t *testing.T) {
	sk := nostr.Generate()
	n := Napp{Author: sk.Public(), Format: FormatNapplet, Kind: KindRootNapplet}
	ptr, err := ParseNappAddress(n.Naddr())
	if err != nil {
		t.Fatal(err)
	}
	if !n.matchesAddress(ptr) {
		t.Fatal("root napplet doesn't match its address")
	}
	if f := addressFilter(ptr); f.Tags != nil || f.Kinds[0] != KindRootNapplet {
		t.Errorf("root filter: %+v", f)
	}
	if f := addressFilter(nostr.EntityPointer{PublicKey: sk.Public(), Kind: KindNapplet, Identifier: "x"}); f.Tags["d"][0] != "x" {
		t.Errorf("named filter: %+v", f)
	}
}

func TestResolvedNappsSurviveDiscovery(t *testing.T) {
	l := launcherState{}
	old := Napp{ID: "a", CreatedAt: 10}
	l.resolved = map[string]Napp{"a": {ID: "a", CreatedAt: 20}, "b": {ID: "b", CreatedAt: 5}}

	got := l.withResolved([]Napp{old, {ID: "c"}})
	if len(got) != 3 {
		t.Fatalf("want 3 napps, got %+v", got)
	}
	for _, n := range got {
		if n.ID == "a" && n.CreatedAt != 20 {
			t.Error("the newer resolved copy didn't win")
		}
	}

	l.resolved["a"] = Napp{ID: "a", CreatedAt: 1}
	for _, n := range l.withResolved([]Napp{old}) {
		if n.ID == "a" && n.CreatedAt != 10 {
			t.Error("an older resolved copy replaced the discovered one")
		}
	}
}
