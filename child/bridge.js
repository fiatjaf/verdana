;(() => {
  // This runs inside every napp's webview and is the whole of what a napp can
  // see of the launcher: window.nostr, window.nostrdb and window.napp, as
  // described in env.d.ts. Everything that needs the network, the user's key
  // or the local eventstore is an rpc to the Go host; the bech32/hex helpers
  // below are pure, so they stay here and answer synchronously.

  const _rpc = window.__bridge_rpc
  const feedCallbacks = new Map()
  const actionHandlers = []
  let serial = 0

  function rpc(method, params) {
    return _rpc(method, params !== undefined ? JSON.stringify(params) : "null").then(result => {
      const val = typeof result === "string" ? JSON.parse(result) : result
      if (val && val.__bridge_error) throw new Error(val.__bridge_error)
      return val
    })
  }

  // ── host → napp hooks ───────────────────────────────────────────
  window.__bridge_feed_callback = function (callbackId, eventsJSON, synced) {
    const cb = feedCallbacks.get(callbackId)
    if (!cb) return
    let events = []
    try {
      events = JSON.parse(eventsJSON)
    } catch {}
    try {
      cb(events, !!synced)
    } catch (err) {
      console.error("[napp] feed callback threw", err)
    }
  }

  // Set while we push the history entry for a dispatched action, so the
  // reporting hook below doesn't echo the launcher's own action back at it.
  let fromHost = false

  window.__bridge_dispatch_action = function (id, name, payloadJSON, idx) {
    let payload = null
    try {
      payload = payloadJSON ? JSON.parse(payloadJSON) : null
    } catch {}

    // A handler was registered with registerAction(): it is the only way to
    // give the caller a result back.
    if (typeof idx === "number" && idx >= 0) {
      const fn = actionHandlers[idx] && actionHandlers[idx][1]
      if (fn) {
        Promise.resolve()
          .then(() => fn(name, payload))
          .then(
            result => rpc("napp.dispatchResult", { id, result: result === undefined ? null : result }),
            err => rpc("napp.dispatchResult", { id, error: String((err && err.message) || err) })
          )
          .catch(() => {})
      }
    }

    // Either way the action becomes a history entry, so napps can handle it
    // through popstate and participate in back/forward.
    const state = { action: { name, payload } }
    fromHost = true
    try {
      history.pushState(state, "", location.href)
      window.dispatchEvent(new PopStateEvent("popstate", { state }))
    } finally {
      fromHost = false
    }
  }

  window.__bridge_theme_change = function (theme, varsJSON) {
    document.documentElement.dataset.theme = theme
    if (!varsJSON) return
    try {
      const vars = JSON.parse(varsJSON)
      for (const key in vars) document.documentElement.style.setProperty("--" + key, vars[key])
    } catch {}
  }

  // A napp that navigates on its own pushes { action: { name, payload } }, and
  // the launcher takes it as the window's current action.
  const reportActionState = state => {
    if (fromHost) return
    const a = state && state.action
    if (!a || typeof a.name !== "string" || !a.name) return
    rpc("napp.actionState", { name: a.name, payload: a.payload === undefined ? null : a.payload }).catch(
      () => {}
    )
  }
  for (const method of ["pushState", "replaceState"]) {
    const original = history[method].bind(history)
    history[method] = function (state, ...rest) {
      const result = original(state, ...rest)
      reportActionState(state)
      return result
    }
  }
  window.addEventListener("popstate", e => reportActionState(e.state))

  // ── window.nostr (NIP-07) ───────────────────────────────────────
  window.nostr = {
    getPublicKey: () => rpc("getPublicKey"),
    signEvent: evt => rpc("signEvent", evt),
    nip04: {
      encrypt: (pubkey, plaintext) => rpc("nip04.encrypt", { pubkey, plaintext }),
      decrypt: (pubkey, ciphertext) => rpc("nip04.decrypt", { pubkey, ciphertext })
    },
    nip44: {
      encrypt: (pubkey, plaintext) => rpc("nip44.encrypt", { pubkey, plaintext }),
      decrypt: (pubkey, ciphertext) => rpc("nip44.decrypt", { pubkey, ciphertext })
    }
  }

  // ── window.nostrdb (NIP-DB draft) ───────────────────────────────
  window.nostrdb = {
    add: event => rpc("nostrdb.add", { event }),
    query: filters => rpc("nostrdb.query", { filters }),
    count: filters => rpc("nostrdb.count", { filters }),
    event: id => rpc("nostrdb.event", { id }),
    remove: ids => rpc("nostrdb.remove", { ids }),
    replaceable: (kind, author, identifier) =>
      rpc("nostrdb.replaceable", { kind, author, identifier }),
    supports: async () => []
  }

  // ── feeds ───────────────────────────────────────────────────────
  function feedRpc(method, params, callback) {
    if (typeof callback !== "function") throw new Error("no callback specified")
    const callbackId = serial++
    params.callbackId = callbackId
    feedCallbacks.set(callbackId, callback)
    rpc(method, params).catch(err => console.error("[napp] feed failed", err))
    return {
      close() {
        feedCallbacks.delete(callbackId)
        rpc("napp.feeds.cancel", { callbackId }).catch(() => {})
      }
    }
  }

  // ── bech32 / nip19 (sync, pure) ─────────────────────────────────
  const CHARSET = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"
  const CHARMAP = {}
  for (let i = 0; i < CHARSET.length; i++) CHARMAP[CHARSET[i]] = i
  const GEN = [0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3]

  function polymod(values) {
    let chk = 1
    for (let p = 0; p < values.length; p++) {
      const top = chk >>> 25
      chk = ((chk & 0x1ffffff) << 5) ^ values[p]
      for (let i = 0; i < 5; i++) if ((top >>> i) & 1) chk ^= GEN[i]
    }
    return chk >>> 0
  }
  function hrpExpand(hrp) {
    const out = []
    for (let i = 0; i < hrp.length; i++) out.push(hrp.charCodeAt(i) >>> 5)
    out.push(0)
    for (let i = 0; i < hrp.length; i++) out.push(hrp.charCodeAt(i) & 31)
    return out
  }
  function bech32Decode(str) {
    if (typeof str !== "string") return null
    if (str !== str.toLowerCase() && str !== str.toUpperCase()) return null
    const s = str.toLowerCase()
    const pos = s.lastIndexOf("1")
    if (pos < 1 || pos + 7 > s.length) return null
    const data = []
    for (let i = pos + 1; i < s.length; i++) {
      const v = CHARMAP[s[i]]
      if (v === undefined) return null
      data.push(v)
    }
    const hrp = s.slice(0, pos)
    if (polymod(hrpExpand(hrp).concat(data)) !== 1) return null
    return { hrp, words: data.slice(0, data.length - 6) }
  }
  function checksum(hrp, data) {
    const mod = polymod(hrpExpand(hrp).concat(data, [0, 0, 0, 0, 0, 0])) ^ 1
    const out = []
    for (let i = 0; i < 6; i++) out.push((mod >>> (5 * (5 - i))) & 31)
    return out
  }
  function bech32Encode(hrp, data) {
    const combined = data.concat(checksum(hrp, data))
    let s = hrp + "1"
    for (let i = 0; i < combined.length; i++) s += CHARSET[combined[i]]
    return s
  }
  function convertBits(data, from, to, pad) {
    let acc = 0
    let bits = 0
    const out = []
    const maxv = (1 << to) - 1
    for (let i = 0; i < data.length; i++) {
      const value = data[i]
      if (value < 0 || value >>> from) return null
      acc = ((acc << from) | value) >>> 0
      bits += from
      while (bits >= to) {
        bits -= to
        out.push((acc >>> bits) & maxv)
      }
    }
    if (pad) {
      if (bits > 0) out.push((acc << (to - bits)) & maxv)
    } else if (bits >= from || (acc << (to - bits)) & maxv) {
      return null
    }
    return out
  }
  const bytesToHex = bytes => {
    let s = ""
    for (let i = 0; i < bytes.length; i++) s += (bytes[i] & 255).toString(16).padStart(2, "0")
    return s
  }
  const hexToBytes = hex => {
    if (typeof hex !== "string" || hex.length % 2) throw new Error("invalid hex")
    const out = new Uint8Array(hex.length / 2)
    for (let i = 0; i < out.length; i++) out[i] = parseInt(hex.slice(i * 2, i * 2 + 2), 16)
    return out
  }
  const utf8Decode = b => new TextDecoder().decode(new Uint8Array(b))
  const utf8Encode = s => new TextEncoder().encode(s)
  const uint32be = b => ((b[0] << 24) | (b[1] << 16) | (b[2] << 8) | b[3]) >>> 0
  const be32 = n => new Uint8Array([(n >>> 24) & 255, (n >>> 16) & 255, (n >>> 8) & 255, n & 255])

  function parseTLV(bytes) {
    const result = {}
    let i = 0
    while (i + 1 < bytes.length) {
      const t = bytes[i++]
      const l = bytes[i++]
      if (i + l > bytes.length) break
      ;(result[t] = result[t] || []).push(bytes.slice(i, i + l))
      i += l
    }
    return result
  }
  function encodeTLV(entries) {
    const parts = []
    for (let e = 0; e < entries.length; e++) {
      const v = entries[e][1]
      parts.push(entries[e][0], v.length)
      for (let i = 0; i < v.length; i++) parts.push(v[i])
    }
    return parts
  }

  function nip19Decode(bech) {
    const dec = bech32Decode(bech)
    if (!dec) throw new Error("invalid bech32")
    const bytes = convertBits(dec.words, 5, 8, false)
    if (!bytes) throw new Error("invalid bech32 data")
    const type = dec.hrp
    if (type === "npub" || type === "note" || type === "nsec") {
      return { type, data: bytesToHex(bytes) }
    }
    const tlv = parseTLV(bytes)
    if (type === "nprofile") {
      if (!tlv[0]) throw new Error("nprofile missing pubkey")
      return {
        type,
        data: { pubkey: bytesToHex(tlv[0][0]), relays: (tlv[1] || []).map(utf8Decode) }
      }
    }
    if (type === "nevent") {
      if (!tlv[0]) throw new Error("nevent missing id")
      return {
        type,
        data: {
          id: bytesToHex(tlv[0][0]),
          relays: (tlv[1] || []).map(utf8Decode),
          author: tlv[2] ? bytesToHex(tlv[2][0]) : undefined,
          kind: tlv[3] ? uint32be(tlv[3][0]) : undefined
        }
      }
    }
    if (type === "naddr") {
      if (!tlv[0] || !tlv[2] || !tlv[3]) throw new Error("invalid naddr")
      return {
        type,
        data: {
          identifier: utf8Decode(tlv[0][0]),
          pubkey: bytesToHex(tlv[2][0]),
          kind: uint32be(tlv[3][0]),
          relays: (tlv[1] || []).map(utf8Decode)
        }
      }
    }
    throw new Error("unsupported prefix: " + type)
  }
  const encodeBytes = (hrp, bytes) => bech32Encode(hrp, convertBits(Array.from(bytes), 8, 5, true))
  const npubEncode = hex => encodeBytes("npub", hexToBytes(hex))
  const noteEncode = hex => encodeBytes("note", hexToBytes(hex))
  // nostr-tools emits TLV types in reverse order (3,2,1,0); match it so our
  // strings are byte-identical to the library's.
  function neventEncode(p) {
    const entries = []
    if (p.kind != null) entries.push([3, be32(p.kind)])
    if (p.author) entries.push([2, hexToBytes(p.author)])
    for (const r of p.relays || []) entries.push([1, utf8Encode(r)])
    entries.push([0, hexToBytes(p.id)])
    return encodeBytes("nevent", encodeTLV(entries))
  }
  function naddrEncode(p) {
    const entries = [
      [3, be32(p.kind)],
      [2, hexToBytes(p.pubkey)]
    ]
    for (const r of p.relays || []) entries.push([1, utf8Encode(r)])
    entries.push([0, utf8Encode(p.identifier || "")])
    return encodeBytes("naddr", encodeTLV(entries))
  }

  const isHex64 = s => typeof s === "string" && /^[0-9a-f]{64}$/i.test(s)
  function parseCoordinate(coord) {
    if (typeof coord !== "string") return null
    const a = coord.indexOf(":")
    const b = coord.indexOf(":", a + 1)
    if (a < 0 || b < 0) return null
    const kind = Number(coord.slice(0, a))
    const pubkey = coord.slice(a + 1, b)
    if (!Number.isInteger(kind) || !isHex64(pubkey)) return null
    return { kind, pubkey, identifier: coord.slice(b + 1) }
  }
  const formatCoordinate = c => `${c.kind}:${c.pubkey}:${c.identifier}`
  function satsFromBolt11(invoice) {
    if (typeof invoice !== "string") return null
    const s = invoice.toLowerCase().trim()
    const pos = s.lastIndexOf("1")
    if (pos < 0) return null
    const m = /^ln(?:bc|tbs?|bcrt|sb)(\d*)([munp]?)$/.exec(s.slice(0, pos))
    if (!m) return null
    if (!m[1]) return 0 // amountless invoice
    const factor = { m: 1e-3, u: 1e-6, n: 1e-9, p: 1e-12 }[m[2]] ?? 1
    return Math.round(parseInt(m[1], 10) * factor * 1e8)
  }

  // ── saveFile encoding ───────────────────────────────────────────
  // The rpc carries JSON, so bytes travel base64-encoded.
  async function toBase64(data) {
    let bytes
    if (data instanceof Blob) bytes = new Uint8Array(await data.arrayBuffer())
    else if (data instanceof ArrayBuffer) bytes = new Uint8Array(data)
    else if (ArrayBuffer.isView(data)) bytes = new Uint8Array(data.buffer, data.byteOffset, data.byteLength)
    else if (typeof data === "string") bytes = new TextEncoder().encode(data)
    else throw new Error("saveFile: data must be a Blob, ArrayBuffer or view")

    let binary = ""
    const chunk = 0x8000
    for (let i = 0; i < bytes.length; i += chunk) {
      binary += String.fromCharCode.apply(null, bytes.subarray(i, i + chunk))
    }
    return btoa(binary)
  }

  window.napp = {
    instance: window.name,

    // ── inter-app calling ───────────────────────────────────────
    // opts: { instance } routes straight into an already-open window.
    action: (name, payload, opts) =>
      rpc("napp.action", { name, payload: payload === undefined ? null : payload, options: opts || {} }),

    registerAction(pattern, fn) {
      if (typeof pattern !== "string" || !pattern) {
        throw new Error("window.napp.registerAction: pattern is required")
      }
      let idx
      if (typeof fn === "function") {
        idx = actionHandlers.length
        actionHandlers.push([pattern, fn])
      }
      rpc("napp.registerAction", { pattern, idx }).catch(err =>
        console.error("[napp] registerAction failed", err)
      )
    },

    close: () => {
      rpc("napp.close").catch(() => {})
    },

    link: url => {
      rpc("napp.link", String(url)).catch(err => console.error("[napp] link failed", err))
    },

    feeds: {
      profile: (pubkey, kinds, callback, opts) =>
        feedRpc("napp.feeds.profile", { pubkey, kinds, ...(opts || {}) }, callback),
      following: (source, kinds, callback, opts) =>
        feedRpc("napp.feeds.following", { source, kinds, ...(opts || {}) }, callback),
      inbox: (pubkey, kinds, callback, opts) =>
        feedRpc("napp.feeds.inbox", { pubkey, kinds, ...(opts || {}) }, callback),
      outbox: (pubkeys, kinds, callback, opts) =>
        feedRpc("napp.feeds.outbox", { pubkeys, kinds, ...(opts || {}) }, callback)
    },

    nip19: { decode: nip19Decode, npubEncode, noteEncode, neventEncode, naddrEncode },
    fx: { isHex64, parseCoordinate, formatCoordinate, satsFromBolt11 },

    utils: {
      // ── NIP-51 lists ─────────────────────────────
      loadRelayList: user => rpc("napp.loadRelayList", user),
      loadFollowsList: user => rpc("napp.loadFollowsList", user),
      loadMuteList: user => rpc("napp.loadMuteList", user),
      loadBookmarks: user => rpc("napp.loadBookmarks", user),
      loadPins: user => rpc("napp.loadPins", user),
      loadBlossomServers: user => rpc("napp.loadBlossomServers", user),
      loadEmojis: user => rpc("napp.loadEmojis", user),
      loadFavoriteRelays: user => rpc("napp.loadFavoriteRelays", user),
      loadBlockedRelays: user => rpc("napp.loadBlockedRelays", user),
      loadSearchRelays: user => rpc("napp.loadSearchRelays", user),
      loadDmRelays: user => rpc("napp.loadDmRelays", user),
      loadWikiAuthors: user => rpc("napp.loadWikiAuthors", user),
      loadWikiRelays: user => rpc("napp.loadWikiRelays", user),
      loadFavoriteFollowSets: user => rpc("napp.loadFavoriteFollowSets", user),
      loadFavoriteScrolls: user => rpc("napp.loadFavoriteScrolls", user),
      loadProfileBadges: user => rpc("napp.loadProfileBadges", user),
      loadSimpleGroups: user => rpc("napp.loadSimpleGroups", user),
      loadGitAuthors: user => rpc("napp.loadGitAuthors", user),
      loadGitRepositories: user => rpc("napp.loadGitRepositories", user),
      loadMediaFollows: user => rpc("napp.loadMediaFollows", user),
      loadFavoritePodcasts: user => rpc("napp.loadFavoritePodcasts", user),
      loadAuthoredPodcasts: user => rpc("napp.loadAuthoredPodcasts", user),

      // ── composite helpers ────────────────────────
      fetchFavoriteRelaysWithSets: user => rpc("napp.fetchFavoriteRelaysWithSets", user),
      fetchEmojisWithSets: user => rpc("napp.fetchEmojisWithSets", user),
      fetchFavoriteFollowSetsWithSets: user => rpc("napp.fetchFavoriteFollowSetsWithSets", user),

      // ── addressable sets ─────────────────────────
      loadFollowSets: user => rpc("napp.loadFollowSets", user),
      loadRelaySets: user => rpc("napp.loadRelaySets", user),
      loadEmojiSets: user => rpc("napp.loadEmojiSets", user),

      // ── relays ───────────────────────────────────
      loadRelayInfo: url => rpc("napp.loadRelayInfo", url),

      // ── metadata + search ────────────────────────
      loadNostrUser: request => rpc("napp.loadNostrUser", request),
      searchUserLocal: term => rpc("napp.searchUserLocal", String(term ?? "")),
      searchUser: term => rpc("napp.searchUser", String(term ?? "")),

      // ── event fetching ───────────────────────────
      loadEvent: (code, relays, author) => rpc("napp.loadEvent", { code, relays, author }),
      loadEvents: ids => rpc("napp.loadEvents", ids),
      verifyEvent: event => rpc("napp.verifyEvent", event),

      // ── throwaway keys ───────────────────────────
      // For ephemeral/anonymous identities, never the user's key — so no
      // permission prompt. (In this launcher the keypair is made by the host
      // process, which is the same trust domain as this webview.)
      generateKey: () => rpc("napp.generateKey"),
      signWithKey: (event, sk) => rpc("napp.signWithKey", { event, sk }),

      // ── files + clipboard ────────────────────────
      saveFile: async (name, data, type) =>
        rpc("napp.saveFile", { name: String(name), data: await toBase64(data), type }),
      copyText: text => rpc("napp.copyText", { text: String(text) }),

      // ── publishing ───────────────────────────────
      publish: (event, relays) => rpc("napp.publish", { event, relays })
    }
  }
})()
