package backend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/sdk"
)

// This is the host side of window.nostr / window.nostrdb / window.napp: every
// method here is something bridge.js can call from inside a napp's webview.
// The napp-facing shapes are the ones documented in env.d.ts — that file is
// the contract, this file honors it.

// resolveUserParam turns the argument of a load* call into a pubkey. Napps may
// pass hex, npub, nprofile or a nip05 address (and loadNostrUser may pass the
// whole { pubkey, relays } request object).
func resolveUserParam(ctx context.Context, params string) (nostr.PubKey, []string, bool) {
	var zero nostr.PubKey
	trimmed := strings.TrimSpace(params)
	if trimmed == "" || trimmed == "null" {
		return zero, nil, false
	}

	input := ""
	var relays []string

	if err := json.Unmarshal([]byte(params), &input); err != nil {
		var req struct {
			Pubkey string   `json:"pubkey"`
			Relays []string `json:"relays"`
		}
		if err := json.Unmarshal([]byte(params), &req); err != nil {
			return zero, nil, false
		}
		input = req.Pubkey
		relays = req.Relays
	}

	input = strings.TrimSpace(input)
	if input == "" {
		return zero, nil, false
	}
	if pk, err := nostr.PubKeyFromHex(input); err == nil {
		return pk, relays, true
	}
	pp := sdk.InputToProfile(ctx, input)
	if pp == nil {
		log.Debug().Str("input", preview(input, 40)).Msg("could not resolve a pubkey")
		return zero, nil, false
	}
	return pp.PublicKey, append(relays, pp.Relays...), true
}

// listCall is the shared preamble of every load* rpc: resolve the pubkey and
// give the fetch a deadline.
func listCall(params string, fn func(context.Context, nostr.PubKey) any, empty any) (any, error) {
	if sys == nil {
		return empty, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pk, _, ok := resolveUserParam(ctx, params)
	if !ok {
		return empty, nil
	}
	return fn(ctx, pk), nil
}

func bridgeRPC(ci *Instance) func(string, string) (any, error) {
	return func(method string, params string) (any, error) {
		log.Debug().Str("method", method).Str("instance", ci.instance).Msg("bridge rpc call")

		switch method {
		// ─── window.nostr ────────────────────────────────────────
		case "getPublicKey":
			// answers from the cached account key and never prompts
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
			if !askApproval(ci, "sign an event with your key",
				fmt.Sprintf("Kind %d, %d tags.", evt.Kind, len(evt.Tags)),
				preview(evt.Content, 200)) {
				return nil, errors.New("denied by the user")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			if err := userKeyer.SignEvent(ctx, &evt); err != nil {
				return nil, keyerErr(err)
			}
			return evt, nil

		case "nip04.encrypt", "nip04.decrypt", "nip44.encrypt", "nip44.decrypt":
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
			encrypting := strings.HasSuffix(method, ".encrypt")
			verb := "decrypt a message with your key"
			payload := preview(p.Ciphertext, 120)
			if encrypting {
				verb = "encrypt a message with your key"
				payload = preview(p.Plaintext, 120)
			}
			if !askApproval(ci, verb, "Counterparty "+shortPubkey(pk)+" ("+strings.SplitN(method, ".", 2)[0]+").", payload) {
				return "", errors.New("denied by the user")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			switch method {
			case "nip04.encrypt":
				res, err := userKeyer.Nip04Encrypt(ctx, p.Plaintext, pk)
				return res, keyerErr(err)
			case "nip04.decrypt":
				res, err := userKeyer.Nip04Decrypt(ctx, p.Ciphertext, pk)
				return res, keyerErr(err)
			case "nip44.encrypt":
				res, err := userKeyer.Encrypt(ctx, p.Plaintext, pk)
				return res, keyerErr(err)
			default:
				res, err := userKeyer.Decrypt(ctx, p.Ciphertext, pk)
				return res, keyerErr(err)
			}

		// ─── window.nostrdb ──────────────────────────────────────
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
			var p struct {
				Filters json.RawMessage `json:"filters"`
			}
			if err := json.Unmarshal([]byte(params), &p); err != nil {
				return nil, err
			}
			filters, err := parseFilters(p.Filters)
			if err != nil {
				return nil, err
			}
			out := []nostr.Event{}
			seen := make(map[nostr.ID]bool)
			for _, f := range filters {
				maxLimit := f.Limit
				if maxLimit <= 0 {
					maxLimit = 500
				}
				for evt := range sys.Store.QueryEvents(f, maxLimit) {
					if !seen[evt.ID] {
						seen[evt.ID] = true
						out = append(out, evt)
					}
				}
			}
			return out, nil

		case "nostrdb.count":
			var p struct {
				Filters json.RawMessage `json:"filters"`
			}
			if err := json.Unmarshal([]byte(params), &p); err != nil {
				return 0, err
			}
			filters, err := parseFilters(p.Filters)
			if err != nil {
				return 0, err
			}
			seen := make(map[nostr.ID]bool)
			for _, f := range filters {
				if len(filters) == 1 {
					c, err := sys.Store.CountEvents(f)
					if err != nil {
						return 0, err
					}
					return int(c), nil
				}
				for evt := range sys.Store.QueryEvents(f, 10000) {
					seen[evt.ID] = true
				}
			}
			return len(seen), nil

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

		case "nostrdb.remove":
			var p struct {
				IDs []string `json:"ids"`
			}
			if err := json.Unmarshal([]byte(params), &p); err != nil {
				return nil, err
			}
			removed := []string{}
			for _, raw := range p.IDs {
				id, err := nostr.IDFromHex(strings.TrimSpace(raw))
				if err != nil {
					continue
				}
				if err := sys.Store.DeleteEvent(id); err == nil {
					removed = append(removed, id.Hex())
				}
			}
			return removed, nil

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

		// ─── actions ─────────────────────────────────────────────
		case "napp.action":
			var p struct {
				Name    string          `json:"name"`
				Payload json.RawMessage `json:"payload"`
				Options actionOptions   `json:"options"`
			}
			if err := json.Unmarshal([]byte(params), &p); err != nil {
				return nil, err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			return runNappAction(ctx, ci, p.Name, p.Payload, p.Options)

		case "napp.registerAction":
			var p struct {
				Pattern string `json:"pattern"`
				Idx     *int   `json:"idx"`
			}
			if err := json.Unmarshal([]byte(params), &p); err != nil {
				return nil, err
			}
			if p.Pattern == "" {
				return nil, errors.New("pattern required")
			}
			idx := -1
			if p.Idx != nil {
				idx = *p.Idx
			}
			ci.registerAction(p.Pattern, idx)
			return nil, nil

		case "napp.dispatchResult":
			var p struct {
				ID     int             `json:"id"`
				Result json.RawMessage `json:"result"`
				Error  string          `json:"error"`
			}
			if err := json.Unmarshal([]byte(params), &p); err != nil {
				return nil, err
			}
			ci.settleDispatch(p.ID, p.Result, p.Error)
			return nil, nil

		case "napp.actionState":
			// the napp navigated on its own and told us where it went, so a
			// later restore/share can put the window back on this action
			var p struct {
				Name    string          `json:"name"`
				Payload json.RawMessage `json:"payload"`
			}
			if err := json.Unmarshal([]byte(params), &p); err != nil {
				return nil, err
			}
			if p.Name != "" {
				ci.lastAction.Store(&actionRequest{name: p.Name, payload: p.Payload})
				notifyState()
			}
			return nil, nil

		case "napp.close":
			log.Info().Str("instance", ci.instance).Msg("napp asked to close its window")
			ci.send(WireMsg{T: "close"})
			return nil, nil

		case "napp.link":
			var url string
			if err := json.Unmarshal([]byte(params), &url); err != nil {
				var p struct {
					URL string `json:"url"`
				}
				if err := json.Unmarshal([]byte(params), &p); err != nil {
					return nil, err
				}
				url = p.URL
			}
			if !askApproval(ci, "open a link in your browser", "", preview(url, 200)) {
				return nil, errors.New("denied by the user")
			}
			return nil, openExternalLink(url)

		// ─── feeds ───────────────────────────────────────────────
		case "napp.feeds.profile", "napp.feeds.following", "napp.feeds.inbox", "napp.feeds.outbox":
			var p feedParams
			if err := json.Unmarshal([]byte(params), &p); err != nil {
				return nil, err
			}
			ctx, cancel := context.WithCancel(context.Background())
			ci.subMu.Lock()
			if old, ok := ci.subs[p.CallbackID]; ok {
				old()
			}
			ci.subs[p.CallbackID] = cancel
			ci.subMu.Unlock()
			go startFeed(ctx, ci, method, p)
			return nil, nil

		case "napp.feeds.cancel":
			var p struct {
				CallbackID int `json:"callbackId"`
			}
			json.Unmarshal([]byte(params), &p)
			ci.subMu.Lock()
			if cancel, ok := ci.subs[p.CallbackID]; ok {
				cancel()
				delete(ci.subs, p.CallbackID)
			}
			ci.subMu.Unlock()
			return nil, nil

		// ─── NIP-51 lists ────────────────────────────────────────
		case "napp.loadRelayList":
			return listCall(params, func(ctx context.Context, pk nostr.PubKey) any {
				return loadRelayList(ctx, pk)
			}, emptyListResult())
		case "napp.loadFollowsList":
			return listCall(params, func(ctx context.Context, pk nostr.PubKey) any {
				return loadFollowsList(ctx, pk)
			}, emptyListResult())
		case "napp.loadMuteList":
			return listCall(params, func(ctx context.Context, pk nostr.PubKey) any {
				return loadMuteList(ctx, pk)
			}, emptyListResult())
		case "napp.loadBookmarks":
			return listCall(params, func(ctx context.Context, pk nostr.PubKey) any {
				return loadBookmarks(ctx, pk)
			}, emptyListResult())
		case "napp.loadPins":
			return listCall(params, func(ctx context.Context, pk nostr.PubKey) any {
				return loadPins(ctx, pk)
			}, emptyListResult())
		case "napp.loadBlossomServers":
			return listCall(params, func(ctx context.Context, pk nostr.PubKey) any {
				return loadBlossomServers(ctx, pk)
			}, emptyListResult())
		case "napp.loadEmojis":
			return listCall(params, func(ctx context.Context, pk nostr.PubKey) any {
				return loadEmojis(ctx, pk)
			}, emptyListResult())
		case "napp.loadFavoriteRelays":
			return listCall(params, func(ctx context.Context, pk nostr.PubKey) any {
				return loadFavoriteRelays(ctx, pk)
			}, emptyListResult())
		case "napp.loadBlockedRelays":
			return listCall(params, func(ctx context.Context, pk nostr.PubKey) any {
				return loadBlockedRelays(ctx, pk)
			}, emptyListResult())
		case "napp.loadSearchRelays":
			return listCall(params, func(ctx context.Context, pk nostr.PubKey) any {
				return loadSearchRelays(ctx, pk)
			}, emptyListResult())
		case "napp.loadDmRelays":
			return listCall(params, func(ctx context.Context, pk nostr.PubKey) any {
				return loadDmRelays(ctx, pk)
			}, emptyListResult())
		case "napp.loadWikiAuthors":
			return listCall(params, func(ctx context.Context, pk nostr.PubKey) any {
				return loadWikiAuthors(ctx, pk)
			}, emptyListResult())
		case "napp.loadWikiRelays":
			return listCall(params, func(ctx context.Context, pk nostr.PubKey) any {
				return loadWikiRelays(ctx, pk)
			}, emptyListResult())
		case "napp.loadFavoriteFollowSets":
			return listCall(params, func(ctx context.Context, pk nostr.PubKey) any {
				return loadFavoriteFollowSets(ctx, pk)
			}, emptyListResult())
		case "napp.loadFavoriteScrolls":
			return listCall(params, func(ctx context.Context, pk nostr.PubKey) any {
				return loadFavoriteScrolls(ctx, pk)
			}, emptyListResult())
		case "napp.loadProfileBadges":
			return listCall(params, func(ctx context.Context, pk nostr.PubKey) any {
				return loadProfileBadges(ctx, pk)
			}, emptyListResult())
		case "napp.loadSimpleGroups":
			return listCall(params, func(ctx context.Context, pk nostr.PubKey) any {
				return loadSimpleGroups(ctx, pk)
			}, emptyListResult())
		case "napp.loadGitAuthors":
			return listCall(params, func(ctx context.Context, pk nostr.PubKey) any {
				return loadGitAuthors(ctx, pk)
			}, emptyListResult())
		case "napp.loadGitRepositories":
			return listCall(params, func(ctx context.Context, pk nostr.PubKey) any {
				return loadGitRepositories(ctx, pk)
			}, emptyListResult())
		case "napp.loadMediaFollows":
			return listCall(params, func(ctx context.Context, pk nostr.PubKey) any {
				return loadMediaFollows(ctx, pk)
			}, emptyListResult())
		case "napp.loadFavoritePodcasts":
			return listCall(params, func(ctx context.Context, pk nostr.PubKey) any {
				return loadFavoritePodcasts(ctx, pk)
			}, emptyListResult())
		case "napp.loadAuthoredPodcasts":
			return listCall(params, func(ctx context.Context, pk nostr.PubKey) any {
				return loadAuthoredPodcasts(ctx, pk)
			}, emptyListResult())

		// ─── composite list+set helpers ──────────────────────────
		case "napp.fetchFavoriteRelaysWithSets":
			return listCall(params, func(ctx context.Context, pk nostr.PubKey) any {
				return fetchFavoriteRelaysWithSets(ctx, pk)
			}, []any{})
		case "napp.fetchEmojisWithSets":
			return listCall(params, func(ctx context.Context, pk nostr.PubKey) any {
				return fetchEmojisWithSets(ctx, pk)
			}, []any{})
		case "napp.fetchFavoriteFollowSetsWithSets":
			return listCall(params, func(ctx context.Context, pk nostr.PubKey) any {
				return fetchFavoriteFollowSetsWithSets(ctx, pk)
			}, []any{})

		// ─── addressable sets ────────────────────────────────────
		case "napp.loadFollowSets":
			return listCall(params, func(ctx context.Context, pk nostr.PubKey) any {
				return loadFollowSets(ctx, pk)
			}, map[string]any{})
		case "napp.loadRelaySets":
			return listCall(params, func(ctx context.Context, pk nostr.PubKey) any {
				return loadRelaySets(ctx, pk)
			}, map[string]any{})
		case "napp.loadEmojiSets":
			return listCall(params, func(ctx context.Context, pk nostr.PubKey) any {
				return loadEmojiSets(ctx, pk)
			}, map[string]any{})

		// ─── relay info ──────────────────────────────────────────
		case "napp.loadRelayInfo":
			var url string
			if err := json.Unmarshal([]byte(params), &url); err != nil {
				var p struct {
					URL string `json:"url"`
				}
				if err := json.Unmarshal([]byte(params), &p); err != nil {
					return nil, nil
				}
				url = p.URL
			}
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			return loadRelayInfo(ctx, url), nil

		// ─── profile metadata + search ───────────────────────────
		case "napp.loadNostrUser":
			if sys == nil {
				return nil, errors.New("system not ready")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			var input string
			var relays []string
			if err := json.Unmarshal([]byte(params), &input); err != nil {
				var req struct {
					Pubkey string   `json:"pubkey"`
					Relays []string `json:"relays"`
				}
				if err := json.Unmarshal([]byte(params), &req); err != nil {
					return nil, errors.New("invalid request")
				}
				input, relays = req.Pubkey, req.Relays
			}
			return loadNostrUser(ctx, input, relays)

		case "napp.searchUserLocal":
			var term string
			json.Unmarshal([]byte(params), &term)
			return searchUserLocal(term), nil

		case "napp.searchUser":
			var term string
			json.Unmarshal([]byte(params), &term)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			return searchUser(ctx, term), nil

		// ─── event fetching ──────────────────────────────────────
		case "napp.loadEvent":
			var p struct {
				Code   string   `json:"code"`
				Relays []string `json:"relays"`
				Author string   `json:"author"`
			}
			if err := json.Unmarshal([]byte(params), &p); err != nil {
				return nil, err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			evt := loadEvent(ctx, p.Code, p.Relays, p.Author)
			if evt == nil {
				return nil, nil
			}
			return *evt, nil

		case "napp.loadEvents":
			var ids []string
			if err := json.Unmarshal([]byte(params), &ids); err != nil {
				var p struct {
					IDs []string `json:"ids"`
				}
				if err := json.Unmarshal([]byte(params), &p); err != nil {
					return []nostr.Event{}, nil
				}
				ids = p.IDs
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			events := loadEventsByID(ctx, ids)
			if events == nil {
				events = []nostr.Event{}
			}
			return events, nil

		case "napp.verifyEvent":
			var evt nostr.Event
			if err := json.Unmarshal([]byte(params), &evt); err != nil {
				return false, nil
			}
			return evt.CheckID() && evt.VerifySignature(), nil

		// ─── throwaway keys ──────────────────────────────────────
		// env.d.ts describes these as running inside the napp's own frame,
		// but a webview here has no crypto companion to import: they run in
		// the host instead. The user's identity is still never involved, so
		// there's no prompt.
		case "napp.generateKey":
			sk := nostr.Generate()
			return map[string]any{"sk": sk.Hex(), "pk": sk.Public().Hex()}, nil

		case "napp.signWithKey":
			var p struct {
				Event nostr.Event `json:"event"`
				SK    string      `json:"sk"`
			}
			if err := json.Unmarshal([]byte(params), &p); err != nil {
				return nil, err
			}
			sk, err := nostr.SecretKeyFromHex(strings.TrimSpace(p.SK))
			if err != nil {
				return nil, errors.New("invalid secret key")
			}
			if p.Event.CreatedAt == 0 {
				p.Event.CreatedAt = nostr.Now()
			}
			if err := p.Event.Sign(sk); err != nil {
				return nil, err
			}
			return p.Event, nil

		// ─── files + clipboard ───────────────────────────────────
		case "napp.saveFile":
			var p struct {
				Name string `json:"name"`
				Data string `json:"data"` // base64
				Type string `json:"type"`
			}
			if err := json.Unmarshal([]byte(params), &p); err != nil {
				return nil, err
			}
			name := SanitizeFilename(p.Name)
			if !askApproval(ci, "save a file to your disk",
				"“"+name+"” goes to "+host.SaveFileTarget()+".", "") {
				return nil, errors.New("denied by the user")
			}
			return saveFileForNapp(p.Name, p.Data)

		case "napp.copyText":
			var p struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal([]byte(params), &p); err != nil {
				return nil, err
			}
			if len(p.Text) > maxCopyChars {
				return nil, errors.New("text is too long to copy")
			}
			if !askApproval(ci, "copy text to your clipboard",
				strconv.Itoa(len(p.Text))+" characters.", preview(p.Text, 120)) {
				return nil, errors.New("denied by the user")
			}
			return copyTextForNapp(p.Text)

		// ─── publishing ──────────────────────────────────────────
		case "napp.publish":
			var p struct {
				Event  nostr.Event `json:"event"`
				Relays []string    `json:"relays"`
			}
			if err := json.Unmarshal([]byte(params), &p); err != nil {
				return nil, err
			}
			return publishEvent(ci, p.Event, p.Relays)

		default:
			log.Warn().Str("method", method).Msg("unsupported rpc method")
			return nil, fmt.Errorf("unsupported method: %s", method)
		}
	}
}

// parseFilters accepts one filter or an array of them, as nostrdb does.
func parseFilters(raw json.RawMessage) ([]nostr.Filter, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil, nil
	}
	if strings.HasPrefix(trimmed, "[") {
		var filters []nostr.Filter
		if err := json.Unmarshal(raw, &filters); err != nil {
			return nil, err
		}
		return filters, nil
	}
	var filter nostr.Filter
	if err := json.Unmarshal(raw, &filter); err != nil {
		return nil, err
	}
	return []nostr.Filter{filter}, nil
}

// nip51ListKind marks events addressed to their author's relays, not to the
// inbox relays of people referenced by their p-tags.
func nip51ListKind(kind nostr.Kind) bool {
	switch kind {
	case 3,
		10000, 10001, 10002, 10003, 10004, 10005, 10006, 10007, 10008, 10009,
		10011, 10012, 10013, 10015, 10017, 10018, 10020, 10030, 10050, 10054,
		10063, 10064, 10101, 10102,
		30000, 30001, 30002, 30003, 30004, 30005, 30006, 30007, 30008, 30015,
		30030, 30063, 30267, 31924, 39089, 39092:
		return true
	default:
		return false
	}
}

// publishTargets is where an event goes when the napp didn't say: the
// author's write relays, plus the inbox relays of everyone p-tagged, plus —
// for a relay list — the indexers that are supposed to carry it.
func publishTargets(ctx context.Context, evt nostr.Event, requested []string) []string {
	targets := make([]string, 0, 8)

	for _, url := range requested {
		targets = append(targets, url)
	}
	if len(targets) > 0 {
		return targets
	}

	for _, url := range sys.FetchWriteRelays(ctx, evt.PubKey) {
		targets = append(targets, url)
	}

	if !nip51ListKind(evt.Kind) {
		for _, key := range []string{"p", "P"} {
			for tag := range evt.Tags.FindAll(key) {
				if len(tag) < 2 {
					continue
				}
				pk, err := nostr.PubKeyFromHex(tag[1])
				if err != nil {
					continue
				}
				for _, url := range sys.FetchInboxRelays(ctx, pk, 3) {
					targets = append(targets, url)
				}
			}
		}
	}

	if evt.Kind == 10002 {
		targets = append(targets, sys.RelayListRelays.URLs...)
	}

	return targets
}

// publishEvent stores the event locally, asks the user, publishes and reports
// per-relay outcomes in the PublishResult shape from env.d.ts.
func publishEvent(ci *Instance, evt nostr.Event, requested []string) (any, error) {
	if sys == nil {
		return nil, errors.New("system not ready")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	log.Println("gathering targets")

	targets := publishTargets(ctx, evt, requested)
	if len(targets) == 0 {
		return nil, errors.New("no relays to publish to")
	}

	log.Println("gathered targets: ", targets)

	if !askApproval(ci, "publish an event",
		fmt.Sprintf("Kind %d to %d relay(s): %s", evt.Kind, len(targets),
			preview(strings.Join(stripSchemes(targets), ", "), 160)),
		preview(evt.Content, 200)) {
		return nil, errors.New("denied by the user")
	}

	// keep it locally first, so the napp can query it back right away
	if _, err := sys.Store.ReplaceEvent(evt); err != nil {
		if err := sys.Store.SaveEvent(evt); err != nil {
			log.Warn().Err(err).Msg("failed to store published event locally")
		}
	}
	// and let the load* caches know they're stale for this kind+author
	invalidateList(evt.Kind, evt.PubKey)

	log.Info().Uint16("kind", uint16(evt.Kind)).Strs("relays", targets).Msg("publishing event")

	relayResults := make(map[string]any, len(targets))
	published, failed := 0, 0
	for res := range sys.Pool.PublishMany(ctx, targets, evt) {
		if res.Error != nil {
			failed++
			relayResults[res.RelayURL] = map[string]any{"ok": false, "error": res.Error.Error()}
			log.Warn().Str("relay", res.RelayURL).Err(res.Error).Msg("publish failed")
			continue
		}
		published++
		relayResults[res.RelayURL] = map[string]any{"ok": true}
		log.Debug().Str("relay", res.RelayURL).Msg("publish succeeded")
	}

	return map[string]any{
		"relays":    relayResults,
		"published": published,
		"failed":    failed,
	}, nil
}

func stripSchemes(urls []string) []string {
	out := make([]string, 0, len(urls))
	for _, u := range urls {
		out = append(out, strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(u, "wss://"), "ws://"), "/"))
	}
	return out
}

func shortPubkey(pk nostr.PubKey) string {
	hex := pk.Hex()
	return hex[:8] + "…"
}
