package backend

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"path"
	"sort"
	"strings"

	"fiatjaf.com/nostr"
)

// The NIP-5D napplet manifest (nostr-protocol/nips#2303): NIP-5A's tag
// schema under kinds 35129 (named) and 15129 (root). Each file is a "path"
// tag, the napplet's content address is the NIP-5A aggregate of those tags
// (the "x" tag, marked "aggregate"), and the runtime recomputes it rather
// than trusting it. Only /index.html runs — a napplet is one self-contained
// file — but every listed blob is downloaded and verified like NIP-5D asks.
//
// Display and routing tags are read leniently, the way napp manifests are:
// title, description (or the content), requires, archetype, server, source.

// nip5dFromEvent reads a NIP-5D manifest. The id and signature were checked
// by the caller.
func nip5dFromEvent(evt nostr.Event) (Napp, error) {
	n := Napp{
		Format:        FormatNapplet,
		NappletSchema: SchemaNIP5D,
		Kind:          evt.Kind,
		Author:        evt.PubKey,
		CreatedAt:     evt.CreatedAt,
	}

	if addressable(evt.Kind) {
		n.D = evt.Tags.GetD()
		if n.D == "" {
			return Napp{}, errors.New("named napplet without a d tag")
		}
	}

	var declared string
	seenPath := map[string]bool{}
	roles := map[string]bool{}
	for _, tag := range evt.Tags {
		if len(tag) < 2 {
			continue
		}
		switch tag[0] {
		case "path":
			if len(tag) < 3 {
				return Napp{}, errors.New("malformed path tag")
			}
			p, sha := tag[1], strings.ToLower(tag[2])
			if !safeNappletPath(p) || !hex64.MatchString(sha) {
				return Napp{}, fmt.Errorf("bad path tag %q", p)
			}
			if seenPath[p] {
				return Napp{}, fmt.Errorf("path %q listed twice", p)
			}
			seenPath[p] = true
			n.Paths = append(n.Paths, NappPath{Path: p, Sha256: sha})
		case "x":
			// the aggregate; a bare x (no marker) is read as one too
			if len(tag) >= 3 && tag[2] != "aggregate" {
				continue
			}
			if declared != "" {
				return Napp{}, errors.New("more than one aggregate x tag")
			}
			declared = strings.ToLower(tag[1])
		case "title":
			if n.Name == "" {
				n.Name = tag[1]
			}
		case "description", "summary":
			if n.Description == "" {
				n.Description = tag[1]
			}
		case "server":
			if origin, ok := blossomOrigin(tag[1]); ok && !containsString(n.Servers, origin) {
				n.Servers = append(n.Servers, origin)
			}
		case "source":
			if validSource(tag[1]) {
				n.Sources = append(n.Sources, tag[1])
			}
		case "requires":
			if domainToken.MatchString(tag[1]) {
				n.RequiredDomains = appendUniqueString(n.RequiredDomains, tag[1])
			}
		case "archetype", "z":
			// ["archetype", "<role>", "napplet:<role>/<intent>"?]
			role := tag[1]
			if !domainToken.MatchString(role) {
				continue
			}
			if !roles[role] {
				roles[role] = true
				n.Roles = append(n.Roles, role)
			}
			if tag[0] == "archetype" && len(tag) >= 3 {
				c, err := parseConvention(nostr.Tag{"i", tag[2]})
				if err == nil && conventionRole(c.ID) == role {
					n.Conventions = appendConvention(n.Conventions, c)
				}
			}
		case "i":
			if c, err := parseConvention(tag); err == nil {
				n.Conventions = appendConvention(n.Conventions, c)
			}
		case "icon":
			// either an artifact-style ["icon", <sha>, <mime>] or a napp-style
			// path into the napplet's own files
			if len(tag) >= 3 && hex64.MatchString(tag[1]) && nappletIconMimes[tag[2]] != "" {
				n.IconSha, n.IconMime = tag[1], tag[2]
			} else {
				n.Icon = tag[1]
			}
		}
	}

	if len(n.Paths) == 0 {
		return Napp{}, errors.New("no path tags")
	}
	if _, ok := nappletIndexPath(n.Paths); !ok {
		return Napp{}, errors.New("no /index.html path")
	}
	aggregate := aggregateHash(n.Paths)
	if declared != "" && declared != aggregate {
		return Napp{}, fmt.Errorf("aggregate x %s does not match the path tags (%s)", declared, aggregate)
	}
	n.ArtifactHash = aggregate

	// a napp-style icon names one of the napplet's files
	if n.IconSha == "" && n.Icon != "" {
		want := strings.TrimPrefix(n.Icon, "/")
		for _, p := range n.Paths {
			if strings.TrimPrefix(p.Path, "/") == want {
				if m := mime.TypeByExtension(path.Ext(p.Path)); nappletIconMimes[m] != "" {
					n.IconSha, n.IconMime = p.Sha256, m
				}
			}
		}
	}

	// conventions only count for a role the napplet declared
	n.Conventions = filterConventions(n.Conventions, roles)
	for _, r := range n.Roles {
		n.Actions = append(n.Actions, "napplet:"+r+"/open")
	}
	for _, c := range n.Conventions {
		n.Actions = appendUniqueString(n.Actions, c.ID)
	}

	if n.Description == "" {
		n.Description = strings.TrimSpace(evt.Content)
	}
	if n.Name == "" {
		n.Name = n.D
	}
	if n.Name == "" {
		n.Name = "napplet"
	}
	n.ID = nappletID(evt.PubKey, n.D)
	if !addressable(evt.Kind) {
		// one root napplet per author: its id can't collide with a named one,
		// whose d is never empty
		n.ID = nappletID(evt.PubKey, "") + "root"
	}
	return n, nil
}

// aggregateHash is NIP-5A's content address of a file set: the sha256 of the
// sorted "<sha256> <path>\n" lines.
func aggregateHash(paths []NappPath) string {
	lines := make([]string, len(paths))
	for i, p := range paths {
		lines[i] = p.Sha256 + " " + p.Path + "\n"
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "")))
	return hex.EncodeToString(sum[:])
}

// nappletIndexPath is the file that runs: /index.html (NIP-5D also accepts
// the bare spellings).
func nappletIndexPath(paths []NappPath) (NappPath, bool) {
	for _, want := range []string{"/index.html", "index.html", "/"} {
		for _, p := range paths {
			if p.Path == want {
				return p, true
			}
		}
	}
	return NappPath{}, false
}

// safeNappletPath refuses paths that would land outside the napplet's own
// directory when installed.
func safeNappletPath(p string) bool {
	if p == "" || strings.ContainsAny(p, "\\\x00") {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return false
		}
	}
	return true
}

// blossomOrigin reads a server tag leniently: any https URL, reduced to its
// origin.
func blossomOrigin(raw string) (string, bool) {
	return httpsOrigin(nostr.Tag{"server", strings.TrimRight(strings.TrimSpace(raw), "/")})
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func appendConvention(list []NappletConvention, c NappletConvention) []NappletConvention {
	for _, have := range list {
		if have.ID == c.ID {
			return list
		}
	}
	return append(list, c)
}

func filterConventions(list []NappletConvention, roles map[string]bool) []NappletConvention {
	out := list[:0]
	for _, c := range list {
		if roles[conventionRole(c.ID)] {
			out = append(out, c)
		}
	}
	return out
}
