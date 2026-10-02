package backend

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip19"
)

// Napps and napplets are addressable: an naddr (or its "<kind>:<pubkey>:<d>"
// coordinate) names one for good, across every update. The launcher can take
// one from the discovery filter, a startup argument or a nostr: link, look it
// up on relays and install or open it.

// naddrPattern finds an naddr inside whatever was pasted: a bare code, a
// nostr: URI or a web link that carries one in its path.
var naddrPattern = regexp.MustCompile(`naddr1[02-9ac-hj-np-z]{20,}`)

// ParseNappAddress reads a napp or napplet address: an naddr, a nostr: URI
// or web link containing one, or a "<kind>:<pubkey hex>:<d>" coordinate. The
// kind must be one the launcher runs.
func ParseNappAddress(input string) (nostr.EntityPointer, error) {
	s := strings.TrimSpace(input)
	if s == "" {
		return nostr.EntityPointer{}, errors.New("empty address")
	}

	var ptr nostr.EntityPointer
	if code := naddrPattern.FindString(strings.ToLower(s)); code != "" {
		prefix, data, err := nip19.Decode(code)
		if err != nil || prefix != "naddr" {
			return nostr.EntityPointer{}, fmt.Errorf("invalid naddr: %w", err)
		}
		ptr = data.(nostr.EntityPointer)
	} else {
		p, err := nostr.ParseAddrString(strings.TrimPrefix(s, "nostr:"))
		if err != nil {
			return nostr.EntityPointer{}, errors.New("not a napp address")
		}
		ptr = p
	}

	if !isNapKind(ptr.Kind) {
		return nostr.EntityPointer{}, fmt.Errorf("kind %d is not a napp or napplet", ptr.Kind)
	}
	if addressable(ptr.Kind) && ptr.Identifier == "" {
		return nostr.EntityPointer{}, errors.New("address has no d tag")
	}
	return ptr, nil
}

// IsNappAddress says whether input reads as an address, for a UI deciding
// between filtering its list and looking something up.
func IsNappAddress(input string) bool {
	_, err := ParseNappAddress(input)
	return err == nil
}

func isNapKind(k nostr.Kind) bool {
	for _, nk := range napKinds {
		if nk == k {
			return true
		}
	}
	return false
}

// addressFilter is the query for the current manifest at ptr. A root
// napplet (kind 15129) is replaceable, so it has no d tag to match.
func addressFilter(ptr nostr.EntityPointer) nostr.Filter {
	f := nostr.Filter{Kinds: []nostr.Kind{ptr.Kind}, Authors: []nostr.PubKey{ptr.PublicKey}}
	if addressable(ptr.Kind) {
		f.Tags = nostr.TagMap{"d": []string{ptr.Identifier}}
	}
	return f
}

// matchesAddress says whether a napp is the one ptr names.
func (n Napp) matchesAddress(ptr nostr.EntityPointer) bool {
	if n.Author != ptr.PublicKey || n.ManifestKind() != ptr.Kind {
		return false
	}
	return !addressable(ptr.Kind) || n.D == ptr.Identifier
}

// Naddr is the napp's address as an naddr, for sharing it.
func (n Napp) Naddr() string {
	if n.Author == (nostr.PubKey{}) {
		return ""
	}
	return nip19.EncodeNaddr(n.Author, n.ManifestKind(), n.D, nil)
}

// resolveTimeout bounds one address lookup across every relay asked.
const resolveTimeout = 15 * time.Second

// ResolveNappAddress finds the newest valid manifest at an address: in the
// local store, then on the address's relay hints, the author's write relays
// and the launcher's relays. Blocking.
func ResolveNappAddress(ctx context.Context, input string) (Napp, error) {
	ptr, err := ParseNappAddress(input)
	if err != nil {
		return Napp{}, err
	}
	if sys == nil {
		return Napp{}, errors.New("not started")
	}
	ctx, cancel := context.WithTimeout(ctx, resolveTimeout)
	defer cancel()

	var (
		best    Napp
		found   bool
		invalid bool
	)
	consider := func(evt nostr.Event) {
		if evt.PubKey != ptr.PublicKey || evt.Kind != ptr.Kind ||
			(addressable(ptr.Kind) && evt.Tags.GetD() != ptr.Identifier) {
			return
		}
		n, ok := nappFromEvent(evt)
		if !ok {
			invalid = true
			return
		}
		if !found || n.CreatedAt > best.CreatedAt {
			best, found = n, true
		}
	}

	filter := addressFilter(ptr)
	for evt := range sys.Store.QueryEvents(filter, 10) {
		consider(evt)
	}

	// relay hints are only a place to look, and only public ws(s) ones are
	// asked: a pasted link mustn't make the launcher dial into the network
	// it sits on
	urls := []string{}
	for _, r := range ptr.Relays {
		if u, err := napExplicitRelay(ctx, r); err == nil {
			urls = nostr.AppendUnique(urls, u)
		}
	}
	for _, r := range sys.FetchWriteRelays(ctx, ptr.PublicKey) {
		urls = nostr.AppendUnique(urls, r)
	}
	for _, r := range Relays() {
		urls = nostr.AppendUnique(urls, r)
	}
	if len(urls) > 0 {
		for re := range sys.Pool.FetchMany(ctx, urls, filter, nostr.SubscriptionOptions{Label: "verdana-address"}) {
			consider(re.Event)
		}
	}

	if !found {
		if invalid {
			return Napp{}, errors.New("the manifest at that address is not a valid napp or napplet")
		}
		return Napp{}, errors.New("no napp or napplet found at that address")
	}
	if best.AuthorName == "" {
		best.AuthorName = best.AuthorShortName()
	}
	return best, nil
}

// ─── the launcher's lookup ───────────────────────────────────────

// AddressLookup is the state of the last address typed into the discovery
// filter: still being looked up, found (NappID, also listed in Discovery) or
// failed (Err).
type AddressLookup struct {
	Query   string `json:"query"`
	Pending bool   `json:"pending"`
	NappID  string `json:"nappId,omitempty"`
	Err     string `json:"err,omitempty"`
}

// LookupAddress starts looking up an address typed into the discovery
// filter, if it is one and isn't already being (or been) looked up. The
// result shows up in State.Lookup, and the napp itself in State.Discovery,
// where the usual install and open buttons apply to it. Cheap to call on
// every keystroke.
func LookupAddress(input string) {
	q := strings.TrimSpace(input)
	if !IsNappAddress(q) {
		ls.mu.Lock()
		changed := ls.lookup != nil
		ls.lookup = nil
		ls.mu.Unlock()
		if changed {
			notifyState()
		}
		return
	}

	ls.mu.Lock()
	if ls.lookup != nil && ls.lookup.Query == q {
		ls.mu.Unlock()
		return
	}
	ls.lookup = &AddressLookup{Query: q, Pending: true}
	ls.mu.Unlock()
	notifyState()

	go func() {
		n, err := ResolveNappAddress(context.Background(), q)
		if err == nil {
			rememberResolved(n)
		}
		ls.mu.Lock()
		if ls.lookup == nil || ls.lookup.Query != q {
			// the filter moved on while we were looking
			ls.mu.Unlock()
			return
		}
		ls.lookup = &AddressLookup{Query: q}
		if err != nil {
			ls.lookup.Err = err.Error()
		} else {
			ls.lookup.NappID = n.ID
		}
		ls.mu.Unlock()
		notifyState()
	}()
}

// rememberResolved lists a napp found by its address among the discovered
// ones (where installing and the detail view look napps up), and keeps it
// there across discovery refreshes.
func rememberResolved(n Napp) {
	ls.mu.Lock()
	if ls.resolved == nil {
		ls.resolved = make(map[string]Napp)
	}
	if prev, ok := ls.resolved[n.ID]; !ok || n.CreatedAt >= prev.CreatedAt {
		ls.resolved[n.ID] = n
	}
	ls.discovery = ls.withResolved(ls.discovery)
	ls.sortDiscovery()
	ls.mu.Unlock()
	notifyState()
}

// withResolved adds the napps found by address to a discovery list, or
// swaps in the resolved copy where it is newer than the listed one.
func (l *launcherState) withResolved(list []Napp) []Napp {
	if len(l.resolved) == 0 {
		return list
	}
	out := append([]Napp(nil), list...)
	seen := make(map[string]int, len(out))
	for i, n := range out {
		seen[n.ID] = i
	}
	for _, n := range l.resolved {
		if i, ok := seen[n.ID]; ok {
			if n.CreatedAt > out[i].CreatedAt {
				out[i] = n
			}
			continue
		}
		out = append(out, n)
	}
	return out
}

// ─── opening ─────────────────────────────────────────────────────

// OpenAddress opens the napp or napplet at an address: it is launched right
// away when installed, and otherwise looked up and, once the user agrees,
// installed and launched. Blocking; meant for links and startup arguments,
// which come from outside the launcher, hence the question before
// installing anything.
func OpenAddress(input string) error {
	ptr, err := ParseNappAddress(input)
	if err != nil {
		return err
	}
	// a link that cold-started the launcher waits for its login, so the
	// window it opens can already sign (see waitStartupLogin)
	ctx, cancel := context.WithTimeout(context.Background(), shortcutActionTimeout)
	waitStartupLogin(ctx)
	cancel()

	if n, ok := installedAt(ptr); ok {
		Launch(n)
		return nil
	}

	n, err := ResolveNappAddress(context.Background(), input)
	if err != nil {
		return err
	}
	rememberResolved(n)
	if !askInstall(n) {
		// a no is an answer, not a failure
		return nil
	}
	if err := InstallNapp(n); err != nil {
		return err
	}
	Launch(n)
	return nil
}

// InstallAddress looks an address up and installs (or updates) what it
// names, without asking: the caller is the user acting in the launcher.
// Blocking.
func InstallAddress(input string) (Napp, error) {
	n, err := ResolveNappAddress(context.Background(), input)
	if err != nil {
		return Napp{}, err
	}
	rememberResolved(n)
	if err := InstallNapp(n); err != nil {
		return Napp{}, err
	}
	return n, nil
}

// installedAt is the installed napp at an address, if there is one.
func installedAt(ptr nostr.EntityPointer) (Napp, bool) {
	for _, n := range installedNapps() {
		if n.matchesAddress(ptr) {
			return n, true
		}
	}
	return Napp{}, false
}

// askInstall asks the user whether a napp that came in by address may be
// installed. Never remembered: each link is its own question.
func askInstall(n Napp) bool {
	what := "napp"
	if n.IsNapplet() {
		what = "napplet"
	}
	detail := "By " + n.AuthorShortName() + "."
	if n.Description != "" {
		detail = n.Description + "\n\n" + detail
	}
	p := newPrompt("Verdana", "Install and open the "+what+" "+n.Label()+"?", detail, n.Naddr(), nil)
	log.Info().Str("napp", n.ID).Msg("asking to install a napp opened by address")
	enqueuePrompt(p)
	return p.wait().OK
}
