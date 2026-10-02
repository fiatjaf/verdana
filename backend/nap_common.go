package backend

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip19"
	"fiatjaf.com/nostr/sdk"
	"github.com/btcsuite/btcd/btcutil/bech32"
)

// NAP-COMMON: the everyday social writes (follow, react, report), two reads
// and nip19 encoding/decoding, done by the launcher so a napplet doesn't have to hand-build kind 3,
// 7 and 1984 events. Every write still goes through napSignAndPublish: one
// prompt, the user's key, the user's relays.
//
// The shim answers every call by "ok": false is a resolved result, not a
// rejection, so errors are reported as {ok:false, error:<code>}.

func init() {
	handleNap(map[string]napHandler{
		"common.encodeNip19": napCommonEncodeNip19,
		"common.decodeNip19": napCommonDecodeNip19,
		"common.getProfile":  napCommonGetProfile,
		"common.follows":     napCommonFollows,
		"common.follow":      napCommonFollow(true),
		"common.unfollow":    napCommonFollow(false),
		"common.react":       napCommonReact,
		"common.report":      napCommonReport,
	})
}

func (c *napCall) commonFail(code string) {
	c.reply(map[string]any{"ok": false, "error": code})
}

func napCommonGetProfile(c *napCall) {
	var r struct {
		Target string `json:"target"`
	}
	_ = c.decode(&r)
	pk, hints, ok := profileTarget(r.Target)
	if !ok {
		c.commonFail("invalid-profile-target")
		return
	}
	if sys == nil {
		c.commonFail("relay-timeout")
		return
	}
	c.async(func(ctx context.Context) {
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		pm := sys.FetchProfileMetadata(ctx, pk)
		if pm.Event == nil && len(hints) > 0 {
			// the nprofile's relays are only hints: asked last, and only
			// public ones (a napplet can name any relay it likes)
			if evt := fetchProfileFromHints(ctx, pk, hints); evt != nil {
				pm, _ = sdk.ParseMetadata(*evt)
			}
		}
		out := map[string]any{"ok": true, "pubkey": pk.Hex(), "profile": nil}
		if pm.Event != nil {
			out["profile"] = commonProfileData(pm)
			out["result"] = relayEventResult(*pm.Event)
		}
		c.reply(out)
	})
}

// profileTarget reads getProfile's target (hex, npub or nprofile), with the
// nprofile's relays as hints.
func profileTarget(s string) (nostr.PubKey, []string, bool) {
	s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "nostr:"))
	if strings.HasPrefix(s, "nprofile1") {
		if _, data, err := nip19.Decode(s); err == nil {
			if pp, ok := data.(nostr.ProfilePointer); ok {
				return pp.PublicKey, pp.Relays, true
			}
		}
		return nostr.ZeroPK, nil, false
	}
	pk, ok := npubOrHex(s)
	return pk, nil, ok
}

func fetchProfileFromHints(ctx context.Context, pk nostr.PubKey, hints []string) *nostr.Event {
	urls := make([]string, 0, 3)
	for _, h := range hints {
		if len(urls) == 3 {
			break
		}
		if u, err := napExplicitRelay(ctx, h); err == nil {
			urls = nostr.AppendUnique(urls, u)
		}
	}
	if len(urls) == 0 {
		return nil
	}
	var best *nostr.Event
	filter := nostr.Filter{Kinds: []nostr.Kind{0}, Authors: []nostr.PubKey{pk}, Limit: 1}
	for re := range sys.Pool.FetchMany(ctx, urls, filter, nostr.SubscriptionOptions{Label: "verdana-nap-profile-hint"}) {
		if re.Event.PubKey == pk && (best == nil || re.Event.CreatedAt > best.CreatedAt) {
			e := re.Event
			best = &e
		}
	}
	return best
}

// commonProfileData is profileData plus whatever else the kind 0 carries:
// CommonProfileData keeps additional fields.
func commonProfileData(pm sdkProfileMetadata) map[string]any {
	out := profileData(pm)
	var raw map[string]any
	if json.Unmarshal([]byte(pm.Event.Content), &raw) != nil {
		return out
	}
	for k, v := range raw {
		switch k {
		case "name", "display_name", "displayName", "about", "picture", "banner", "nip05", "lud16", "website":
			continue
		}
		if v != nil {
			out[k] = v
		}
	}
	return out
}

func napCommonFollows(c *napCall) {
	pk, ok := currentUser()
	if !ok {
		c.commonFail("not-signed-in")
		return
	}
	c.async(func(ctx context.Context) {
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		pubkeys, err := identityFollows(ctx, pk)
		if err != nil {
			// "follows nobody" and "couldn't find out" must not look alike
			c.commonFail("relay-timeout")
			return
		}
		c.reply(map[string]any{"ok": true, "pubkeys": pubkeys})
	})
}

// napCommonFollow edits the user's kind 3 follow list. It starts from the
// newest list the launcher can find, so other clients' entries (and the
// list's content, which some clients still use for relays) survive. A kind 3
// replaces the old one outright, so not finding the list in time is an
// error, never an empty list to start from.
func napCommonFollow(follow bool) napHandler {
	return func(c *napCall) {
		var r struct {
			Pubkeys []string `json:"pubkeys"`
		}
		_ = c.decode(&r)
		user, ok := currentUser()
		if !ok {
			c.commonFail("not-signed-in")
			return
		}
		targets := make([]nostr.PubKey, 0, len(r.Pubkeys))
		for _, p := range r.Pubkeys {
			pk, ok := npubOrHex(p)
			if !ok {
				c.commonFail("invalid-pubkey")
				return
			}
			targets = append(targets, pk)
		}
		if len(targets) == 0 {
			c.commonFail("invalid-pubkey")
			return
		}
		if sys == nil {
			c.commonFail("relay-timeout")
			return
		}

		c.async(func(ctx context.Context) {
			fctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			var current *nostr.Event
			if fl := sys.FetchFollowList(fctx, user); fl.Event != nil {
				e := *fl.Event
				current = &e
			}
			if latest := fetchReplaceable(fctx, 3, user); latest != nil &&
				(current == nil || latest.CreatedAt > current.CreatedAt) {
				current = latest
			}
			timedOut := fctx.Err() != nil
			cancel()
			if current == nil && timedOut {
				c.commonFail("relay-timeout")
				return
			}

			t := napTemplate{Kind: 3, Tags: nostr.Tags{}}
			if current != nil {
				t.Content = current.Content
				t.Tags = current.Tags
			}
			tags, changed := mergeFollowTags(t.Tags, targets, follow)
			if !changed {
				// already the case: nothing to sign or publish
				out := map[string]any{"ok": true}
				if current != nil {
					out["eventId"], out["event"] = current.ID.Hex(), *current
				}
				c.reply(out)
				return
			}
			t.Tags = tags

			verb := "unfollow"
			if follow {
				verb = "follow"
			}
			npubs := make([]string, len(targets))
			for i, pk := range targets {
				npubs[i] = nip19.EncodeNpub(pk)
			}
			who := "1 person"
			if len(targets) > 1 {
				who = fmt.Sprintf("%d people", len(targets))
			}
			detail := fmt.Sprintf("Your follow list will have %d entries", countFollows(tags))
			if current == nil {
				detail = "No follow list of yours was found, so this starts a new one"
			}
			t.ask = &napAsk{
				Title:  verb + " " + who,
				Detail: detail,
				Code:   preview(strings.Join(npubs, " "), 400),
			}
			napCommonPublish(ctx, c, t)
		})
	}
}

// mergeFollowTags adds (follow) or removes the targets' p tags, leaving
// every other tag as it was. changed reports whether anything did.
func mergeFollowTags(current nostr.Tags, targets []nostr.PubKey, follow bool) (nostr.Tags, bool) {
	tags := slices.Clone(current)
	if tags == nil {
		tags = nostr.Tags{}
	}
	changed := false
	for _, pk := range targets {
		hex := pk.Hex()
		isTarget := func(tag nostr.Tag) bool { return len(tag) >= 2 && tag[0] == "p" && tag[1] == hex }
		has := slices.ContainsFunc(tags, isTarget)
		switch {
		case follow && !has:
			tags = append(tags, nostr.Tag{"p", hex})
			changed = true
		case !follow && has:
			tags = slices.DeleteFunc(tags, isTarget)
			changed = true
		}
	}
	return tags, changed
}

func countFollows(tags nostr.Tags) int {
	n := 0
	for _, tag := range tags {
		if len(tag) >= 2 && tag[0] == "p" {
			n++
		}
	}
	return n
}

func napCommonReact(c *napCall) {
	var r struct {
		TargetEventID   string `json:"targetEventId"`
		Reaction        string `json:"reaction"`
		CustomEmojiHref string `json:"customEmojiHref"`
	}
	_ = c.decode(&r)
	if _, ok := currentUser(); !ok {
		c.commonFail("not-signed-in")
		return
	}
	id, err := nostr.IDFromHex(strings.TrimSpace(r.TargetEventID))
	if err != nil {
		c.commonFail("invalid-target")
		return
	}
	if code := validateReaction(r.Reaction, r.CustomEmojiHref); code != "" {
		c.commonFail(code)
		return
	}
	c.async(func(ctx context.Context) {
		fctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		target := loadEvent(fctx, json.RawMessage(`"`+id.Hex()+`"`), nil, "")
		cancel()
		if target == nil {
			c.commonFail("author-unresolved")
			return
		}
		t, code := reactionTemplate(*target, r.Reaction, r.CustomEmojiHref)
		if code != "" {
			c.commonFail(code)
			return
		}
		t.ask = &napAsk{
			Title:  "react " + preview(r.Reaction, 40) + " to a post",
			Detail: "By " + nip19.EncodeNpub(target.PubKey),
			Code:   preview(target.Content, 200),
		}
		napCommonPublish(ctx, c, t)
	})
}

var shortcodeRe = regexp.MustCompile(`^:([A-Za-z0-9_-]+):$`)

// validateReaction checks a NIP-25 reaction: "+", "-", an emoji or a NIP-30
// :shortcode:, which needs its image url (and only a shortcode may have one).
func validateReaction(reaction, href string) string {
	if reaction == "" || len(reaction) > 64 ||
		strings.IndexFunc(reaction, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return "invalid-reaction"
	}
	if shortcodeRe.MatchString(reaction) != (href != "") {
		return "invalid-reaction"
	}
	if href != "" {
		u, err := url.Parse(href)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return "invalid-reaction"
		}
	}
	return ""
}

// reactionTemplate is the kind 7 for reaction to target (NIP-25), with the
// emoji tag a custom reaction needs (NIP-30).
func reactionTemplate(target nostr.Event, reaction, href string) (napTemplate, string) {
	if code := validateReaction(reaction, href); code != "" {
		return napTemplate{}, code
	}
	t := napTemplate{Kind: 7, Content: reaction, Tags: nostr.Tags{
		{"e", target.ID.Hex()},
		{"p", target.PubKey.Hex()},
	}}
	switch {
	case target.Kind.IsAddressable():
		t.Tags = append(t.Tags, nostr.Tag{"a", fmt.Sprintf("%d:%s:%s", target.Kind, target.PubKey.Hex(), target.Tags.GetD())})
	case target.Kind.IsReplaceable():
		t.Tags = append(t.Tags, nostr.Tag{"a", fmt.Sprintf("%d:%s:", target.Kind, target.PubKey.Hex())})
	}
	t.Tags = append(t.Tags, nostr.Tag{"k", strconv.Itoa(int(target.Kind))})
	if m := shortcodeRe.FindStringSubmatch(reaction); m != nil {
		t.Tags = append(t.Tags, nostr.Tag{"emoji", m[1], href})
	}
	return t, ""
}

var reportReasons = []string{"nudity", "malware", "profanity", "illegal", "spam", "impersonation", "other"}

// reportTarget is a CommonReportTarget with its keys as hex.
type reportTarget struct {
	Type   string // "event" or "pubkey"
	ID     string // the event, for "event"
	Pubkey string // the author or the reported user; may be "" for "event"
	Relay  string
}

// parseReportTarget reads the wire's structured target, or the shorthand an
// SDK may let through as a plain string: npub/nprofile for a user,
// note/nevent for an event. Bare hex can't say which it is.
func parseReportTarget(raw json.RawMessage) (reportTarget, string) {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "nostr:"))
		prefix, data, err := nip19.Decode(s)
		if err != nil {
			return reportTarget{}, "invalid-target"
		}
		switch v := data.(type) {
		case nostr.PubKey:
			return reportTarget{Type: "pubkey", Pubkey: v.Hex()}, ""
		case nostr.ProfilePointer:
			return reportTarget{Type: "pubkey", Pubkey: v.PublicKey.Hex(), Relay: first(v.Relays)}, ""
		case nostr.EventPointer:
			t := reportTarget{Type: "event", ID: v.ID.Hex(), Relay: first(v.Relays)}
			if prefix == "nevent" && v.Author != nostr.ZeroPK {
				t.Pubkey = v.Author.Hex()
			}
			return t, ""
		}
		return reportTarget{}, "invalid-target"
	}

	var w struct {
		Type   string `json:"type"`
		ID     string `json:"id"`
		Pubkey string `json:"pubkey"`
		Relay  string `json:"relay"`
	}
	if json.Unmarshal(raw, &w) != nil {
		return reportTarget{}, "invalid-target"
	}
	t := reportTarget{Type: w.Type, Relay: w.Relay}
	switch w.Type {
	case "event":
		id, err := nostr.IDFromHex(strings.TrimSpace(w.ID))
		if err != nil {
			return reportTarget{}, "invalid-target"
		}
		t.ID = id.Hex()
		if w.Pubkey != "" {
			pk, ok := npubOrHex(w.Pubkey)
			if !ok {
				return reportTarget{}, "invalid-pubkey"
			}
			t.Pubkey = pk.Hex()
		}
	case "pubkey":
		pk, ok := npubOrHex(w.Pubkey)
		if !ok {
			return reportTarget{}, "invalid-pubkey"
		}
		t.Pubkey = pk.Hex()
	default:
		return reportTarget{}, "invalid-target"
	}
	return t, ""
}

func first(list []string) string {
	if len(list) == 0 {
		return ""
	}
	return list[0]
}

// reportTemplate is the NIP-56 kind 1984 for a resolved target: the reason
// rides on the reported thing's tag, and an event report names its author.
func reportTemplate(t reportTarget, reason, text string) napTemplate {
	tmpl := napTemplate{Kind: 1984, Content: text}
	if t.Type == "event" {
		tmpl.Tags = nostr.Tags{{"e", t.ID, reason}, {"p", t.Pubkey}}
	} else {
		tmpl.Tags = nostr.Tags{{"p", t.Pubkey, reason}}
	}
	return tmpl
}

func napCommonReport(c *napCall) {
	var r struct {
		Target json.RawMessage `json:"target"`
		Reason string          `json:"reason"`
		Text   string          `json:"text"`
	}
	_ = c.decode(&r)
	if _, ok := currentUser(); !ok {
		c.commonFail("not-signed-in")
		return
	}
	if !slices.Contains(reportReasons, r.Reason) {
		c.commonFail("invalid-report-reason")
		return
	}
	target, code := parseReportTarget(r.Target)
	if code != "" {
		c.commonFail(code)
		return
	}
	c.async(func(ctx context.Context) {
		if target.Type == "event" && target.Pubkey == "" {
			// NIP-56 wants the author too: find it, or refuse
			var hints []string
			fctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			if target.Relay != "" {
				if u, err := napExplicitRelay(fctx, target.Relay); err == nil {
					hints = []string{u}
				}
			}
			evt := loadEvent(fctx, json.RawMessage(`"`+target.ID+`"`), hints, "")
			cancel()
			if evt == nil {
				c.commonFail("author-unresolved")
				return
			}
			target.Pubkey = evt.PubKey.Hex()
		}
		t := reportTemplate(target, r.Reason, r.Text)
		pk, _ := nostr.PubKeyFromHex(target.Pubkey)
		what := nip19.EncodeNpub(pk)
		if target.Type == "event" {
			id, _ := nostr.IDFromHex(target.ID)
			what = "a post (" + bech32Encode("note", id[:]) + ") by " + what
		}
		t.ask = &napAsk{
			Title:  "report someone for " + r.Reason,
			Detail: "Reporting " + what,
			Code:   preview(r.Text, 200),
		}
		napCommonPublish(ctx, c, t)
	})
}

// napCommonPublish signs, publishes and answers with NAP-COMMON's codes.
func napCommonPublish(ctx context.Context, c *napCall, t napTemplate) {
	evt, err := napSignAndPublish(ctx, c, t, "", "", "")
	if err != nil {
		code := err.Error()
		switch {
		case code == "not-signed-in", code == "user-denied", code == "publish-failed":
		case errors.Is(err, context.DeadlineExceeded):
			code = "relay-timeout"
		default:
			code = "publish-failed"
		}
		c.commonFail(code)
		return
	}
	c.reply(map[string]any{"ok": true, "eventId": evt.ID.Hex(), "event": evt})
}

// ─── nip19 ───────────────────────────────────────────────────────

// nip19MaxLen is the longest code decodeNip19 looks at (the spec's SHOULD).
const nip19MaxLen = 5000

// NIP-19 TLV types
const (
	tlvDefault = 0
	tlvRelay   = 1
	tlvAuthor  = 2
	tlvKind    = 3
)

// napCommonEncodeNip19 encodes a typed input as a nip19 code. nsec never: a
// napplet has no business with secret keys.
func napCommonEncodeNip19(c *napCall) {
	var r struct {
		Input struct {
			Type       string   `json:"type"`
			Hex        string   `json:"hex"`
			Pubkey     string   `json:"pubkey"`
			EventID    string   `json:"eventId"`
			Author     string   `json:"author"`
			Kind       *int     `json:"kind"`
			Identifier *string  `json:"identifier"`
			Relays     []string `json:"relays"`
			Relay      string   `json:"relay"`
		} `json:"input"`
	}
	if err := c.decode(&r); err != nil {
		c.commonFail("invalid-nip19")
		return
	}
	in := r.Input
	if in.Kind != nil && (*in.Kind < 0 || *in.Kind > 65535) {
		c.commonFail("invalid-nip19")
		return
	}
	var tlv tlvWriter
	var value string
	switch in.Type {
	case "npub":
		pk, err := nostr.PubKeyFromHex(in.Hex)
		if err != nil {
			c.commonFail("invalid-pubkey")
			return
		}
		value = nip19.EncodeNpub(pk)
	case "note":
		id, err := nostr.IDFromHex(in.Hex)
		if err != nil {
			c.commonFail("invalid-nip19")
			return
		}
		value = bech32Encode("note", id[:])
	case "nprofile":
		pk, err := nostr.PubKeyFromHex(in.Pubkey)
		if err != nil {
			c.commonFail("invalid-pubkey")
			return
		}
		tlv.add(tlvDefault, pk[:])
		tlv.addRelays(in.Relays)
		value = tlv.encode("nprofile")
	case "nevent":
		id, err := nostr.IDFromHex(in.EventID)
		if err != nil {
			c.commonFail("invalid-nip19")
			return
		}
		tlv.add(tlvDefault, id[:])
		tlv.addRelays(in.Relays)
		if in.Author != "" {
			author, err := nostr.PubKeyFromHex(in.Author)
			if err != nil {
				c.commonFail("invalid-pubkey")
				return
			}
			tlv.add(tlvAuthor, author[:])
		}
		if in.Kind != nil {
			tlv.add(tlvKind, binary.BigEndian.AppendUint32(nil, uint32(*in.Kind)))
		}
		value = tlv.encode("nevent")
	case "naddr":
		pk, err := nostr.PubKeyFromHex(in.Pubkey)
		if err != nil || in.Kind == nil || in.Identifier == nil {
			c.commonFail("invalid-nip19")
			return
		}
		tlv.add(tlvDefault, []byte(*in.Identifier))
		tlv.addRelays(in.Relays)
		tlv.add(tlvAuthor, pk[:])
		tlv.add(tlvKind, binary.BigEndian.AppendUint32(nil, uint32(*in.Kind)))
		value = tlv.encode("naddr")
	case "nrelay":
		if !strings.HasPrefix(in.Relay, "wss://") && !strings.HasPrefix(in.Relay, "ws://") {
			c.commonFail("invalid-nip19")
			return
		}
		tlv.add(tlvDefault, []byte(in.Relay))
		value = tlv.encode("nrelay")
	default:
		c.commonFail("unsupported-nip19-type")
		return
	}
	if value == "" {
		// a TLV over 255 bytes, or bech32 refused it
		c.commonFail("invalid-nip19")
		return
	}
	c.reply(map[string]any{"ok": true, "value": value, "nip19Type": in.Type})
}

// tlvWriter builds a NIP-19 TLV payload. A value too long for its one-byte
// length spoils the whole thing (encode returns "") rather than wrapping.
type tlvWriter struct {
	buf bytes.Buffer
	bad bool
}

func (w *tlvWriter) add(typ byte, value []byte) {
	if len(value) > 255 {
		w.bad = true
		return
	}
	w.buf.WriteByte(typ)
	w.buf.WriteByte(byte(len(value)))
	w.buf.Write(value)
}

func (w *tlvWriter) addRelays(relays []string) {
	for _, relay := range relays {
		w.add(tlvRelay, []byte(relay))
	}
}

func (w *tlvWriter) encode(prefix string) string {
	if w.bad {
		return ""
	}
	return bech32Encode(prefix, w.buf.Bytes())
}

func napCommonDecodeNip19(c *napCall) {
	var r struct {
		Value string `json:"value"`
	}
	_ = c.decode(&r)
	if len(r.Value) > nip19MaxLen {
		c.commonFail("invalid-nip19")
		return
	}
	code := strings.TrimPrefix(strings.TrimSpace(r.Value), "nostr:")
	if strings.HasPrefix(strings.ToLower(code), "nsec") {
		c.commonFail("unsupported-nip19-type")
		return
	}
	prefix, data, err := nip19.Decode(code)
	if err != nil {
		// a well-formed code of a kind we don't hand out (ncryptsec, …)
		// comes back as its raw bytes; nrelay is one the library leaves to us
		if raw, ok := data.([]byte); ok && prefix != "" {
			if prefix != "nrelay" {
				c.commonFail("unsupported-nip19-type")
				return
			}
			relay, ok := nrelayURL(raw)
			if !ok {
				c.commonFail("invalid-nip19")
				return
			}
			c.reply(map[string]any{"ok": true, "nip19Type": "nrelay", "relay": relay})
			return
		}
		c.commonFail("invalid-nip19")
		return
	}
	out := map[string]any{"ok": true, "nip19Type": prefix}
	switch v := data.(type) {
	case nostr.PubKey:
		out["hex"], out["pubkey"] = v.Hex(), v.Hex()
	case nostr.ProfilePointer:
		out["pubkey"], out["relays"] = v.PublicKey.Hex(), nonNil(v.Relays)
	case nostr.EventPointer:
		out["eventId"] = v.ID.Hex()
		if prefix == "note" {
			out["hex"] = v.ID.Hex()
		} else {
			out["relays"] = nonNil(v.Relays)
			if v.Author != nostr.ZeroPK {
				out["author"] = v.Author.Hex()
			}
			if v.Kind != 0 {
				out["kind"] = int(v.Kind)
			}
		}
	case nostr.EntityPointer:
		out["pubkey"], out["kind"], out["identifier"] = v.PublicKey.Hex(), int(v.Kind), v.Identifier
		out["relays"] = nonNil(v.Relays)
	default:
		c.commonFail("unsupported-nip19-type")
		return
	}
	c.reply(out)
}

// nrelayURL is the relay url in an nrelay's TLV payload (type 0), skipping
// any TLV it doesn't know.
func nrelayURL(data []byte) (string, bool) {
	for len(data) >= 2 {
		typ, n := data[0], int(data[1])
		if len(data) < 2+n {
			return "", false
		}
		if typ == tlvDefault {
			return string(data[2 : 2+n]), true
		}
		data = data[2+n:]
	}
	return "", false
}

func nonNil(list []string) []string {
	if list == nil {
		return []string{}
	}
	return list
}

func bech32Encode(prefix string, data []byte) string {
	bits5, err := bech32.ConvertBits(data, 8, 5, true)
	if err != nil {
		return ""
	}
	out, _ := bech32.Encode(prefix, bits5)
	return out
}
