package backend

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/sdk"
)

// A napp configuration is a kind:35133 event saying how a launcher should be
// set up: which napps to install, which napp handles an action by default and
// which bundles go on the home tab. Applying one is the only way a default
// handler gets set.
//
//	["napp", "<kind>:<pubkey>:<d>", "<relay>"]
//	["napp", "<event-id>", "<relay>", "<author>"]
//	["action", "<action>", "<kind>:<pubkey>:<d>", "timed"|"suggested"|"forced", "singleton"|""]
//	["bundle", "<name>", "<icon-url>"|"", "napp <kind>:<pubkey>:<d> <action> <payload>|null …", …]
//	["sign", "ask"|"deny"|"allow"]
//	["publish", "ask"|"deny"|"allow"]
//	["auth-on-reads", "yes"|"no"]
//	["open-links", "yes"|"non-image"|"no"]
//
// Each "napp …" item of a bundle tag is one napp of the bundle with the
// actions it gets, each action followed by its payload as JSON (null for
// none). An item can also carry more napps, each starting at another "napp".
//
// Applying a configuration writes its default handlers, its "sign",
// "publish" and "open-links" (what napps get when the user has not answered
// for them, see NappPolicy) and its "auth-on-reads" into the launcher's
// settings, where
// the user can change them afterwards like anything else.

// KindConfig is a napp configuration.
const KindConfig = nostr.Kind(35133)

// HandlerMode is how hard a default handler is pushed on the user.
type HandlerMode string

const (
	// HandlerTimed puts the default at the top of the picker and sends the
	// action to it once a countdown runs out (see defaultCountdown).
	HandlerTimed HandlerMode = "timed"

	// HandlerSuggested puts the default at the top of the picker, and that
	// is all.
	HandlerSuggested HandlerMode = "suggested"

	// HandlerForced sends the action to the default with no picker at all.
	HandlerForced HandlerMode = "forced"
)

// defaultCountdown is how long a timed default waits before it takes the
// action. The picker adds to it while the user moves around in it.
const defaultCountdown = 3 * time.Second

// DefaultHandler is the napp an action goes to unless the user says
// otherwise. Singleton sends the action into the first open window of that
// napp, when there is one, rather than a new window.
type DefaultHandler struct {
	Action    string      `json:"action"`
	Target    string      `json:"target"`
	Mode      HandlerMode `json:"mode"`
	Singleton bool        `json:"singleton,omitempty"`
}

// NappRef is a napp a configuration installs: an address, or an event id
// with the author it is expected from.
type NappRef struct {
	Addr   string       `json:"addr,omitempty"`
	ID     nostr.ID     `json:"id,omitempty"`
	Author nostr.PubKey `json:"author,omitempty"`
	Relay  string       `json:"relay,omitempty"`
}

// ConfigBundle is a bundle a configuration puts on the home tab.
type ConfigBundle struct {
	Name    string          `json:"name"`
	Icon    string          `json:"icon,omitempty"`
	Entries []ShortcutEntry `json:"entries"`
}

// NappConfiguration is a kind:35133 event as the launcher keeps it.
type NappConfiguration struct {
	Author    nostr.PubKey     `json:"author"`
	D         string           `json:"d"`
	Name      string           `json:"name"`
	CreatedAt nostr.Timestamp  `json:"created_at"`
	Napps     []NappRef        `json:"napps"`
	Handlers  []DefaultHandler `json:"handlers"`
	Bundles   []ConfigBundle   `json:"bundles"`

	// Sign and Publish are what napps asking to sign or to publish get,
	// empty when the configuration says nothing about it. ReadAuth and
	// OpenLinks is nil and empty when it says nothing about authenticating
	// on reads or about napps opening links without asking.
	Sign      Decision   `json:"sign,omitempty"`
	Publish   Decision   `json:"publish,omitempty"`
	ReadAuth  *bool      `json:"readAuth,omitempty"`
	OpenLinks LinkPolicy `json:"openLinks,omitempty"`
}

// key is what a configuration is replaced by: the same author and d-tag.
func (c NappConfiguration) key() string { return c.Author.Hex() + ":" + c.D }

// Label is the configuration's title, falling back to its d-tag.
func (c NappConfiguration) Label() string {
	if c.Name != "" {
		return c.Name
	}
	if c.D != "" {
		return c.D
	}
	return "napp configuration"
}

// nappIDFromAddr is the id a napp gets from its "<kind>:<pubkey>:<d>" address
// (see nappFromEvent).
func nappIDFromAddr(addr string) (string, error) {
	ptr, err := nostr.ParseAddrString(addr)
	if err != nil {
		return "", fmt.Errorf("bad napp address %q: %w", addr, err)
	}
	if !isNappKind(ptr.Kind) {
		return "", fmt.Errorf("%q is not a napp address", addr)
	}
	return ptr.PublicKey.Hex()[:16] + "~" + ptr.Identifier, nil
}

// configFromEvent reads a kind:35133 event. Tags that don't make sense are
// skipped with a warning rather than failing the whole configuration.
func configFromEvent(evt nostr.Event) NappConfiguration {
	c := NappConfiguration{
		Author:    evt.PubKey,
		D:         evt.Tags.GetD(),
		CreatedAt: evt.CreatedAt,
	}
	for _, tag := range evt.Tags {
		if len(tag) < 2 {
			continue
		}
		switch tag[0] {
		case "title", "name":
			if c.Name == "" {
				c.Name = tag[1]
			}
		case "napp":
			ref, err := parseNappRef(tag)
			if err != nil {
				log.Warn().Err(err).Msg("skipping a napp of a configuration")
				continue
			}
			c.Napps = append(c.Napps, ref)
		case "action":
			h, err := parseHandlerTag(tag)
			if err != nil {
				log.Warn().Err(err).Msg("skipping a default handler of a configuration")
				continue
			}
			// one default per action: the first tag for it is the one
			if !slices.ContainsFunc(c.Handlers, func(o DefaultHandler) bool { return o.Action == h.Action }) {
				c.Handlers = append(c.Handlers, h)
			}
		case "sign", "publish":
			d := Decision(tag[1])
			switch d {
			case DecisionAsk, DecisionAllow, DecisionDeny:
			default:
				log.Warn().Str("tag", tag[0]).Str("value", tag[1]).Msg("skipping an unknown permission decision of a configuration")
				continue
			}
			if tag[0] == "sign" {
				c.Sign = d
			} else {
				c.Publish = d
			}
		case "auth-on-reads":
			switch tag[1] {
			case "yes", "no":
				on := tag[1] == "yes"
				c.ReadAuth = &on
			default:
				log.Warn().Str("value", tag[1]).Msg("skipping an unknown auth-on-reads of a configuration")
			}
		case "open-links":
			switch p := LinkPolicy(tag[1]); p {
			case LinksAll, LinksNonImage, LinksNone:
				c.OpenLinks = p
			default:
				log.Warn().Str("value", tag[1]).Msg("skipping an unknown open-links of a configuration")
			}
		case "bundle":
			b, err := parseBundleTag(tag)
			if err != nil {
				log.Warn().Err(err).Msg("skipping a bundle of a configuration")
				continue
			}
			c.Bundles = append(c.Bundles, b)
		}
	}
	return c
}

func parseNappRef(tag nostr.Tag) (NappRef, error) {
	var ref NappRef
	if len(tag) > 2 {
		ref.Relay = strings.TrimSpace(tag[2])
	}
	if strings.Contains(tag[1], ":") {
		if _, err := nappIDFromAddr(tag[1]); err != nil {
			return ref, err
		}
		ref.Addr = tag[1]
		return ref, nil
	}
	id, err := nostr.IDFromHex(tag[1])
	if err != nil {
		return ref, fmt.Errorf("bad napp reference %q", tag[1])
	}
	ref.ID = id
	if len(tag) > 3 && tag[3] != "" {
		pk, err := nostr.PubKeyFromHex(tag[3])
		if err != nil {
			return ref, fmt.Errorf("bad author %q for napp %s", tag[3], tag[1])
		}
		ref.Author = pk
	}
	return ref, nil
}

func parseHandlerTag(tag nostr.Tag) (DefaultHandler, error) {
	if len(tag) < 4 {
		return DefaultHandler{}, fmt.Errorf("action tag %v is too short", tag)
	}
	if err := validActionName(tag[1]); err != nil {
		return DefaultHandler{}, err
	}
	target, err := nappIDFromAddr(tag[2])
	if err != nil {
		return DefaultHandler{}, err
	}
	mode := HandlerMode(tag[3])
	switch mode {
	case HandlerTimed, HandlerSuggested, HandlerForced:
	default:
		return DefaultHandler{}, fmt.Errorf("unknown handler mode %q for %s", tag[3], tag[1])
	}
	return DefaultHandler{
		Action:    tag[1],
		Target:    target,
		Mode:      mode,
		Singleton: len(tag) > 4 && tag[4] == "singleton",
	}, nil
}

func parseBundleTag(tag nostr.Tag) (ConfigBundle, error) {
	b := ConfigBundle{Name: strings.TrimSpace(tag[1])}
	if b.Name == "" {
		return b, errors.New("bundle with no name")
	}
	if len(tag) > 2 {
		b.Icon = strings.TrimSpace(tag[2])
	}
	for _, item := range tag[min(3, len(tag)):] {
		entries, err := parseBundleItem(item)
		if err != nil {
			return b, fmt.Errorf("bundle %q: %w", b.Name, err)
		}
		b.Entries = append(b.Entries, entries...)
	}
	if len(b.Entries) == 0 {
		return b, fmt.Errorf("bundle %q has no napps", b.Name)
	}
	return b, nil
}

// parseBundleItem reads "napp <addr> <action> <payload> <action> <payload>…".
// A payload is any JSON value, spaces and all, so it is read with a JSON
// decoder rather than split on spaces.
func parseBundleItem(item string) ([]ShortcutEntry, error) {
	var entries []ShortcutEntry
	rest := strings.TrimSpace(item)
	word := func() string {
		w, after, _ := strings.Cut(rest, " ")
		rest = strings.TrimSpace(after)
		return w
	}
	for rest != "" {
		w := word()
		if w == "napp" {
			id, err := nappIDFromAddr(word())
			if err != nil {
				return nil, err
			}
			entries = append(entries, ShortcutEntry{NappID: id})
			continue
		}
		if len(entries) == 0 {
			return nil, fmt.Errorf("%q does not start with a napp", item)
		}
		if err := validActionName(w); err != nil {
			return nil, err
		}
		if rest == "" {
			return nil, fmt.Errorf("action %q has no payload (null for none)", w)
		}
		dec := json.NewDecoder(strings.NewReader(rest))
		var payload json.RawMessage
		if err := dec.Decode(&payload); err != nil {
			return nil, fmt.Errorf("bad payload for %q: %w", w, err)
		}
		rest = strings.TrimSpace(rest[dec.InputOffset():])
		if bytes.Equal(payload, []byte("null")) {
			payload = nil
		}
		last := &entries[len(entries)-1]
		last.Actions = append(last.Actions, ShortcutAction{Type: w, Payload: payload})
	}
	return entries, nil
}

// ─── applying a configuration ────────────────────────────────────

// lines are what a configuration sets, a line each, the way the settings
// list it (the prompt asking to apply it groups them: see sections).
func (c NappConfiguration) lines() []string {
	var lines []string
	for _, ref := range c.Napps {
		lines = append(lines, "install "+ref.label())
	}
	for _, h := range c.Handlers {
		line := "send “" + h.Action + "” to " + nappName(h.Target) + " (" + string(h.Mode)
		if h.Singleton {
			line += ", singleton"
		}
		lines = append(lines, line+")")
	}
	for _, b := range c.Bundles {
		lines = append(lines, "add the bundle “"+b.Name+"” to the home tab, with "+plural(len(b.Entries), "napp", "napps"))
	}
	return append(lines, c.permissionLines()...)
}

// sections are lines grouped under what they touch, the way the prompt
// asking to apply the configuration shows them.
func (c NappConfiguration) sections() []PromptSection {
	var sections []PromptSection
	add := func(heading string, lines []string) {
		if len(lines) > 0 {
			sections = append(sections, PromptSection{Heading: heading, Lines: lines})
		}
	}

	var napps []string
	for _, ref := range c.Napps {
		napps = append(napps, ref.label())
	}
	add("Install", napps)

	var handlers []string
	for _, h := range c.Handlers {
		line := "“" + h.Action + "” → " + nappName(h.Target) + " (" + string(h.Mode)
		if h.Singleton {
			line += ", singleton"
		}
		handlers = append(handlers, line+")")
	}
	add("Default handlers", handlers)

	var bundles []string
	for _, b := range c.Bundles {
		bundles = append(bundles, "“"+b.Name+"”, with "+plural(len(b.Entries), "napp", "napps"))
	}
	add("Bundles on the home tab", bundles)

	add("Permissions", c.permissionLines())
	return sections
}

// permissionLines say what a configuration sets the permissions to.
func (c NappConfiguration) permissionLines() []string {
	var lines []string
	for _, p := range []struct {
		what string
		d    Decision
	}{{"sign", c.Sign}, {"publish", c.Publish}} {
		switch p.d {
		case DecisionAllow:
			lines = append(lines, "let napps "+p.what+" without asking")
		case DecisionDeny:
			lines = append(lines, "never let napps "+p.what)
		case DecisionAsk:
			lines = append(lines, "ask before napps "+p.what)
		}
	}
	if c.ReadAuth != nil {
		if *c.ReadAuth {
			lines = append(lines, "authenticate to relays when reading")
		} else {
			lines = append(lines, "not authenticate to relays when reading")
		}
	}
	switch c.OpenLinks {
	case LinksAll:
		lines = append(lines, "let napps open links, images too, without asking")
	case LinksNonImage:
		lines = append(lines, "let napps open links without asking, showing images over the napp")
	case LinksNone:
		lines = append(lines, "ask before napps open links")
	}
	return lines
}

// label is a napp reference as a line can say it: the napp's name when the
// launcher has seen it, its address or event id otherwise.
func (ref NappRef) label() string {
	if ref.Addr != "" {
		if id, err := nappIDFromAddr(ref.Addr); err == nil {
			if name := nappName(id); name != id {
				return name
			}
		}
		if ptr, err := nostr.ParseAddrString(ref.Addr); err == nil {
			return "“" + ptr.Identifier + "”"
		}
		return ref.Addr
	}
	return "the napp " + ref.ID.Hex()[:12] + "…"
}

// nappName is a napp's name when the launcher knows it, its id otherwise.
func nappName(id string) string {
	if n, ok := InstalledNapp(id); ok {
		return n.Label()
	}
	if n, ok := DiscoveredNapp(id); ok {
		return n.Label()
	}
	if n, ok := seenNapp(id); ok {
		return n.Label()
	}
	return id
}

// offerConfiguration asks the user whether to apply a configuration and, if
// they agree, applies it. It returns right away.
func offerConfiguration(evt nostr.Event) {
	c := configFromEvent(evt)
	reloadKnownConfigs()
	go func() {
		lines := c.lines()
		if len(lines) == 0 {
			SetFetchErr(c.Label() + ": this configuration has nothing in it")
			return
		}
		p := newPrompt("launcher", "Apply the configuration “"+c.Label()+"”?", PromptBody{
			Sections: c.sections(),
		}, nil)
		enqueuePrompt(p)
		if !p.wait().OK {
			return
		}
		applyConfiguration(c)
	}()
}

// applyConfiguration keeps a configuration — replacing an older one from the
// same author and d-tag — and sets what it says: its default handlers and
// permissions over the ones there were, its bundles on the home tab and its
// napps installed. Blocking.
func applyConfiguration(c NappConfiguration) {
	stateMu.Lock()
	state.Configurations = slices.DeleteFunc(state.Configurations, func(o NappConfiguration) bool {
		return o.key() == c.key()
	})
	state.Configurations = append(state.Configurations, c)
	takeConfigSettingsLocked(c)
	saveState()
	stateMu.Unlock()
	reloadKnownConfigs()
	log.Info().Str("config", c.key()).Int("napps", len(c.Napps)).Int("handlers", len(c.Handlers)).
		Int("bundles", len(c.Bundles)).Msg("napp configuration applied")

	for _, b := range c.Bundles {
		putConfigBundle(b)
	}
	if c.ReadAuth != nil {
		SetReadAuth(*c.ReadAuth)
	}
	notifyState()

	for _, ref := range c.Napps {
		n, err := fetchNappRef(ref)
		if err != nil {
			log.Warn().Err(err).Str("config", c.key()).Msg("could not get a napp of a configuration")
			SetFetchErr(c.Label() + ": " + err.Error())
			continue
		}
		if installed, ok := InstalledNapp(n.ID); ok && IsInstalled(n.ID) && installed.CreatedAt >= n.CreatedAt {
			continue
		}
		Install(n)
	}
}

// ─── the configurations the launcher knows ───────────────────────

// ConfigurationInfo is a configuration as the settings list it: one line
// saying what it is, the lines of what it sets, and whether it was applied
// in this very version.
type ConfigurationInfo struct {
	Key        string          `json:"key"`
	Name       string          `json:"name"`
	Author     string          `json:"author"`
	AuthorName string          `json:"authorName,omitempty"`
	CreatedAt  nostr.Timestamp `json:"createdAt"`
	Summary    string          `json:"summary"`
	Lines      []string        `json:"lines"`
	Applied    bool            `json:"applied"`
}

// knownConfigs caches knownConfigurations, which reads the store: a
// snapshot asks for it every frame. Thrown away whenever a configuration is
// loaded or applied.
var knownConfigs atomic.Pointer[[]ConfigurationInfo]

func reloadKnownConfigs() {
	knownConfigs.Store(nil)
	notifyState()
}

// knownConfigurations is every configuration the launcher ever loaded — the
// newest version of each, from the local store — and the applied ones the
// store doesn't have any more, newest first.
func knownConfigurations() []ConfigurationInfo {
	if c := knownConfigs.Load(); c != nil {
		return *c
	}
	byKey := make(map[string]NappConfiguration)
	if sys != nil {
		for evt := range sys.Store.QueryEvents(nostr.Filter{Kinds: []nostr.Kind{KindConfig}}, 1000) {
			c := configFromEvent(evt)
			if prev, ok := byKey[c.key()]; ok && prev.CreatedAt >= c.CreatedAt {
				continue
			}
			byKey[c.key()] = c
		}
	}
	stateMu.Lock()
	applied := make(map[string]nostr.Timestamp, len(state.Configurations))
	for _, c := range state.Configurations {
		applied[c.key()] = c.CreatedAt
		if prev, ok := byKey[c.key()]; !ok || prev.CreatedAt < c.CreatedAt {
			byKey[c.key()] = c
		}
	}
	stateMu.Unlock()

	list := make([]ConfigurationInfo, 0, len(byKey))
	for key, c := range byKey {
		var parts []string
		if n := len(c.Napps); n > 0 {
			parts = append(parts, plural(n, "napp", "napps"))
		}
		if n := len(c.Handlers); n > 0 {
			parts = append(parts, plural(n, "default handler", "default handlers"))
		}
		if n := len(c.Bundles); n > 0 {
			parts = append(parts, plural(n, "bundle", "bundles"))
		}
		if c.Sign != "" || c.Publish != "" || c.ReadAuth != nil || c.OpenLinks != "" {
			parts = append(parts, "permissions")
		}
		if len(parts) == 0 {
			parts = append(parts, "nothing")
		}
		at, ok := applied[key]
		info := ConfigurationInfo{
			Key:       key,
			Name:      c.Label(),
			Author:    c.Author.Hex(),
			CreatedAt: c.CreatedAt,
			Summary:   and(parts),
			Lines:     c.lines(),
			Applied:   ok && at == c.CreatedAt,
		}
		if sys != nil {
			if pm, ok := sys.MetadataCache.Get(c.Author); ok {
				info.AuthorName = pm.ShortName()
			}
		}
		list = append(list, info)
	}
	slices.SortFunc(list, func(a, b ConfigurationInfo) int { return cmp.Compare(b.CreatedAt, a.CreatedAt) })
	if sys != nil {
		// before the store is up there is only half the list to keep
		knownConfigs.Store(&list)
	}
	return list
}

// ApplyConfiguration applies one of the configurations the launcher knows,
// by its key (see ConfigurationInfo), without asking: the user picked it
// from the list that shows what it sets. It returns right away.
func ApplyConfiguration(key string) error {
	var found *NappConfiguration
	if sys != nil {
		for evt := range sys.Store.QueryEvents(nostr.Filter{Kinds: []nostr.Kind{KindConfig}}, 1000) {
			if c := configFromEvent(evt); c.key() == key && (found == nil || found.CreatedAt < c.CreatedAt) {
				found = &c
			}
		}
	}
	if found == nil {
		stateMu.Lock()
		for _, c := range state.Configurations {
			if c.key() == key {
				found = &c
			}
		}
		stateMu.Unlock()
	}
	if found == nil {
		return fmt.Errorf("no configuration %s", key)
	}
	go applyConfiguration(*found)
	return nil
}

// takeConfigSettingsLocked writes a configuration's default handlers and
// permissions over the settings'. Must be called with stateMu held.
func takeConfigSettingsLocked(c NappConfiguration) {
	if state.DefaultHandlers == nil {
		state.DefaultHandlers = make(map[string]DefaultHandler)
	}
	for _, h := range c.Handlers {
		state.DefaultHandlers[h.Action] = h
	}
	if c.Sign != "" {
		state.SignPolicy = c.Sign
	}
	if c.Publish != "" {
		state.PublishPolicy = c.Publish
	}
	if c.OpenLinks != "" {
		state.LinkPolicy = c.OpenLinks
	}
}

// putConfigBundle stores a configuration's bundle under its name, over a
// bundle of the same name, and pins it.
func putConfigBundle(b ConfigBundle) {
	stateMu.Lock()
	bundle := Bundle{Name: b.Name, Icon: b.Icon, Entries: b.Entries}
	if i := bundleIndexLocked(b.Name); i >= 0 {
		state.Bundles[i] = bundle
	} else {
		state.Bundles = append(state.Bundles, bundle)
	}
	if key := BundlePinKey(b.Name); !slices.Contains(state.Pinned, key) {
		state.Pinned = append(state.Pinned, key)
	}
	saveState()
	stateMu.Unlock()
}

// fetchNappRef gets the napp event a configuration points at.
func fetchNappRef(ref NappRef) (Napp, error) {
	if sys == nil {
		return Napp{}, errors.New("not ready yet")
	}
	var ptr nostr.Pointer
	var relays []string
	if ref.Relay != "" {
		relays = []string{ref.Relay}
	}
	what := ref.Addr
	if ref.Addr != "" {
		ep, err := nostr.ParseAddrString(ref.Addr)
		if err != nil {
			return Napp{}, err
		}
		ep.Relays = relays
		ptr = ep
	} else {
		what = ref.ID.Hex()
		ptr = nostr.EventPointer{ID: ref.ID, Relays: relays, Author: ref.Author}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	evt, _, err := sys.FetchSpecificEvent(ctx, ptr, sdk.FetchSpecificEventParameters{SaveToLocalStore: true})
	if err != nil || evt == nil {
		return Napp{}, fmt.Errorf("couldn't find the napp %s", what)
	}
	if !isNappKind(evt.Kind) {
		return Napp{}, fmt.Errorf("%s is not a napp", what)
	}
	if ref.Author != nostr.ZeroPK && evt.PubKey != ref.Author {
		return Napp{}, fmt.Errorf("the napp %s is not by the author the configuration names", what)
	}
	n := nappFromEvent(*evt)
	rememberNapp(n)
	return n, nil
}

// ─── default handlers ────────────────────────────────────────────

// defaultHandler is an action's default handler.
func defaultHandler(action string) (DefaultHandler, bool) {
	stateMu.Lock()
	defer stateMu.Unlock()
	h, ok := state.DefaultHandlers[action]
	return h, ok
}

// hasViewDefaults says whether any view:<kind> has a default handler, which
// is when a raw "view" is worth waiting on to learn its kind.
func hasViewDefaults() bool {
	stateMu.Lock()
	defer stateMu.Unlock()
	for action := range state.DefaultHandlers {
		if strings.HasPrefix(action, "view:") {
			return true
		}
	}
	return false
}

// DefaultHandlerInfo is a default handler as the settings show it.
type DefaultHandlerInfo struct {
	DefaultHandler
	TargetName string `json:"targetName"`
}

// defaultHandlers lists the default handlers by action.
func defaultHandlers() []DefaultHandlerInfo {
	stateMu.Lock()
	list := make([]DefaultHandlerInfo, 0, len(state.DefaultHandlers))
	for _, h := range state.DefaultHandlers {
		name := h.Target
		if n, ok := localNappLocked(h.Target); ok {
			name = n.Label()
		}
		list = append(list, DefaultHandlerInfo{DefaultHandler: h, TargetName: name})
	}
	stateMu.Unlock()
	slices.SortFunc(list, func(a, b DefaultHandlerInfo) int { return strings.Compare(a.Action, b.Action) })
	return list
}

// RemoveDefaultHandler makes an action ask the user again, like it did
// before a configuration gave it a default.
func RemoveDefaultHandler(action string) {
	stateMu.Lock()
	if _, ok := state.DefaultHandlers[action]; !ok {
		stateMu.Unlock()
		return
	}
	delete(state.DefaultHandlers, action)
	saveState()
	stateMu.Unlock()
	notifyState()
}

// handlerDefault is an action's default handler made into the option it is
// among these handlers: the first open window of the napp for a singleton
// that has one, the napp to launch otherwise.
type handlerDefault struct {
	mode   HandlerMode
	option PromptOption
}

// configuredHandler is the default handler of an action among these
// handlers, if it has one and it is among them.
func configuredHandler(action string, candidates []Napp, open []*Instance) (*handlerDefault, bool) {
	h, ok := defaultHandler(action)
	if !ok {
		return nil, false
	}
	if !slices.ContainsFunc(candidates, func(n Napp) bool { return n.ID == h.Target }) {
		log.Debug().Str("action", action).Str("target", h.Target).
			Msg("the default handler is not installed or doesn't take this action")
		return nil, false
	}
	var list []*Instance
	if h.Singleton {
		list = open
	}
	opt, ok := handlerOption(h.Target, candidates, list)
	if !ok {
		return nil, false
	}
	return &handlerDefault{mode: h.Mode, option: opt}, true
}

// ─── permissions ─────────────────────────────────────────────────

// NappPolicy is what every napp asking for perm gets when the user has not
// answered for that napp: only signing, publishing and opening links have
// one, and "ask" is no policy at all.
func NappPolicy(perm Permission) Decision {
	stateMu.Lock()
	defer stateMu.Unlock()
	var d Decision
	switch perm {
	case PermSign:
		d = state.SignPolicy
	case PermPublish:
		d = state.PublishPolicy
	case PermOpenLink:
		if state.LinkPolicy == LinksAll || state.LinkPolicy == LinksNonImage {
			d = DecisionAllow
		}
	}
	if d == "" {
		return DecisionAsk
	}
	return d
}

// SetNappPolicy sets what every napp asking to sign or to publish gets when
// the user has not answered for that napp.
func SetNappPolicy(perm Permission, d Decision) error {
	switch d {
	case DecisionAsk, DecisionAllow, DecisionDeny:
	default:
		return fmt.Errorf("unknown decision %q", d)
	}
	stateMu.Lock()
	switch perm {
	case PermSign:
		state.SignPolicy = d
	case PermPublish:
		state.PublishPolicy = d
	default:
		stateMu.Unlock()
		return fmt.Errorf("%s has no policy", perm)
	}
	saveState()
	stateMu.Unlock()
	log.Info().Str("permission", string(perm)).Str("decision", string(d)).Msg("napp policy changed")
	notifyState()
	return nil
}

// LinkPolicy is which links napps open in the browser without asking.
// Whatever they don't open that way, they ask for, except an image, which is
// shown over the napp with a button to open it.
type LinkPolicy string

const (
	LinksAll      LinkPolicy = "yes"       // every link, images too
	LinksNonImage LinkPolicy = "non-image" // the links that aren't images
	LinksNone     LinkPolicy = "no"        // none
)

// OpenLinks is which links napps open in the browser without asking.
func OpenLinks() LinkPolicy {
	stateMu.Lock()
	defer stateMu.Unlock()
	if state.LinkPolicy == "" {
		return LinksNone
	}
	return state.LinkPolicy
}

// SetOpenLinks sets which links napps open in the browser without asking,
// for the napps the user has not answered for.
func SetOpenLinks(p LinkPolicy) error {
	switch p {
	case LinksAll, LinksNonImage, LinksNone:
	default:
		return fmt.Errorf("unknown link policy %q", p)
	}
	stateMu.Lock()
	changed := state.LinkPolicy != p
	state.LinkPolicy = p
	if changed {
		saveState()
	}
	stateMu.Unlock()
	if changed {
		log.Info().Str("links", string(p)).Msg("opening links without asking changed")
		notifyState()
	}
	return nil
}
