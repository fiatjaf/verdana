package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/sdk"
)

// What an event says, in words a person can judge it by, for the sign and
// publish prompts: the parts of the kinds napps commonly sign that are worth
// reading before agreeing. The title says what the event is ("wants to sign
// a note"), the detail goes on from it ("Notifying alice and bob."), a
// warning says what could hurt, and the whole event stays folded under all
// that, as the signer gets it. Given the event it replaces (a follow list, a
// profile) it says what changes.

// eventKindNames is what an event kind is, in a few words. Only the kinds
// napps commonly sign; the rest go by number alone.
var eventKindNames = map[nostr.Kind]string{
	0:     "profile",
	1:     "note",
	3:     "follow list",
	4:     "encrypted message",
	5:     "deletion request",
	6:     "repost",
	7:     "reaction",
	9:     "chat message",
	13:    "seal",
	14:    "direct message",
	16:    "repost",
	20:    "picture",
	21:    "video",
	22:    "short video",
	1059:  "gift wrap",
	1063:  "file metadata",
	1111:  "comment",
	1984:  "report",
	9734:  "zap request",
	9802:  "highlight",
	10000: "mute list",
	10001: "pin list",
	10002: "relay list",
	10003: "bookmark list",
	10050: "message relay list",
	10063: "blossom server list",
	22242: "relay auth",
	24133: "nostr connect",
	24242: "blossom auth",
	27235: "http auth",
	30000: "follow set",
	30003: "bookmark set",
	30023: "article",
	30024: "article draft",
	30078: "app data record",
	31922: "calendar event",
	31923: "calendar event",
	35128: "nsite",
}

// aKindTagged is aKind for the kind a tag names ("k", "K"), or fallback
// when the tag has none.
func aKindTagged(evt *nostr.Event, tag, fallback string) string {
	if k, err := strconv.ParseUint(tagged(evt, tag), 10, 16); err == nil {
		return aKind(nostr.Kind(k), "post")
	}
	return fallback
}

// aKind is "a note", "an http auth"; unknown for a kind with no name.
func aKind(kind nostr.Kind, unknown string) string {
	name := eventKindNames[kind]
	if name == "" {
		name = unknown
	}
	if strings.ContainsAny(name[:1], "aeiou") || strings.HasPrefix(name, "http ") {
		return "an " + name
	}
	return "a " + name
}

// eventDescription is what a sign or publish prompt shows about an event.
type eventDescription struct {
	// Sentence goes on from "wants to sign a note", with no closing stop:
	// "notifying alice". Empty when there is nothing worth saying.
	Sentence string
	// Warning is what could hurt: a follow list about to lose most of itself.
	Warning string
	// Summary and Code are the fold: what the event is in one line, and the
	// event itself as JSON.
	Summary string
	Code    string
}

// people named in a prompt before it says how many more there are
const peopleMax = 5

// how long a prompt waits for names before going with what it has
const namesWait = 1500 * time.Millisecond

// describeEvent says what evt is for a prompt. It looks up the event it
// replaces in the local store and the names of the people it mentions, for
// no longer than namesWait.
func describeEvent(evt nostr.Event) eventDescription {
	d := describer{deadline: time.Now().Add(namesWait)}
	prev := previousVersion(evt)
	sentence, warning := d.facts(&evt, prev)
	return eventDescription{
		Sentence: sentence,
		Warning:  warning,
		Summary:  eventSummary(evt),
		Code:     eventJSON(evt),
	}
}

// body is the prompt for d: its sentence, ended by end (a stop, or where
// it goes: ", to 3 relays:"), what that points at, the warning, the fold.
func (d eventDescription) body(end string, items []string) PromptBody {
	detail := strings.TrimPrefix(d.Sentence+end, ", ")
	if detail == "." {
		detail = ""
	}
	if detail != "" {
		r := []rune(detail)
		detail = strings.ToUpper(string(r[0])) + string(r[1:])
	}
	return PromptBody{
		Detail:  detail,
		Warning: d.Warning,
		Items:   items,
		Code:    d.Code,
		Summary: d.Summary,
	}
}

// previousVersion is the version of a replaceable or addressable event the
// store has now, which evt would replace; nil for any other kind.
func previousVersion(evt nostr.Event) *nostr.Event {
	if sys == nil || evt.PubKey == (nostr.PubKey{}) {
		return nil
	}
	f := nostr.Filter{Kinds: []nostr.Kind{evt.Kind}, Authors: []nostr.PubKey{evt.PubKey}}
	switch {
	case evt.Kind.IsReplaceable():
	case evt.Kind.IsAddressable():
		f.Tags = nostr.TagMap{"d": []string{evt.Tags.GetD()}}
	default:
		return nil
	}
	for prev := range sys.Store.QueryEvents(f, 1) {
		if prev.ID == evt.ID {
			// already stored (published before): nothing to compare it to
			return nil
		}
		return &prev
	}
	return nil
}

// eventSummary is the fold's line: "kind-1 · content 120 · 3 tags".
func eventSummary(evt nostr.Event) string {
	parts := []string{fmt.Sprintf("kind-%d", evt.Kind)}
	if n := len([]rune(evt.Content)); n > 0 {
		parts = append(parts, fmt.Sprintf("content %d", n))
	}
	parts = append(parts, plural(len(evt.Tags), "tag", "tags"))
	return strings.Join(parts, " · ")
}

// past this a dialog would stall; the summary still gives the full length
const eventJSONMax = 20_000

// eventJSON is the event as the signer gets it: an unsigned one has no id,
// pubkey or signature to show yet.
func eventJSON(evt nostr.Event) string {
	shown := struct {
		ID        string     `json:"id,omitempty"`
		PubKey    string     `json:"pubkey,omitempty"`
		CreatedAt int64      `json:"created_at"`
		Kind      int        `json:"kind"`
		Tags      nostr.Tags `json:"tags"`
		Content   string     `json:"content"`
		Sig       string     `json:"sig,omitempty"`
	}{
		CreatedAt: int64(evt.CreatedAt),
		Kind:      int(evt.Kind),
		Tags:      evt.Tags,
		Content:   evt.Content,
	}
	if shown.Tags == nil {
		shown.Tags = nostr.Tags{}
	}
	if evt.ID != (nostr.ID{}) {
		shown.ID = evt.ID.Hex()
	}
	if evt.PubKey != (nostr.PubKey{}) {
		shown.PubKey = evt.PubKey.Hex()
	}
	if evt.Sig != ([64]byte{}) {
		shown.Sig = fmt.Sprintf("%x", evt.Sig[:])
	}
	b, err := json.MarshalIndent(shown, "", "  ")
	if err != nil {
		return "unreadable event"
	}
	s := string(b)
	if len(s) > eventJSONMax {
		s = s[:eventJSONMax] + "…"
	}
	return s
}

// ─── the words ───────────────────────────────────────────────────

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

// and is "a", "a and b", "a, b and c".
func and(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

// tagValues is the value of every tag named name; evt may be nil.
func tagValues(evt *nostr.Event, name string) []string {
	if evt == nil {
		return nil
	}
	var out []string
	for tag := range evt.Tags.FindAll(name) {
		out = append(out, tag[1])
	}
	return out
}

func tagged(evt *nostr.Event, name string) string {
	if tag := evt.Tags.Find(name); tag != nil {
		return tag[1]
	}
	return ""
}

func isHex64(s string) bool {
	_, err := nostr.PubKeyFromHex(s)
	return err == nil
}

func hexKeys(values []string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if isHex64(v) && !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

// urlHost is a relay or server url without its scheme or trailing slash.
func urlHost(u string) string {
	for _, scheme := range []string{"wss://", "ws://", "https://", "http://"} {
		u = strings.TrimPrefix(u, scheme)
	}
	return strings.TrimSuffix(u, "/")
}

// looks like nip04 ("…?iv=…") or nip44 (one long base64 run) ciphertext
var ciphertextRe = regexp.MustCompile(`^[A-Za-z0-9+/=]+(\?iv=[A-Za-z0-9+/=]+)?$`)

func encrypted(text string) bool {
	return len(text) >= 40 && ciphertextRe.MatchString(text)
}

// ─── names ───────────────────────────────────────────────────────

type describer struct {
	deadline time.Time
}

// names are the names of the people behind these pubkeys, looked up all at
// once and waited on until the deadline; whoever isn't found by then is
// shown by what the cache has, or a short npub.
func (d describer) names(hexes []string) []string {
	out := make([]string, len(hexes))
	type found struct {
		i    int
		name string
	}
	ch := make(chan found, len(hexes))
	pending := 0
	for i, h := range hexes {
		pk, _ := nostr.PubKeyFromHex(h)
		out[i] = sdk.ProfileMetadata{PubKey: pk}.ShortName()
		if sys == nil {
			continue
		}
		if pm, ok := sys.MetadataCache.Get(pk); ok {
			out[i] = pm.ShortName()
			continue
		}
		pending++
		go func() {
			// its own deadline, longer than the prompt's: whatever comes in
			// late is still cached for the next time
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			ch <- found{i, sys.FetchProfileMetadata(ctx, pk).ShortName()}
		}()
	}
	timer := time.NewTimer(time.Until(d.deadline))
	defer timer.Stop()
	for ; pending > 0; pending-- {
		select {
		case f := <-ch:
			out[f.i] = f.name
		case <-timer.C:
			return out
		}
	}
	return out
}

func (d describer) name(hex string) string {
	return d.names([]string{hex})[0]
}

// people is who these are by name, peopleMax at most, then how many more.
func (d describer) people(pubkeys []string) string {
	keys := hexKeys(pubkeys)
	shown := keys
	if len(shown) > peopleMax {
		shown = shown[:peopleMax]
	}
	named := d.names(shown)
	if len(keys) > peopleMax {
		named = append(named, fmt.Sprintf("%d more", len(keys)-peopleMax))
	}
	return and(named)
}

// ─── what each kind says ─────────────────────────────────────────

// facts is the sentence to go on from "wants to sign <kind>" and the warning
// under it, for the kinds we can read; both empty for the rest.
func (d describer) facts(evt, prev *nostr.Event) (sentence, warning string) {
	switch evt.Kind {
	case 0:
		return d.profile(evt, prev), ""

	case 1:
		// a note: who it notifies
		if p := hexKeys(tagValues(evt, "p")); len(p) > 0 {
			return "notifying " + d.people(p), ""
		}

	case 3:
		return d.follows(evt, prev)

	case 5:
		// deletion request: how many events, of which kinds, why
		var kinds []string
		for _, k := range tagValues(evt, "k") {
			name := "kind " + k
			if kind, err := strconv.ParseUint(k, 10, 16); err == nil && eventKindNames[nostr.Kind(kind)] != "" {
				name = eventKindNames[nostr.Kind(kind)]
			}
			if !slices.Contains(kinds, name) {
				kinds = append(kinds, name)
			}
		}
		n := len(tagValues(evt, "e")) + len(tagValues(evt, "a"))
		s := "for " + plural(n, "event", "events")
		if len(kinds) > 0 {
			s += " (" + strings.Join(kinds, ", ") + ")"
		}
		if evt.Content != "" {
			s += ", saying “" + evt.Content + "”"
		}
		return s, ""

	case 6, 16:
		// repost: of what, by whom
		fallback := "a note"
		if evt.Kind == 16 {
			fallback = "a post"
		}
		return "of " + aKindTagged(evt, "k", fallback) + d.by(evt, "p"), ""

	case 7:
		// reaction: which, on what, by whom
		on := aKindTagged(evt, "k", "a note") + d.by(evt, "p")
		switch evt.Content {
		case "+", "":
			return "liking " + on, ""
		case "-":
			return "disliking " + on, ""
		}
		return "with " + strings.Trim(evt.Content, ":") + " on " + on, ""

	case 1059:
		// gift wrap: who can open it
		return "for " + d.people(tagValues(evt, "p")), ""

	case 1111:
		// comment (NIP-22): on what, who it notifies
		root := ""
		if r := hexKeys(tagValues(evt, "P")); len(r) > 0 {
			root = r[0]
		}
		s := "on "
		if ext := tagged(evt, "I"); ext != "" {
			s += ext
		} else {
			s += aKindTagged(evt, "K", "a post")
			if root != "" {
				s += " by " + d.name(root)
			}
		}
		var notify []string
		for _, p := range tagValues(evt, "p") {
			if p != root {
				notify = append(notify, p)
			}
		}
		if len(hexKeys(notify)) > 0 {
			s += ", notifying " + d.people(notify)
		}
		return s, ""

	case 9734:
		// zap request: how much, to whom, for what
		msats, _ := strconv.ParseInt(tagged(evt, "amount"), 10, 64)
		to := d.by(evt, "p")
		if msats == 0 && to == "" {
			return "", ""
		}
		s := ""
		if msats > 0 {
			s = "for " + plural(int((msats+500)/1000), "sat", "sats")
		}
		if to != "" {
			s = strings.TrimSpace(s + " to" + strings.TrimPrefix(to, " by"))
		}
		if tagged(evt, "e") != "" {
			s += ", on a note"
		}
		return s, ""

	case 10000, 10001, 10003:
		return "with " + listItems(evt, prev), ""

	case 10002:
		// relay list (NIP-65): each relay, marked when it is only read or write
		return "with " + urlList(evt, prev, "r", func(t nostr.Tag) string {
			if len(t) > 2 && (t[2] == "read" || t[2] == "write") {
				return t[2]
			}
			return ""
		}), ""

	case 10050:
		return "with " + urlList(evt, prev, "relay", nil), ""

	case 10063:
		return "with " + urlList(evt, prev, "server", nil), ""

	case 22242:
		// relay auth (NIP-42): which relay it logs you in to
		relay := tagged(evt, "relay")
		if relay == "" {
			relay = "?"
		}
		return "for " + urlHost(relay), ""

	case 24242:
		// blossom auth (BUD-11): what it allows, on how many blobs, where
		verb := tagged(evt, "t")
		if verb == "" {
			verb = "?"
		}
		blobs := "blobs"
		if n := len(tagValues(evt, "x")); n > 0 {
			blobs = plural(n, "blob", "blobs")
		}
		prep := " on "
		if verb == "upload" || verb == "media" {
			prep = " to "
		}
		servers := []string{"any server"}
		if s := tagValues(evt, "server"); len(s) > 0 {
			servers = nil
			for _, u := range s {
				servers = append(servers, urlHost(u))
			}
		}
		return verb + " " + blobs + prep + and(servers), ""

	case 27235:
		// http auth (NIP-98): the request it signs for
		method := tagged(evt, "method")
		if method == "" {
			method = "a request to"
		}
		u := tagged(evt, "u")
		if u == "" {
			u = "?"
		}
		return method + " " + u, ""

	case 30000, 30003:
		// sets: which one, then its items
		name := tagged(evt, "title")
		if name != "" {
			name = "“" + name + "”"
		} else if name = evt.Tags.GetD(); name == "" {
			name = "?"
		}
		return name + " with " + listItems(evt, prev), ""

	case 30023:
		if title := tagged(evt, "title"); title != "" {
			return "“" + title + "”", ""
		}

	case 30078:
		// app data (NIP-78): the key it's stored under, and whether it's readable
		key := evt.Tags.GetD()
		if key == "" {
			key = "?"
		}
		s := "under " + key
		if encrypted(evt.Content) {
			s += ", encrypted"
		}
		return s, ""
	}
	return "", ""
}

// by is " by <name>" for the first pubkey under tag, or nothing.
func (d describer) by(evt *nostr.Event, tag string) string {
	if p := hexKeys(tagValues(evt, tag)); len(p) > 0 {
		return " by " + d.name(p[0])
	}
	return ""
}

// follows is a follow list against the current one: the count before and
// after, who is followed and unfollowed; losing more than a quarter of it is
// a warning.
func (d describer) follows(evt, prev *nostr.Event) (string, string) {
	now := hexKeys(tagValues(evt, "p"))
	// without the current one in hand there is no diff to claim: just its size
	if prev == nil {
		return "of " + plural(len(now), "profile", "profiles"), ""
	}
	was := hexKeys(tagValues(prev, "p"))
	var added, removed []string
	for _, p := range now {
		if !slices.Contains(was, p) {
			added = append(added, p)
		}
	}
	for _, p := range was {
		if !slices.Contains(now, p) {
			removed = append(removed, p)
		}
	}
	s := fmt.Sprintf("of %s (was %d)", plural(len(now), "profile", "profiles"), len(was))
	if len(added) > 0 {
		s += ", following " + d.people(added)
	}
	if len(removed) > 0 {
		s += ", unfollowing " + d.people(removed)
	}
	warning := ""
	if float64(len(removed)) > float64(len(was))/4 {
		warning = fmt.Sprintf("This drops %d of the %d profiles you follow.", len(removed), len(was))
	}
	return s, warning
}

// profile fields in the order a person reads them; the rest after
var profileOrder = []string{"name", "display_name", "about", "picture", "banner", "website", "nip05", "lud16"}

func profileFields(evt *nostr.Event) map[string]string {
	out := map[string]string{}
	if evt == nil {
		return out
	}
	var o map[string]any
	if json.Unmarshal([]byte(evt.Content), &o) != nil {
		return out
	}
	for k, v := range o {
		switch v := v.(type) {
		case nil:
		case string:
			if v != "" {
				out[k] = v
			}
		default:
			// structured values keep their shape in the comparison
			b, _ := json.Marshal(v)
			out[k] = string(b)
		}
	}
	return out
}

func fieldNames(keys []string) string {
	rank := func(k string) int {
		if i := slices.Index(profileOrder, k); i >= 0 {
			return i
		}
		return len(profileOrder)
	}
	slices.SortFunc(keys, func(a, b string) int {
		if r := rank(a) - rank(b); r != 0 {
			return r
		}
		return strings.Compare(a, b)
	})
	names := make([]string, len(keys))
	for i, k := range keys {
		names[i] = strings.ReplaceAll(k, "_", " ")
	}
	return and(names)
}

// profile is, with the current one, the fields that change and go; without,
// the fields it has.
func (d describer) profile(evt, prev *nostr.Event) string {
	now := profileFields(evt)
	if prev == nil {
		keys := make([]string, 0, len(now))
		for k := range now {
			keys = append(keys, k)
		}
		if len(keys) == 0 {
			return "with no fields"
		}
		return "with " + fieldNames(keys)
	}
	was := profileFields(prev)
	var changed, removed []string
	for k, v := range now {
		if was[k] != v {
			changed = append(changed, k)
		}
	}
	for k := range was {
		if _, ok := now[k]; !ok {
			removed = append(removed, k)
		}
	}
	var parts []string
	if len(changed) > 0 {
		parts = append(parts, "changing "+fieldNames(changed))
	}
	if len(removed) > 0 {
		parts = append(parts, "removing "+fieldNames(removed))
	}
	if len(parts) == 0 {
		return "with no changes"
	}
	return strings.Join(parts, ", ")
}

// listItemNames are the public items of a list by what they are; private
// ones are encrypted in the content.
var listItemNames = []struct{ tag, one, many string }{
	{"p", "person", "people"},
	{"e", "note", "notes"},
	{"a", "item", "items"},
	{"t", "hashtag", "hashtags"},
	{"word", "word", "words"},
	{"r", "link", "links"},
	{"relay", "relay", "relays"},
	{"emoji", "emoji", "emoji"},
	{"group", "group", "groups"},
}

// listItems is how many of each item a list has, and against the list it
// replaces how many come and go.
func listItems(evt, prev *nostr.Event) string {
	var parts []string
	for _, item := range listItemNames {
		now := uniq(tagValues(evt, item.tag))
		was := uniq(tagValues(prev, item.tag))
		if len(now) == 0 && len(was) == 0 {
			continue
		}
		added, removed := 0, 0
		for _, v := range now {
			if !slices.Contains(was, v) {
				added++
			}
		}
		for _, v := range was {
			if !slices.Contains(now, v) {
				removed++
			}
		}
		part := plural(len(now), item.one, item.many)
		if prev != nil && (added > 0 || removed > 0) {
			var change []string
			if added > 0 {
				change = append(change, fmt.Sprintf("+%d", added))
			}
			if removed > 0 {
				change = append(change, fmt.Sprintf("−%d", removed))
			}
			part += " (" + strings.Join(change, ", ") + ")"
		}
		parts = append(parts, part)
	}
	if evt.Content != "" {
		parts = append(parts, "private items")
	}
	if len(parts) == 0 {
		return "nothing"
	}
	return and(parts)
}

func uniq(values []string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

// urlList is a list of urls under one tag (relays, servers), each with what
// the tag marks it for; against the list it replaces, what is new and what
// goes.
func urlList(evt, prev *nostr.Event, tag string, marks func(nostr.Tag) string) string {
	tags := func(e *nostr.Event) []nostr.Tag {
		if e == nil {
			return nil
		}
		return slices.Collect(e.Tags.FindAll(tag))
	}
	var was, kept []string
	for _, t := range tags(prev) {
		was = append(was, urlHost(t[1]))
	}
	var items []string
	for _, t := range tags(evt) {
		h := urlHost(t[1])
		kept = append(kept, h)
		var notes []string
		if marks != nil {
			if m := marks(t); m != "" {
				notes = append(notes, m)
			}
		}
		if prev != nil && !slices.Contains(was, h) {
			notes = append(notes, "new")
		}
		if len(notes) > 0 {
			h += " (" + strings.Join(notes, ", ") + ")"
		}
		items = append(items, h)
	}
	var gone []string
	for _, h := range uniq(was) {
		if !slices.Contains(kept, h) {
			gone = append(gone, h)
		}
	}
	s := "no entries"
	if len(items) > 0 {
		s = and(items)
	}
	if len(gone) > 0 {
		s += ", dropping " + and(gone)
	}
	return s
}
