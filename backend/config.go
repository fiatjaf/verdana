package backend

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
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
//
// Each "napp …" item of a bundle tag is one napp of the bundle with the
// actions it gets, each action followed by its payload as JSON (null for
// none). An item can also carry more napps, each starting at another "napp".
//
// "sign" and "publish" answer every napp's signing and publishing for which
// the user has not answered themselves (see configuredPermission), and
// "auth-on-reads" sets whether the launcher authenticates to relays when
// reading, once, when the configuration is applied.

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
	// empty when the configuration says nothing about it. ReadAuth is nil
	// when it says nothing about authenticating on reads.
	Sign     Decision `json:"sign,omitempty"`
	Publish  Decision `json:"publish,omitempty"`
	ReadAuth *bool    `json:"readAuth,omitempty"`
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

// offerConfiguration asks the user whether to apply a configuration and, if
// they agree, applies it. It returns right away.
func offerConfiguration(evt nostr.Event) {
	c := configFromEvent(evt)
	go func() {
		var lines []string
		if len(c.Napps) > 0 {
			lines = append(lines, "install "+plural(len(c.Napps), "napp", "napps"))
		}
		for _, h := range c.Handlers {
			lines = append(lines, "send “"+h.Action+"” to "+h.Target+" ("+string(h.Mode)+")")
		}
		for _, b := range c.Bundles {
			lines = append(lines, "add the bundle “"+b.Name+"” to the home tab")
		}
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
		if len(lines) == 0 {
			SetFetchErr(c.Label() + ": this configuration has nothing in it")
			return
		}
		p := newPrompt("launcher", "Apply the configuration “"+c.Label()+"”?", PromptBody{
			Detail: "It will " + strings.Join(lines, "; ") + ".",
		}, nil)
		enqueuePrompt(p)
		if !p.wait().OK {
			return
		}
		applyConfiguration(c)
	}()
}

// applyConfiguration stores a configuration — replacing an older one from
// the same author and d-tag — and then installs its napps and puts its
// bundles on the home tab. Blocking.
func applyConfiguration(c NappConfiguration) {
	stateMu.Lock()
	state.Configurations = slices.DeleteFunc(state.Configurations, func(o NappConfiguration) bool {
		return o.key() == c.key()
	})
	// the newest one applied comes last, and has the last word
	state.Configurations = append(state.Configurations, c)
	saveState()
	stateMu.Unlock()
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

// defaultHandler is the default handler the applied configurations give an
// action: the one applied last has the last word.
func defaultHandler(action string) (DefaultHandler, bool) {
	stateMu.Lock()
	defer stateMu.Unlock()
	for _, c := range slices.Backward(state.Configurations) {
		for _, h := range c.Handlers {
			if h.Action == action {
				return h, true
			}
		}
	}
	return DefaultHandler{}, false
}

// hasViewDefaults says whether any view:<kind> has a default handler, which
// is when a raw "view" is worth waiting on to learn its kind.
func hasViewDefaults() bool {
	stateMu.Lock()
	defer stateMu.Unlock()
	for _, c := range state.Configurations {
		for _, h := range c.Handlers {
			if strings.HasPrefix(h.Action, "view:") {
				return true
			}
		}
	}
	return false
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

// configuredPermission is what the applied configurations say about every
// napp's signing or publishing: the one applied last that says anything has
// the last word, and an "ask" there means asking even when an older one
// allowed or denied it.
func configuredPermission(perm Permission) (Decision, bool) {
	stateMu.Lock()
	defer stateMu.Unlock()
	for _, c := range slices.Backward(state.Configurations) {
		var d Decision
		switch perm {
		case PermSign:
			d = c.Sign
		case PermPublish:
			d = c.Publish
		default:
			return "", false
		}
		if d != "" {
			return d, true
		}
	}
	return "", false
}
