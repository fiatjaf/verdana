package main

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"fiatjaf.com/nostr"
)

func bridgeRPC(ci *childInfo) func(string, string) (any, error) {
	return func(method string, params string) (any, error) {
		switch method {
		case "getPublicKey":
			return getPublicKey()
		case "signEvent":
			return signEvent(params)
		case "nip04.encrypt", "nip04.decrypt":
			return nip04crypt(method, params)
		case "nip44.encrypt", "nip44.decrypt":
			return nip44crypt(method, params)
		case "nostrdb.add":
			return nostrdbAdd(params)
		case "nostrdb.query":
			return nostrdbQuery(params)
		case "nostrdb.count":
			return nostrdbCount(params)
		case "nostrdb.event":
			return nostrdbEvent(params)
		case "nostrdb.replaceable":
			return nostrdbReplaceable(params)
		case "napp.action":
			return nappAction(params)
		case "napp.feeds.profile", "napp.feeds.following", "napp.feeds.inbox":
			return feedSubscribe(ci, method, params)
		case "napp.feeds.cancel":
			return feedCancel(ci, params)
		case "napp.loadBlossomServers":
			return emptyList(), nil
		case "napp.loadBookmarks":
			return emptyList(), nil
		case "napp.loadEmojis":
			return emptyList(), nil
		case "napp.loadFavoriteRelays":
			return emptyList(), nil
		case "napp.loadFavoriteScrolls":
			return emptyList(), nil
		case "napp.loadFollowsList":
			return loadFollowsList(params)
		case "napp.loadMuteList":
			return loadMuteList(params)
		case "napp.loadPins":
			return emptyList(), nil
		case "napp.loadRelayList":
			return emptyList(), nil
		case "napp.loadWikiAuthors":
			return emptyList(), nil
		case "napp.loadWikiRelays":
			return emptyList(), nil
		case "napp.loadEmojiSets":
			return emptySets(), nil
		case "napp.loadFollowPacks":
			return emptySets(), nil
		case "napp.loadFollowSets":
			return emptySets(), nil
		case "napp.loadRelaySets":
			return emptySets(), nil
		case "napp.loadRelayInfo":
			return loadRelayInfo(params)
		case "napp.loadNostrUser":
			return loadNostrUser(params)
		case "napp.loadEvent":
			return loadEvent(params)
		case "napp.publish":
			return publish(params)
		default:
			return nil, nil
		}
	}
}

func getPublicKey() (string, error) {
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
}

func signEvent(params string) (any, error) {
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
}

func nip04crypt(method string, params string) (string, error) {
	return "", nil
}

func nip44crypt(method string, params string) (string, error) {
	return "", nil
}

func nostrdbAdd(params string) (bool, error) {
	return true, nil
}

func nostrdbQuery(params string) (any, error) {
	return []any{}, nil
}

func nostrdbCount(params string) (int, error) {
	return 0, nil
}

func nostrdbEvent(params string) (any, error) {
	return nil, nil
}

func nostrdbReplaceable(params string) (any, error) {
	return nil, nil
}

func nappAction(params string) (any, error) {
	return nil, nil
}

func feedSubscribe(ci *childInfo, method string, params string) (any, error) {
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

func feedCancel(ci *childInfo, params string) (any, error) {
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
}

func emptyList() any {
	return []any{}
}

func emptySets() any {
	return map[string]any{}
}

func loadFollowsList(params string) (any, error) {
	var p struct {
		Pubkey string `json:"pubkey"`
	}
	json.Unmarshal([]byte(params), &p)
	if sys == nil || p.Pubkey == "" {
		return emptyList(), nil
	}
	pk, err := nostr.PubKeyFromHex(p.Pubkey)
	if err != nil {
		return emptyList(), nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	list := sys.FetchFollowList(ctx, pk)
	return list, nil
}

func loadMuteList(params string) (any, error) {
	var p struct {
		Pubkey string `json:"pubkey"`
	}
	json.Unmarshal([]byte(params), &p)
	if sys == nil || p.Pubkey == "" {
		return emptyList(), nil
	}
	pk, err := nostr.PubKeyFromHex(p.Pubkey)
	if err != nil {
		return emptyList(), nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	list := sys.FetchMuteList(ctx, pk)
	return list, nil
}

func loadRelayInfo(params string) (any, error) {
	return nil, nil
}

func loadNostrUser(params string) (any, error) {
	return nil, nil
}

func loadEvent(params string) (any, error) {
	return nil, nil
}

func publish(params string) (any, error) {
	return nil, nil
}

const bridgeJS = `;(() => {
const _rpc = window.__bridge_rpc;
const pending = new Map();
const feedCallbacks = new Map();
const actionHandlers = [];
let feedSerial = 0;

function rpc(method, params) {
  const id = 'rpc' + (feedSerial++);
  return new Promise((resolve, reject) => {
    pending.set(id, {resolve, reject});
    _rpc(method, params !== undefined ? JSON.stringify(params) : null).then(result => {
      const p = pending.get(id);
      if (!p) return;
      pending.delete(id);
      p.resolve(JSON.parse(result));
    }).catch(err => {
      const p = pending.get(id);
      if (!p) return;
      pending.delete(id);
      p.reject(err);
    });
  });
}

window.__bridge_feed_callback = function(callbackId, eventsJSON, synced) {
  const cb = feedCallbacks.get(callbackId);
  if (cb) cb(JSON.parse(eventsJSON), synced);
};

window.__bridge_dispatch_action = function(name, payloadJSON, idx) {
  if (typeof idx === 'number') {
    const fn = actionHandlers[idx]?.[1];
    if (fn) {
      Promise.resolve().then(() => fn(name, JSON.parse(payloadJSON))).catch(() => {});
    }
  }
  const state = { action: { name, payload: payloadJSON ? JSON.parse(payloadJSON) : null } };
  history.pushState(state, '', location.href);
  window.dispatchEvent(new PopStateEvent('popstate', { state }));
};

window.__bridge_theme_change = function(theme, varsJSON) {
  document.documentElement.dataset.theme = theme;
  if (varsJSON) {
    const vars = JSON.parse(varsJSON);
    for (const key in vars) {
      document.documentElement.style.setProperty('--' + key, vars[key]);
    }
  }
};

window.nostr = {
  getPublicKey: () => rpc('getPublicKey'),
  signEvent: evt => rpc('signEvent', evt),
  nip04: {
    encrypt: (pubkey, plaintext) => rpc('nip04.encrypt', {pubkey, plaintext}),
    decrypt: (pubkey, ciphertext) => rpc('nip04.decrypt', {pubkey, ciphertext})
  },
  nip44: {
    encrypt: (pubkey, plaintext) => rpc('nip44.encrypt', {pubkey, plaintext}),
    decrypt: (pubkey, ciphertext) => rpc('nip44.decrypt', {pubkey, ciphertext})
  }
};

window.nostrdb = {
  add: event => rpc('nostrdb.add', {event}),
  query: filters => rpc('nostrdb.query', {filters}),
  count: filters => rpc('nostrdb.count', {filters}),
  event: id => rpc('nostrdb.event', {id}),
  replaceable: (kind, author, identifier) => rpc('nostrdb.replaceable', {kind, author, identifier}),
  supports: async () => []
};

function feedRpc(method, params, callback) {
  if (!callback) throw new Error('no callback specified');
  const callbackId = feedSerial++;
  params.callbackId = callbackId;
  feedCallbacks.set(callbackId, callback);
  rpc(method, params);
  return {
    close() {
      feedCallbacks.delete(callbackId);
      rpc('napp.feeds.cancel', {callbackId}).catch(() => {});
    }
  };
}

let __pointer = {x: 0, y: 0};
window.addEventListener('pointermove', e => { __pointer = {x: e.clientX, y: e.clientY}; }, {passive: true});

window.napp = {
  instance: window.name,
  action: (name, payload, options) => rpc('napp.action', {name, payload, options, pointer: __pointer}),
  registerAction(pattern, fn) {
    if (typeof pattern !== 'string' || !pattern) throw new Error('pattern required');
    let idx;
    if (typeof fn === 'function') {
      idx = actionHandlers.length;
      actionHandlers.push([pattern, fn]);
    }
  },
  feeds: {
    profile: (pubkey, kinds, callback, opts) => feedRpc('napp.feeds.profile', {pubkey, kinds, ...opts}, callback),
    following: (source, kinds, callback, opts) => feedRpc('napp.feeds.following', {source, kinds, ...opts}, callback),
    inbox: (pubkey, kinds, callback, opts) => feedRpc('napp.feeds.inbox', {pubkey, kinds, ...opts}, callback)
  },
  utils: {
    loadBlossomServers: (pubkey, hints, refresh, def) => rpc('napp.loadBlossomServers', {pubkey, hints, refreshStyle: refresh, defaultItems: def}),
    loadBookmarks: (pubkey, hints, refresh, def) => rpc('napp.loadBookmarks', {pubkey, hints, refreshStyle: refresh, defaultItems: def}),
    loadEmojis: (pubkey, hints, refresh, def) => rpc('napp.loadEmojis', {pubkey, hints, refreshStyle: refresh, defaultItems: def}),
    loadFavoriteRelays: (pubkey, hints, refresh, def) => rpc('napp.loadFavoriteRelays', {pubkey, hints, refreshStyle: refresh, defaultItems: def}),
    loadFavoriteScrolls: (pubkey, hints, refresh, def) => rpc('napp.loadFavoriteScrolls', {pubkey, hints, refreshStyle: refresh, defaultItems: def}),
    loadFollowsList: (pubkey, hints, refresh, def) => rpc('napp.loadFollowsList', {pubkey, hints, refreshStyle: refresh, defaultItems: def}),
    loadMuteList: (pubkey, hints, refresh, def) => rpc('napp.loadMuteList', {pubkey, hints, refreshStyle: refresh, defaultItems: def}),
    loadPins: (pubkey, hints, refresh, def) => rpc('napp.loadPins', {pubkey, hints, refreshStyle: refresh, defaultItems: def}),
    loadRelayList: (pubkey, hints, refresh, def) => rpc('napp.loadRelayList', {pubkey, hints, refreshStyle: refresh, defaultItems: def}),
    loadWikiAuthors: (pubkey, hints, refresh, def) => rpc('napp.loadWikiAuthors', {pubkey, hints, refreshStyle: refresh, defaultItems: def}),
    loadWikiRelays: (pubkey, hints, refresh, def) => rpc('napp.loadWikiRelays', {pubkey, hints, refreshStyle: refresh, defaultItems: def}),
    loadEmojiSets: (pubkey, hints, force) => rpc('napp.loadEmojiSets', {pubkey, hints, forceUpdate: force}),
    loadFollowPacks: (pubkey, hints, force) => rpc('napp.loadFollowPacks', {pubkey, hints, forceUpdate: force}),
    loadFollowSets: (pubkey, hints, force) => rpc('napp.loadFollowSets', {pubkey, hints, forceUpdate: force}),
    loadRelaySets: (pubkey, hints, force) => rpc('napp.loadRelaySets', {pubkey, hints, forceUpdate: force}),
    loadRelayInfo: (url, refresh) => rpc('napp.loadRelayInfo', {url, refreshStyle: refresh}),
    loadNostrUser: req => rpc('napp.loadNostrUser', req),
    loadEvent: (code, relays, author) => rpc('napp.loadEvent', {code, relays, author}),
    publish: (event, relays) => rpc('napp.publish', {event, relays})
  }
};})();`
