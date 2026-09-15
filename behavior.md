## For developers

The idea is that each napp is a very small, specialized app. It should do one (or few) things and do them well. It should call `window.napp.registerAction()` in order to receive the parameters it will use (for example, an app that displays any information related to a profile should call that to register the `"profile"` action) and it should call `window.napp.action()` for anything it doesn't handle internally (for example, an app that displays a list of notes but doesn't handle threads or an expanded view of such notes should call out to other apps with the `view:1` action).

A napp is any folder with an `index.html`. Inside the iframe you get:

```js
// NIP-07 signer, mediated by the launcher. Present only when `identity` is
// granted (see Permissions below). getPublicKey() answers from the cached
// account key and never prompts.
window.nostr.getPublicKey()
window.nostr.signEvent(evt)
window.nostr.nip04.encrypt|decrypt(pubkey, text)
window.nostr.nip44.encrypt|decrypt(pubkey, text)

// Global event store (NIP-DB draft)
window.nostrdb.add(event)
window.nostrdb.query(filters)
window.nostrdb.count(filters)
window.nostrdb.event(id)
window.nostrdb.remove(ids) // string[]
window.nostrdb.replaceable(kind, author, identifier?)
window.nostrdb.supports() // returns []

// NIP-51 list loaders — accepts hex pubkey, npub, or nprofile
window.napp.utils.loadRelayList(pubkey: string): Promise<ListResult<RelayItem>>
window.napp.utils.loadFollowsList(pubkey)
window.napp.utils.loadMuteList(pubkey)
window.napp.utils.loadBookmarks(pubkey)
window.napp.utils.loadPins(pubkey)
window.napp.utils.loadBlossomServers(pubkey)
window.napp.utils.loadEmojis(pubkey)
window.napp.utils.loadFavoriteRelays(pubkey)
window.napp.utils.loadBlockedRelays(pubkey) // kind 10006
window.napp.utils.loadSearchRelays(pubkey) // kind 10007
window.napp.utils.loadDmRelays(pubkey) // kind 10050, NIP-17
window.napp.utils.loadWikiAuthors(pubkey)
window.napp.utils.loadWikiRelays(pubkey)
window.napp.utils.loadFavoriteFollowSets(pubkey) // kind 10021
window.napp.utils.loadFavoriteScrolls(pubkey) // kind 10027
window.napp.utils.loadProfileBadges(pubkey) // kind 10008
window.napp.utils.loadSimpleGroups(pubkey) // kind 10009
window.napp.utils.loadGitAuthors(pubkey) // kind 10017
window.napp.utils.loadGitRepositories(pubkey) // kind 10018
window.napp.utils.loadMediaFollows(pubkey) // kind 10020
window.napp.utils.loadFavoritePodcasts(pubkey) // kind 10054
window.napp.utils.loadAuthoredPodcasts(pubkey) // kind 10064

// Composite helpers resolving address-pointer items into their sets
window.napp.utils.fetchFavoriteRelaysWithSets(pubkey)
window.napp.utils.fetchEmojisWithSets(pubkey)
window.napp.utils.fetchFavoriteFollowSetsWithSets(pubkey)

// Addressable sets
window.napp.utils.loadFollowSets(pubkey)
window.napp.utils.loadRelaySets(pubkey)
window.napp.utils.loadEmojiSets(pubkey)

// Relay metadata
window.napp.utils.loadRelayInfo(url)

// Profile metadata
window.napp.utils.loadNostrUser(request) // NostrUserRequest | string → NostrUser

// User search (returns NostrUser[])
window.napp.utils.searchUserLocal(term) // local full-text search over profiles known to the launcher
window.napp.utils.searchUser(term) // remote NIP-50 kind:0 search on the user's search relays (or defaults)

// Arbitrary event fetching
window.napp.utils.loadEvent(code, relays?, author?)

// Batched by-id fetching: one REQ over the id union
window.napp.utils.loadEvents(ids)

// Verify an event's id + signature on the host (nostr-tools verifyEvent).
window.napp.utils.verifyEvent(event)

// Throwaway-key signing for ephemeral/anonymous identities (no rpc — runs in
// the napp's own frame, secret key never reaches the host, no prompt)
window.napp.utils.generateKey() // → { sk, pk } fresh secp256k1 keypair, hex
window.napp.utils.signWithKey(event, sk)

// Saving a file to disk (the sandbox blocks <a download>; prompts the user)
window.napp.utils.saveFile(name, data, type?)
//   data: Blob | ArrayBuffer | ArrayBufferView — prefer Blob (clones by reference)
//   returns { name, size } — name reduced to a basename

// Copying text to the clipboard (navigator.clipboard rejects in the sandbox;
// prompts the user with a preview of the text)
window.napp.utils.copyText(text)
//   text: string, max 100k chars
//   returns { length }

// Publishing
window.napp.utils.publish(event, relays?)
//   event: NostrEvent (must be signed)
//   relays?: string[] — if omitted, publishes to the author's write relays
//     (for kind 10002 also publishes to fallback + indexer relays)
//   returns { relays: {[url]: { ok, error? }}, published, failed }
//
//   For kinds handled by load* methods (NIP-51 lists, addressable sets, contacts),
//   publish() also updates the local cache with the published event so subsequent
//   load* calls reflect the change immediately without re-fetching from relays.

// Sync helpers (no rpc round-trip)
window.napp.nip19.decode(bech) // npub/note/nsec/nprofile/nevent/naddr
window.napp.nip19.npubEncode|noteEncode|neventEncode|naddrEncode(...)
window.napp.fx.isHex64(s)
window.napp.fx.parseCoordinate("kind:pubkey:d") // → { kind, pubkey, identifier } | null
window.napp.fx.formatCoordinate({ kind, pubkey, identifier })
window.napp.fx.satsFromBolt11(invoice)

// Opening external links
window.napp.link(url)
```

### `metadata.json`

A napp folder carries a `metadata.json` next to its `index.html`. The launcher reads it directly on the dev/local paths, and the uploader turns it into manifest tags when you publish — so the same file describes the napp in both places.

```json
{
  "id": "relays",
  "title": "Relays",
  "icon": "/icon.svg",
  "description": "Edit your relay lists",
  "singleton": true,
  "requires": ["ui"],
  "actions": ["profile", "view:0"]
}
```

| Field         | Published as         | Meaning                                                                           |
| ------------- | -------------------- | --------------------------------------------------------------------------------- |
| `id`          | `["d", …]`           | Required. The napp's identifier, and the basis of its origin.                     |
| `title`       | `["title", …]`       | Display name.                                                                     |
| `icon`        | `["icon", …]`        | Icon URL or path.                                                                 |
| `description` | `["description", …]` | One line, shown on the app's card and detail view.                                |
| `singleton`   | `["singleton"]`      | One window at a time — see below.                                                 |
| `requires`    | `["requires", …]`    | Capability domains ([Permissions](#permissions), [Shared UI](#shared-ui-opt-in)). |
| `actions`     | `["action", …]`      | Action patterns the napp `registerAction()`s.                                     |

Declaring `actions` or `requires` is also what makes the launcher call your app a **napp** rather than a plain **nsite**: an app that declares neither is a static site, shown and launched as one. A pure-UI napp with no handlers still qualifies via `"requires": ["ui"]`.

**`singleton`** — with `"singleton": true`, launching an app that's already open surfaces the existing window (adopting it into the space you're in) instead of opening a second one, and its `window.napp.instance` is a stable string rather than a per-window serial. Leave it out for napps that are useful several at a time — a feed reader, a note viewer — and set it for the ones that edit a single piece of your state, like a relay-list editor.

### Streaming feeds

Napps can subscribe to live event streams. Each returns a handle with `.close()`:

```js
window.napp.feeds.profile(pubkey, kinds, callback, { since?, until?, limit? })
window.napp.feeds.following(source, kinds, callback, { since?, until?, limit? })
window.napp.feeds.inbox(pubkey, kinds, callback, { since?, until?, limit? })
```

`callback` will be called with `callback(events: NostrEvent[], synced: boolean)`.

### Registering action handlers

Napps can expose handlers for other windows to call:

```js
window.napp.registerAction(pattern, handler?)
// handler(name, payload) -> result
```

`pattern` is an exact match, with one special case: `"view"` matches all `"view:<any-number>"` actions.

The napp can omit the `handler` and opt into only handling actions via the `popstate` event. The host pushes history entries with `state: { action: { name, payload } }` — listen for `popstate` and read `event.state.action`. This lets actions participate in browser back/forward navigation.

The same shape works the other way. When the napp navigates on its own — to another profile, say — push where it went: `history.pushState({ action: { name: "profile", payload: pubkey } }, "")`. The launcher takes it as the window's current `profile` action, so it comes back on restore and goes into a share link; back and forward report the entry landed on. Use `replaceState` when the step shouldn't be a back stop.

There is restriction of what actions are allowed, but these are some of the common ones:

| Action               | Payload                             | Returns                         |
| -------------------- | ----------------------------------- | ------------------------------- |
| `view`               | `nevent/naddr` **or** full event    |                                 |
| `view:<kind-number>` | full event object (always resolved) |                                 |
| `profile`            | `pubkey` as hex                     |                                 |
| `feed`               | list of pubkey strings              |                                 |
| `relay`              | list of relay URLs                  |                                 |
| `wiki-term`          | a "d"-tag NIP-54 normalized string  | the resolved `kind:30818` event |

Apps registering `"view"` (generic, no number) may receive either a nip19 code string or a resolved event object and must handle both. Apps registering a specific `"view:<kind-number>"` always receive a resolved event object. The sender may pass the event object, an `nevent`/`naddr`, or the event as a JSON string; the launcher resolves it.

Optionally `{ instance: "<instanceId>" }` as the third argument to route the action directly to a specific running instance instead of launching a new one.

Each napp also gets its instance id at `window.napp.instance` (a string, unique per window — or the napp's own id, stable across launches, when it declares `singleton`).

TypeScript types for everything above live in [`env.d.ts`](./env.d.ts). Reference it in your napp's `tsconfig.json` or copy it as a starting point.

The host also pushes runtime signals to every napp via `postMessage`. bridge.js relays them:

- **`napp-theme-change`**: sets `data-theme` (`"light"`/`"dark"`) on `<html>` and the launcher's `--surface`/`--text` tokens on `:root`, so napps using them track the theme automatically. To read the colors programmatically, declare `theme` in `requires` and use `window.napplet.theme` (see below).

### Shared UI (opt-in)

A napp can adopt the launcher's design system (buttons, inputs, disclosures, checkboxes, icons) by declaring the `ui` capability in its `requires`:

```json
{ "requires": ["ui"] }
```

`ui` is auto-granted (never a permission toggle) and implies `theme`. The service worker injects `<link rel="stylesheet" href="/napp-ui.css">` before your own styles, so you can override anything. It provides `.btn` (+ `-primary`/`-outline`/`-danger`/`-warning`/`-ghost`/`-link`), `.ui-input`, `.ui-details`, `.ui-check`, and `.ui-icon-*`, with fonts and icons inlined and `--surface`/`--text` tracking the theme. Napps that don't declare `ui` are unaffected. (The retired `"ui": "wrapper"` field is still honored for apps published before the migration — don't use it in new napps.)

### Permissions

Before an app first runs, the launcher shows what it declared; the grants are stored and enforced per call. Two launcher-local capabilities join the NAP domains:

- `identity`: `window.nostr`. When denied the signer is gone, pinned so an extension can't re-inject it.
- `network`: the app's own direct connections, on by default for nsites. Not relay access: nostr always flows through the bridge, which works sealed. When denied the app is served under a locked CSP (`default-src 'self'`, and `worker-src 'none'` since workers have their own network) and any service worker it registered is unregistered.

Declared domains the launcher doesn't implement are shown as non-grantable, so it's visible what won't work. Sensitive calls (`signEvent`, `nip04`/`nip44`, `saveFile`, `copyText`) still prompt per call. App data is only cleared on uninstall.

### Origin sandboxing

Each napp runs at its own origin (a unique `<nappId>` subdomain). From the iframe, `window.parent` is cross-origin, so the napp can't reach into the launcher. The bridge is the only channel. True napplets don't get an origin at all; their only persistence is the `storage` domain.

### Boot flow

1. The launcher opens a hidden iframe at `<napp-origin>/boot.html`.
2. That iframe registers a service worker and writes the napp's files to its origin's IndexedDB via `postMessage`.
3. The launcher creates the visible iframe at `<napp-origin>/`. The service worker serves the HTML and assets out of IDB.
4. The bridge picks up its `instanceId` from `window.name` (set by the parent before the iframe loads) and starts forwarding RPC.

`window.name` survives same-origin navigations, so reloading an iframe (during an update, for example) keeps the same instance id and per-instance state.
