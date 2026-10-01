package backend

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"slices"
	"strings"
	"testing"

	"fiatjaf.com/nostr"
)

const testArtifact = "186ea5fd14e88fd1ac49351759e7ab906fa94892002b60bf7f5a428f28ca1c99"

// validNappletTags is the WEB-NAPPLET.md example event's tags.
func validNappletTags() nostr.Tags {
	return nostr.Tags{
		{"d", "feed-reader"},
		{"x", testArtifact},
		{"server", "https://blossom.example.com"},
		{"title", "Feed Reader"},
		{"icon", "0c1b82b9559f922f6f921fe4ba7fd4c3d8b406630978e0e408f55b15674f5d27", "image/png"},
		{"source", "nostr://repo-ref"},
		{"z", "feed"},
		{"i", "napplet:feed/open", "filter", "relay"},
		{"R", "relay"},
		{"O", "theme"},
	}
}

func signedNapplet(t *testing.T, tags nostr.Tags, content string) nostr.Event {
	t.Helper()
	evt := nostr.Event{
		Kind:      KindNapplet,
		CreatedAt: 1700000000,
		Tags:      tags,
		Content:   content,
	}
	if err := evt.Sign(nostr.Generate()); err != nil {
		t.Fatal(err)
	}
	return evt
}

func TestNappletFromEventValid(t *testing.T) {
	evt := signedNapplet(t, validNappletTags(), "Displays and filters a chronological Nostr feed.")
	n, err := nappletFromEvent(evt)
	if err != nil {
		t.Fatalf("valid event rejected: %v", err)
	}
	if !n.IsNapplet() || n.ManifestKind() != KindNapplet {
		t.Errorf("not marked as a napplet: %+v", n)
	}
	if want := nappletID(evt.PubKey, "feed-reader"); n.ID != want || !strings.HasPrefix(n.ID, "napplet~") {
		t.Errorf("id = %q, want %q", n.ID, want)
	}
	if n.Address() != "35129:"+evt.PubKey.Hex()+":feed-reader" {
		t.Errorf("address = %q", n.Address())
	}
	if n.Name != "Feed Reader" || n.Description == "" {
		t.Errorf("display fields: %q / %q", n.Name, n.Description)
	}
	if n.ArtifactHash != testArtifact || len(n.Paths) != 1 || n.Paths[0] != (NappPath{Path: "/index.html", Sha256: testArtifact}) {
		t.Errorf("artifact: %q %+v", n.ArtifactHash, n.Paths)
	}
	if n.IconSha == "" || n.IconMime != "image/png" || n.IconHash() != n.IconSha {
		t.Errorf("icon: %q %q", n.IconSha, n.IconMime)
	}
	if !slices.Equal(n.Servers, []string{"https://blossom.example.com"}) {
		t.Errorf("servers: %v", n.Servers)
	}
	if !slices.Equal(n.Actions, []string{"napplet:feed/open"}) || !n.Handles("napplet:feed/open") {
		t.Errorf("actions: %v", n.Actions)
	}
	if len(n.Conventions) != 1 || !slices.Equal(n.Conventions[0].Params, []string{"filter", "relay"}) {
		t.Errorf("conventions: %+v", n.Conventions)
	}
	if !slices.Equal(n.RequiredDomains, []string{"relay"}) || !slices.Equal(n.OptionalDomains, []string{"theme"}) {
		t.Errorf("domains: R=%v O=%v", n.RequiredDomains, n.OptionalDomains)
	}

	// and through the kind dispatch the catalog uses
	if got, ok := nappFromEvent(evt); !ok || got.ID != n.ID {
		t.Errorf("nappFromEvent: ok=%v id=%q", ok, got.ID)
	}
}

func TestNappletFromEventRejects(t *testing.T) {
	replace := func(name string, tag nostr.Tag) nostr.Tags {
		out := nostr.Tags{}
		for _, t := range validNappletTags() {
			if t[0] != name {
				out = append(out, t)
			}
		}
		if tag != nil {
			out = append(out, tag)
		}
		return out
	}
	with := func(extra ...nostr.Tag) nostr.Tags { return append(validNappletTags(), extra...) }

	cases := map[string]nostr.Tags{
		"path, aggregate x wrong": with(nostr.Tag{"path", "/index.html", testArtifact}),
		"legacy requires":         with(nostr.Tag{"requires", "relay"}),
		"legacy C":                with(nostr.Tag{"C", "relay"}),
		"no d":                    replace("d", nil),
		"two d":                   with(nostr.Tag{"d", "other"}),
		"empty d":                 replace("d", nostr.Tag{"d", ""}),
		"no x":                    replace("x", nil),
		"uppercase x":             replace("x", nostr.Tag{"x", strings.ToUpper(testArtifact)}),
		"short x":                 replace("x", nostr.Tag{"x", "abcd"}),
		"x with extra":            replace("x", nostr.Tag{"x", testArtifact, "aggregate"}),
		"no title":                replace("title", nil),
		"no server":               replace("server", nil),
		"http server":             replace("server", nostr.Tag{"server", "http://blossom.example.com"}),
		"server with path":        replace("server", nostr.Tag{"server", "https://blossom.example.com/blobs"}),
		"server with query":       replace("server", nostr.Tag{"server", "https://blossom.example.com?a=b"}),
		"bad z":                   replace("z", nostr.Tag{"z", "Feed"}),
		"z with extra":            replace("z", nostr.Tag{"z", "feed", "x"}),
		"i without z":             replace("z", nil),
		"i bad param":             replace("i", nostr.Tag{"i", "napplet:feed/open", "1bad"}),
		"i dup param":             replace("i", nostr.Tag{"i", "napplet:feed/open", "a", "a"}),
		"i with query":            replace("i", nostr.Tag{"i", "napplet:feed/open?x=1"}),
		"i not napplet":           replace("i", nostr.Tag{"i", "https://feed/open"}),
		"i listed twice":          with(nostr.Tag{"i", "napplet:feed/open"}),
		"bad R":                   replace("R", nostr.Tag{"R", "NAP-RELAY"}),
		"O with extra":            replace("O", nostr.Tag{"O", "theme", "x"}),
		"tag with only a name ":   with(nostr.Tag{"z"}),
	}
	for name, tags := range cases {
		t.Run(name, func(t *testing.T) {
			evt := signedNapplet(t, tags, "desc")
			if _, err := nappletFromEvent(evt); err == nil {
				t.Fatal("accepted")
			}
			if _, ok := nappFromEvent(evt); ok {
				t.Fatal("listed by nappFromEvent")
			}
		})
	}

	t.Run("empty content", func(t *testing.T) {
		if _, err := nappletFromEvent(signedNapplet(t, validNappletTags(), "  ")); err == nil {
			t.Fatal("accepted")
		}
	})
	t.Run("bad signature", func(t *testing.T) {
		evt := signedNapplet(t, validNappletTags(), "desc")
		evt.Content = "tampered"
		if _, err := nappletFromEvent(evt); err == nil {
			t.Fatal("accepted")
		}
	})
}

func TestNappletFromEventLenient(t *testing.T) {
	tags := validNappletTags()
	// malformed optional metadata is dropped, not fatal
	for i, tag := range tags {
		switch tag[0] {
		case "icon":
			tags[i] = nostr.Tag{"icon", "nothex", "image/gif"}
		case "source":
			tags[i] = nostr.Tag{"source", "file:///home/me/repo"}
		}
	}
	tags = append(tags,
		nostr.Tag{"unknown", "whatever"},
		nostr.Tag{"server", "https://blossom.example.com/"}, // same origin again
		nostr.Tag{"O", "relay"},                             // R wins
	)
	n, err := nappletFromEvent(signedNapplet(t, tags, "desc"))
	if err != nil {
		t.Fatalf("rejected: %v", err)
	}
	if n.IconSha != "" || len(n.Sources) != 0 {
		t.Errorf("malformed metadata kept: icon=%q sources=%v", n.IconSha, n.Sources)
	}
	if len(n.Servers) != 1 {
		t.Errorf("repeated origin kept twice: %v", n.Servers)
	}
	if slices.Contains(n.OptionalDomains, "relay") {
		t.Errorf("R did not win over O: %v", n.OptionalDomains)
	}
}

func TestNappFromEventStillReadsNapps(t *testing.T) {
	evt := nostr.Event{Kind: KindNapp, Tags: nostr.Tags{{"d", "x"}, {"title", "X"}}}
	n, ok := nappFromEvent(evt)
	if !ok || n.IsNapplet() || n.ID == "" || strings.HasPrefix(n.ID, "napplet~") {
		t.Fatalf("napp misread: ok=%v %+v", ok, n)
	}
}

func TestCheckNappletIcon(t *testing.T) {
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.Black)
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := checkNappletIcon(buf.Bytes(), "image/png"); err != nil {
		t.Errorf("png rejected: %v", err)
	}
	if err := checkNappletIcon(buf.Bytes(), "image/jpeg"); err == nil {
		t.Error("png accepted as jpeg")
	}
	if err := checkNappletIcon([]byte("<svg/>"), "image/png"); err == nil {
		t.Error("garbage accepted")
	}
	if err := checkNappletIcon(buf.Bytes(), "image/svg+xml"); err == nil {
		t.Error("disallowed type accepted")
	}
}

// ─── NIP-5D manifests ────────────────────────────────────────────

// realNapplets are kind 35129 events as found on relays (testdata), keyed by
// d tag.
func realNapplets(t *testing.T) map[string]nostr.Event {
	t.Helper()
	raw, err := os.ReadFile("testdata/nip5d-napplets.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]nostr.Event{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var evt nostr.Event
		if err := json.Unmarshal([]byte(line), &evt); err != nil {
			t.Fatal(err)
		}
		out[evt.Tags.GetD()] = evt
	}
	return out
}

func TestNIP5DNappletsFromRelays(t *testing.T) {
	evs := realNapplets(t)

	n, err := nappletFromEvent(evs["noris"])
	if err != nil {
		t.Fatalf("noris rejected: %v", err)
	}
	if n.NappletSchema != SchemaNIP5D || !n.IsNapplet() || n.ManifestKind() != KindNapplet {
		t.Errorf("schema: %+v", n)
	}
	if n.Name != "Noris" || !strings.Contains(n.Description, "long-form") {
		t.Errorf("display: %q / %q", n.Name, n.Description)
	}
	if n.ArtifactHash != "3bda2086bf639b09d1e399910cfe938af3162e51920ede17140da5eda9a9d241" {
		t.Errorf("aggregate: %s", n.ArtifactHash)
	}
	if n.IndexHash() != "172cbf89a24b10aa0eb84d5b7344a1c1d62f8455779059e4f9757074c2bc54e9" {
		t.Errorf("index hash: %s", n.IndexHash())
	}
	if !slices.Equal(n.Servers, []string{"https://cdn.hzrd149.com", "https://blossom.primal.net"}) {
		t.Errorf("servers: %v", n.Servers)
	}
	if !slices.Equal(n.Roles, []string{"article", "highlight"}) {
		t.Errorf("roles: %v", n.Roles)
	}
	for _, a := range []string{"napplet:article/open", "napplet:article/author", "napplet:highlight/open"} {
		if !n.Handles(a) {
			t.Errorf("does not handle %s: %v", a, n.Actions)
		}
	}
	if !slices.Contains(n.RequiredDomains, "outbox") || !slices.Equal(n.MissingDomains(), []string{"outbox"}) {
		t.Errorf("requires: %v missing %v", n.RequiredDomains, n.MissingDomains())
	}

	// content "{}" and only a description tag
	opener, err := nappletFromEvent(evs["hosted-nowhere-opener"])
	if err != nil || opener.Description == "" || opener.Description == "{}" {
		t.Errorf("opener: %v %q", err, opener.Description)
	}

	// kind 35129 used for something else entirely: neither schema
	for d, evt := range evs {
		if strings.HasPrefix(d, "35128:") {
			if _, err := nappletFromEvent(evt); err == nil {
				t.Errorf("%s accepted", d)
			}
		}
	}
}

func nip5dTags(d string, paths ...NappPath) nostr.Tags {
	tags := nostr.Tags{{"d", d}, {"title", "T"}, {"server", "https://blossom.example.com"}}
	for _, p := range paths {
		tags = append(tags, nostr.Tag{"path", p.Path, p.Sha256})
	}
	return tags
}

func TestNIP5DManifestChecks(t *testing.T) {
	index := NappPath{Path: "/index.html", Sha256: testArtifact}
	asset := NappPath{Path: "/a.js", Sha256: strings.Repeat("ab", 32)}

	// x is optional; when there it must be the recomputed aggregate
	good := nip5dTags("app", index, asset)
	n, err := nappletFromEvent(signedNapplet(t, good, ""))
	if err != nil || n.ArtifactHash != aggregateHash([]NappPath{asset, index}) || n.IndexHash() != testArtifact {
		t.Fatalf("no x: %v %+v", err, n)
	}
	withX := append(nip5dTags("app", index, asset), nostr.Tag{"x", n.ArtifactHash, "aggregate"})
	if _, err := nappletFromEvent(signedNapplet(t, withX, "")); err != nil {
		t.Errorf("right x rejected: %v", err)
	}
	if n.Name != "T" || n.Description != "" {
		t.Errorf("display: %q %q", n.Name, n.Description)
	}

	bad := map[string]nostr.Tags{
		"wrong x":     append(nip5dTags("app", index), nostr.Tag{"x", strings.Repeat("0", 64), "aggregate"}),
		"no index":    nip5dTags("app", asset),
		"traversal":   nip5dTags("app", index, NappPath{Path: "/../../evil", Sha256: testArtifact}),
		"bad sha":     nip5dTags("app", NappPath{Path: "/index.html", Sha256: "nothex"}),
		"duplicate":   nip5dTags("app", index, index),
		"named, no d": nip5dTags("", index),
		"two aggregates": append(nip5dTags("app", index), nostr.Tag{"x", aggregateHash([]NappPath{index}), "aggregate"},
			nostr.Tag{"x", aggregateHash([]NappPath{index}), "aggregate"}),
	}
	for name, tags := range bad {
		if _, err := nappletFromEvent(signedNapplet(t, tags, "")); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}

	// a root napplet: kind 15129, no d, one per author
	root := signedEvent(t, KindRootNapplet, nostr.Tags{{"path", "/index.html", testArtifact}, {"title", "Root"}}, "")
	rn, err := nappletFromEvent(root)
	if err != nil {
		t.Fatalf("root rejected: %v", err)
	}
	if rn.ManifestKind() != KindRootNapplet || rn.D != "" || rn.Address() != "15129:"+root.PubKey.Hex()+":" ||
		rn.ID == nappletID(root.PubKey, "") {
		t.Errorf("root: %+v", rn)
	}
	if f := manifestFilter(rn); f.Tags != nil || f.Kinds[0] != KindRootNapplet {
		t.Errorf("root update filter: %+v", f)
	}
	// a root napplet in the artifact schema doesn't exist
	if _, err := nappletFromEvent(signedEvent(t, KindRootNapplet, validNappletTags(), "x")); err == nil {
		t.Error("artifact-schema root accepted")
	}
}

func TestManifestFilterIsKindSpecific(t *testing.T) {
	napplet := Napp{Format: FormatNapplet, Kind: KindNapplet, D: "same"}
	napp := Napp{D: "same"}
	if f := manifestFilter(napplet); !slices.Equal(f.Kinds, []nostr.Kind{KindNapplet}) || f.Tags["d"][0] != "same" {
		t.Errorf("napplet filter: %+v", f)
	}
	if f := manifestFilter(napp); !slices.Equal(f.Kinds, []nostr.Kind{KindNapp}) {
		t.Errorf("napp filter: %+v", f)
	}
}

func signedEvent(t *testing.T, kind nostr.Kind, tags nostr.Tags, content string) nostr.Event {
	t.Helper()
	evt := nostr.Event{Kind: kind, CreatedAt: 1700000000, Tags: tags, Content: content}
	if err := evt.Sign(nostr.Generate()); err != nil {
		t.Fatal(err)
	}
	return evt
}
