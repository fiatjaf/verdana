package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/sdk"
)

// fetchListArgs parses a {pubkey} params blob and returns the pubkey plus a
// short-lived context for the various napp.load* list/set fetches. ok is false
// when the system isn't ready or the pubkey is missing/invalid, in which case
// the caller should return an empty result.
func fetchListArgs(params string) (pk nostr.PubKey, ctx context.Context, cancel context.CancelFunc, ok bool) {
	var p struct {
		Pubkey string `json:"pubkey"`
	}
	json.Unmarshal([]byte(params), &p)
	if p.Pubkey == "" {
		log.Debug().Msg("fetchListArgs: no pubkey in params")
		return pk, nil, func() {}, false
	}
	pk, err := nostr.PubKeyFromHex(p.Pubkey)
	if err != nil {
		log.Debug().Err(err).Msg("fetchListArgs: invalid pubkey")
		return pk, nil, func() {}, false
	}
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	return pk, ctx, cancel, true
}

func bridgeRPC(ci *childInfo) func(string, string) (any, error) {
	return func(method string, params string) (any, error) {
		log.Debug().Str("method", method).Int("child", ci.cmd.Process.Pid).Msg("bridge rpc call")
		switch method {
		case "getPublicKey":
			if userKeyer == nil {
				return "", errors.New("not logged in")
			}
			if userPubkey != (nostr.PubKey{}) {
				return userPubkey.Hex(), nil
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			pk, err := userKeyer.GetPublicKey(ctx)
			if err != nil {
				return "", err
			}
			return pk.Hex(), nil
		case "signEvent":
			if userKeyer == nil {
				return nil, errors.New("not logged in")
			}
			var evt nostr.Event
			if err := json.Unmarshal([]byte(params), &evt); err != nil {
				return nil, err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := userKeyer.SignEvent(ctx, &evt); err != nil {
				return nil, err
			}
			return evt, nil
		case "nip04.encrypt", "nip04.decrypt":
			if userKeyer == nil {
				return "", errors.New("not logged in")
			}
			var p struct {
				Pubkey     string `json:"pubkey"`
				Plaintext  string `json:"plaintext"`
				Ciphertext string `json:"ciphertext"`
			}
			if err := json.Unmarshal([]byte(params), &p); err != nil {
				return "", err
			}
			pk, err := nostr.PubKeyFromHex(p.Pubkey)
			if err != nil {
				return "", err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if strings.HasSuffix(method, ".encrypt") {
				return userKeyer.Nip04Encrypt(ctx, p.Plaintext, pk)
			}
			return userKeyer.Nip04Decrypt(ctx, p.Ciphertext, pk)
		case "nip44.encrypt", "nip44.decrypt":
			if userKeyer == nil {
				return "", errors.New("not logged in")
			}
			var p struct {
				Pubkey     string `json:"pubkey"`
				Plaintext  string `json:"plaintext"`
				Ciphertext string `json:"ciphertext"`
			}
			if err := json.Unmarshal([]byte(params), &p); err != nil {
				return "", err
			}
			pk, err := nostr.PubKeyFromHex(p.Pubkey)
			if err != nil {
				return "", err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if strings.HasSuffix(method, ".encrypt") {
				return userKeyer.Encrypt(ctx, p.Plaintext, pk)
			}
			return userKeyer.Decrypt(ctx, p.Ciphertext, pk)
		case "nostrdb.add":
			var p struct {
				Event nostr.Event `json:"event"`
			}
			if err := json.Unmarshal([]byte(params), &p); err != nil {
				return false, err
			}
			if err := sys.Store.SaveEvent(p.Event); err != nil {
				return false, err
			}
			return true, nil
		case "nostrdb.query":
			out := []nostr.Event{}
			var p struct {
				Filters []nostr.Filter `json:"filters"`
			}
			if err := json.Unmarshal([]byte(params), &p); err != nil {
				return nil, err
			}
			seen := make(map[nostr.ID]bool)
			for _, f := range p.Filters {
				max := f.Limit
				if max <= 0 {
					max = 500
				}
				for evt := range sys.Store.QueryEvents(f, max) {
					if !seen[evt.ID] {
						seen[evt.ID] = true
						out = append(out, evt)
					}
				}
			}
			return out, nil
		case "nostrdb.count":
			var p struct {
				Filters []nostr.Filter `json:"filters"`
			}
			if err := json.Unmarshal([]byte(params), &p); err != nil {
				return 0, err
			}
			var total int
			for _, f := range p.Filters {
				c, err := sys.Store.CountEvents(f)
				if err != nil {
					return 0, err
				}
				total += int(c)
			}
			return total, nil
		case "nostrdb.event":
			var p struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal([]byte(params), &p); err != nil {
				return nil, err
			}
			id, err := nostr.IDFromHex(p.ID)
			if err != nil {
				return nil, nil
			}
			for evt := range sys.Store.QueryEvents(nostr.Filter{IDs: []nostr.ID{id}}, 1) {
				return evt, nil
			}
			return nil, nil
		case "nostrdb.replaceable":
			var p struct {
				Kind       int    `json:"kind"`
				Author     string `json:"author"`
				Identifier string `json:"identifier"`
			}
			if err := json.Unmarshal([]byte(params), &p); err != nil {
				return nil, err
			}
			pk, err := nostr.PubKeyFromHex(p.Author)
			if err != nil {
				return nil, nil
			}
			f := nostr.Filter{
				Kinds:   []nostr.Kind{nostr.Kind(p.Kind)},
				Authors: []nostr.PubKey{pk},
			}
			if p.Identifier != "" {
				f.Tags = nostr.TagMap{"d": []string{p.Identifier}}
			}
			var newest *nostr.Event
			for evt := range sys.Store.QueryEvents(f, 10) {
				if newest == nil || evt.CreatedAt > newest.CreatedAt {
					e := evt
					newest = &e
				}
			}
			if newest == nil {
				return nil, nil
			}
			return *newest, nil
		case "napp.action":
			return nil, nil
		case "napp.feeds.profile", "napp.feeds.following", "napp.feeds.inbox":
			var p struct {
				Pubkey     string           `json:"pubkey"`
				Source     string           `json:"source"`
				Kinds      []nostr.Kind     `json:"kinds"`
				CallbackId int              `json:"callbackId"`
				Since      *nostr.Timestamp `json:"since"`
				Until      *nostr.Timestamp `json:"until"`
				Limit      int              `json:"limit"`
			}
			json.Unmarshal([]byte(params), &p)
			ctx, cancel := context.WithCancel(context.Background())
			ci.subMu.Lock()
			ci.subs[p.CallbackId] = cancel
			ci.subMu.Unlock()
			go feedPump(ctx, ci, method, p)
			return nil, nil
		case "napp.feeds.cancel":
			var p struct {
				CallbackId int `json:"callbackId"`
			}
			json.Unmarshal([]byte(params), &p)
			ci.subMu.Lock()
			if cancel, ok := ci.subs[p.CallbackId]; ok {
				cancel()
				delete(ci.subs, p.CallbackId)
			}
			ci.subMu.Unlock()
			return nil, nil
		case "napp.loadBlossomServers":
			pk, ctx, cancel, ok := fetchListArgs(params)
			if !ok {
				return []any{}, nil
			}
			defer cancel()
			return sys.FetchBlossomServerList(ctx, pk).Items, nil
		case "napp.loadBookmarks":
			pk, ctx, cancel, ok := fetchListArgs(params)
			if !ok {
				return []any{}, nil
			}
			defer cancel()
			return sys.FetchBookmarkList(ctx, pk).Items, nil
		case "napp.loadEmojis":
			// no corresponding sys.Fetch method
			return []any{}, nil
		case "napp.loadFavoriteRelays":
			// no corresponding sys.Fetch method
			return []any{}, nil
		case "napp.loadFollowsList":
			pk, ctx, cancel, ok := fetchListArgs(params)
			if !ok {
				return []any{}, nil
			}
			defer cancel()
			return sys.FetchFollowList(ctx, pk).Items, nil
		case "napp.loadMuteList":
			pk, ctx, cancel, ok := fetchListArgs(params)
			if !ok {
				return []any{}, nil
			}
			defer cancel()
			return sys.FetchMuteList(ctx, pk).Items, nil
		case "napp.loadPins":
			pk, ctx, cancel, ok := fetchListArgs(params)
			if !ok {
				return []any{}, nil
			}
			defer cancel()
			return sys.FetchPinList(ctx, pk).Items, nil
		case "napp.loadRelayList":
			pk, ctx, cancel, ok := fetchListArgs(params)
			if !ok {
				return []any{}, nil
			}
			defer cancel()
			return sys.FetchRelayList(ctx, pk).Items, nil
		case "napp.loadWikiAuthors":
			pk, ctx, cancel, ok := fetchListArgs(params)
			if !ok {
				return []any{}, nil
			}
			defer cancel()
			return sys.FetchGoodWikiAuthorList(ctx, pk).Items, nil
		case "napp.loadWikiRelays":
			pk, ctx, cancel, ok := fetchListArgs(params)
			if !ok {
				return []any{}, nil
			}
			defer cancel()
			return sys.FetchGoodWikiRelayList(ctx, pk).Items, nil
		case "napp.loadEmojiSets":
			// no corresponding sys.Fetch method
			return struct{}{}, nil
		case "napp.loadFollowSets":
			pk, ctx, cancel, ok := fetchListArgs(params)
			if !ok {
				return struct{}{}, nil
			}
			defer cancel()
			return sys.FetchFollowSets(ctx, pk).Sets, nil
		case "napp.loadRelaySets":
			pk, ctx, cancel, ok := fetchListArgs(params)
			if !ok {
				return struct{}{}, nil
			}
			defer cancel()
			return sys.FetchRelaySets(ctx, pk).Sets, nil
		case "napp.loadRelayInfo":
			// no corresponding sys.Fetch method
			return nil, nil
		case "napp.loadNostrUser":
			// The JS bridge sends a bare JSON string (the npub/nprofile/nip05/hex
			// reference). FetchProfileFromInput wants that bare input.
			var input string
			if err := json.Unmarshal([]byte(params), &input); err != nil {
				log.Debug().Str("params", params).Err(err).Msg("loadNostrUser: invalid params")
				return nil, nil
			}
			input = strings.TrimSpace(input)
			if input == "" {
				log.Debug().Str("params", params).Msg("loadNostrUser: empty input")
				return nil, nil
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			pm, err := sys.FetchProfileFromInput(ctx, input)
			if err != nil {
				log.Debug().Str("input", input).Err(err).Msg("loadNostrUser: fetch failed")
				return nil, nil
			}
			user := struct {
				Pubkey      string               `json:"pubkey"`
				Npub        string               `json:"npub"`
				ShortName   string               `json:"shortName"`
				Metadata    sdk.ProfileMetadata  `json:"metadata"`
				Image       string               `json:"image,omitempty"`
				LastUpdated nostr.Timestamp       `json:"lastUpdated,omitempty"`
			}{
				Pubkey:    pm.PubKey.Hex(),
				Npub:      pm.Npub(),
				ShortName: pm.ShortName(),
				Metadata:  pm,
			}
			if pm.Picture != "" {
				user.Image = pm.Picture
			}
			if pm.Event != nil {
				user.LastUpdated = pm.Event.CreatedAt
			}
			return user, nil
		case "napp.loadEvent":
			var p struct {
				Code string `json:"code"`
			}
			json.Unmarshal([]byte(params), &p)
			if p.Code == "" {
				return nil, nil
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			evt, _, err := sys.FetchSpecificEventFromInput(ctx, p.Code, sdk.FetchSpecificEventParameters{SaveToLocalStore: true})
			if err != nil || evt == nil {
				return nil, nil
			}
			return *evt, nil
		case "napp.publish":
			var p struct {
				Event  nostr.Event `json:"event"`
				Relays []string    `json:"relays"`
			}
			if err := json.Unmarshal([]byte(params), &p); err != nil {
				return nil, err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			log.Debug().Str("kind", fmt.Sprint(p.Event.Kind)).Int("relays", len(p.Relays)).Msg("publishing event")

			seen := make(map[string]bool)
			var targets []string
			addRelay := func(url string) {
				url = strings.TrimSpace(url)
				if url == "" {
					return
				}
				norm := nostr.NormalizeURL(url)
				if norm == "" || seen[norm] {
					return
				}
				seen[norm] = true
				targets = append(targets, norm)
			}

			for _, url := range p.Relays {
				addRelay(url)
			}
			for _, url := range sys.FetchOutboxRelays(ctx, p.Event.PubKey, 3) {
				addRelay(url)
			}
			for _, key := range []string{"p", "P"} {
				for tag := range p.Event.Tags.FindAll(key) {
					pk, err := nostr.PubKeyFromHex(tag[1])
					if err != nil {
						continue
					}
					for _, url := range sys.FetchInboxRelays(ctx, pk, 3) {
						addRelay(url)
					}
				}
			}

			if len(targets) == 0 {
				return nil, errors.New("no relays to publish to")
			}

			log.Debug().Strs("targets", targets).Msg("publishing to relays")

			results := []struct {
				Relay   string `json:"relay"`
				Success bool   `json:"success"`
				Error   string `json:"error,omitempty"`
			}{}
			for res := range sys.Pool.PublishMany(ctx, targets, p.Event) {
				r := struct {
					Relay   string `json:"relay"`
					Success bool   `json:"success"`
					Error   string `json:"error,omitempty"`
				}{Relay: res.RelayURL, Success: res.Error == nil}
				if res.Error != nil {
					r.Error = res.Error.Error()
					log.Warn().Str("relay", res.RelayURL).Err(res.Error).Msg("publish to relay failed")
				} else {
					log.Debug().Str("relay", res.RelayURL).Msg("publish to relay succeeded")
				}
				results = append(results, r)
			}
			return results, nil
		default:
			return nil, nil
		}
	}
}

func feedPump(ctx context.Context, ci *childInfo, method string, p struct {
	Pubkey     string           `json:"pubkey"`
	Source     string           `json:"source"`
	Kinds      []nostr.Kind     `json:"kinds"`
	CallbackId int              `json:"callbackId"`
	Since      *nostr.Timestamp `json:"since"`
	Until      *nostr.Timestamp `json:"until"`
	Limit      int              `json:"limit"`
},
) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(30 * time.Second):
			ci.eval("")
		}
	}
}
