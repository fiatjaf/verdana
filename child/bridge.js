;(() => {
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
      console.log('got result:', result, typeof result)
      const val = (typeof result === 'string') ? JSON.parse(result) : result;
      if (val && val.__bridge_error) { p.reject(new Error(val.__bridge_error)); return; }
      p.resolve(val);
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
    loadBlossomServers: pubkey => rpc('napp.loadBlossomServers', String(pubkey)),
    loadBookmarks: pubkey => rpc('napp.loadBookmarks', String(pubkey)),
    loadEmojis: pubkey => rpc('napp.loadEmojis', String(pubkey)),
    loadFavoriteRelays: pubkey => rpc('napp.loadFavoriteRelays', String(pubkey)),
    loadFollowsList: pubkey => rpc('napp.loadFollowsList', String(pubkey)),
    loadMuteList: pubkey => rpc('napp.loadMuteList', String(pubkey)),
    loadPins: pubkey => rpc('napp.loadPins', String(pubkey)),
    loadRelayList: pubkey => rpc('napp.loadRelayList', String(pubkey)),
    loadWikiAuthors: pubkey => rpc('napp.loadWikiAuthors', String(pubkey)),
    loadWikiRelays: pubkey => rpc('napp.loadWikiRelays', String(pubkey)),
    loadEmojiSets: pubkey => rpc('napp.loadEmojiSets', String(pubkey)),
    loadFollowSets: pubkey => rpc('napp.loadFollowSets', String(pubkey)),
    loadRelaySets: pubkey => rpc('napp.loadRelaySets', String(pubkey)),
    loadRelayInfo: (url, refresh) => rpc('napp.loadRelayInfo', {url, refreshStyle: refresh}),
    loadNostrUser: ref => rpc('napp.loadNostrUser', typeof ref === 'string' ? ref : ref.pubkey),
    loadEvent: (code, relays, author) => rpc('napp.loadEvent', {code, relays, author}),
    publish: (event, relays) => rpc('napp.publish', {event, relays})
  }
};})();
