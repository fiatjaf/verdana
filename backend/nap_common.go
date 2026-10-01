package backend

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip19"
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
	pk, ok := npubOrHex(r.Target)
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
		out := map[string]any{"ok": true, "pubkey": pk.Hex(), "profile": nil}
		if pm.Event != nil {
			out["profile"] = profileData(pm)
			out["result"] = relayEventResult(*pm.Event)
		}
		c.reply(out)
	})
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
		c.reply(map[string]any{"ok": true, "pubkeys": identityFollows(ctx, pk)})
	})
}

// napCommonFollow edits the user's kind 3 follow list. It starts from the
// newest list the launcher can find, so other clients' entries (and the
// list's content, which some clients still use for relays) survive.
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

		c.async(func(ctx context.Context) {
			fctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			current := fetchReplaceable(fctx, 3, user)
			cancel()

			t := napTemplate{Kind: 3, Tags: nostr.Tags{}}
			if current != nil {
				t.Content = current.Content
				t.Tags = slices.Clone(current.Tags)
			}
			for _, pk := range targets {
				hex := pk.Hex()
				has := slices.ContainsFunc(t.Tags, func(tag nostr.Tag) bool {
					return len(tag) >= 2 && tag[0] == "p" && tag[1] == hex
				})
				switch {
				case follow && !has:
					t.Tags = append(t.Tags, nostr.Tag{"p", hex})
				case !follow && has:
					t.Tags = slices.DeleteFunc(t.Tags, func(tag nostr.Tag) bool {
						return len(tag) >= 2 && tag[0] == "p" && tag[1] == hex
					})
				}
			}
			napCommonPublish(ctx, c, t)
		})
	}
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
	reaction := r.Reaction
	if reaction == "" || len(reaction) > 64 {
		c.commonFail("invalid-reaction")
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
		t := napTemplate{Kind: 7, Content: reaction, Tags: nostr.Tags{
			{"e", target.ID.Hex()},
			{"p", target.PubKey.Hex()},
			{"k", strconv.Itoa(int(target.Kind))},
		}}
		if r.CustomEmojiHref != "" && strings.HasPrefix(reaction, ":") && strings.HasSuffix(reaction, ":") {
			t.Tags = append(t.Tags, nostr.Tag{"emoji", strings.Trim(reaction, ":"), r.CustomEmojiHref})
		}
		napCommonPublish(ctx, c, t)
	})
}

var reportReasons = []string{"nudity", "malware", "profanity", "illegal", "spam", "impersonation", "other"}

func napCommonReport(c *napCall) {
	var r struct {
		Target struct {
			Type   string `json:"type"`
			ID     string `json:"id"`
			Pubkey string `json:"pubkey"`
		} `json:"target"`
		Reason string `json:"reason"`
		Text   string `json:"text"`
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
	t := napTemplate{Kind: 1984, Content: r.Text, Tags: nostr.Tags{}}
	switch r.Target.Type {
	case "event":
		id, err := nostr.IDFromHex(r.Target.ID)
		if err != nil {
			c.commonFail("invalid-target")
			return
		}
		t.Tags = append(t.Tags, nostr.Tag{"e", id.Hex(), r.Reason})
		if pk, ok := npubOrHex(r.Target.Pubkey); ok {
			t.Tags = append(t.Tags, nostr.Tag{"p", pk.Hex()})
		}
	case "pubkey":
		pk, ok := npubOrHex(r.Target.Pubkey)
		if !ok {
			c.commonFail("invalid-pubkey")
			return
		}
		t.Tags = append(t.Tags, nostr.Tag{"p", pk.Hex(), r.Reason})
	default:
		c.commonFail("invalid-target")
		return
	}
	c.async(func(ctx context.Context) { napCommonPublish(ctx, c, t) })
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
		value = nip19.EncodeNprofile(pk, in.Relays)
	case "nevent":
		id, err := nostr.IDFromHex(in.EventID)
		if err != nil {
			c.commonFail("invalid-nip19")
			return
		}
		var author nostr.PubKey
		if in.Author != "" {
			if author, err = nostr.PubKeyFromHex(in.Author); err != nil {
				c.commonFail("invalid-pubkey")
				return
			}
		}
		value = nip19.EncodeNevent(id, in.Relays, author)
	case "naddr":
		pk, err := nostr.PubKeyFromHex(in.Pubkey)
		if err != nil || in.Kind == nil || in.Identifier == nil || *in.Kind < 0 || *in.Kind > 65535 {
			c.commonFail("invalid-nip19")
			return
		}
		value = nip19.EncodeNaddr(pk, nostr.Kind(*in.Kind), *in.Identifier, in.Relays)
	case "nrelay":
		if !strings.HasPrefix(in.Relay, "wss://") && !strings.HasPrefix(in.Relay, "ws://") {
			c.commonFail("invalid-nip19")
			return
		}
		// TLV 0: the relay url
		value = bech32Encode("nrelay", append([]byte{0, byte(len(in.Relay))}, in.Relay...))
	default:
		c.commonFail("unsupported-nip19-type")
		return
	}
	c.reply(map[string]any{"ok": true, "value": value, "nip19Type": in.Type})
}

func napCommonDecodeNip19(c *napCall) {
	var r struct {
		Value string `json:"value"`
	}
	_ = c.decode(&r)
	code := strings.TrimPrefix(strings.TrimSpace(r.Value), "nostr:")
	if strings.HasPrefix(strings.ToLower(code), "nsec") {
		c.commonFail("unsupported-nip19-type")
		return
	}
	prefix, data, err := nip19.Decode(code)
	if err != nil && prefix != "nrelay" {
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
	case []byte:
		// nrelay: the library leaves it to us (TLV 0 holds the url)
		if prefix != "nrelay" || len(v) < 2 || v[0] != 0 || int(v[1]) > len(v)-2 {
			c.commonFail("invalid-nip19")
			return
		}
		out["relay"] = string(v[2 : 2+int(v[1])])
	default:
		c.commonFail("unsupported-nip19-type")
		return
	}
	c.reply(out)
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
