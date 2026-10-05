# WASM napps

A napp is usually a folder of HTML, CSS and JavaScript that verdana puts in a
webview. A **wasm napp** is not: it ships one WebAssembly module, and verdana
runs that module itself in a window of its own and hands it a canvas to paint
into. There is no DOM, no iframe, no webview and no JavaScript in the picture.
The module draws pixels and the window shows them.

The same module runs in
[nostrapps](https://github.com/eskema/nostrapps), which runs it against a
`<canvas>` in the browser. The contract below is the same in both.

## Declaring one

A wasm napp is a directory with a `metadata.json` and an `app.wasm` beside
it, and no `index.html`:

```json
{
  "id": "wasm-egui",
  "title": "egui Canvas",
  "initial_size": { "width": 520, "height": 420 }
}
```

The module's name is fixed: always `/app.wasm`, in the folder, in the
manifest's `path` tags, and on disk after install. There is nothing to name
and no field for it. A folder with both an `index.html` and an `app.wasm` is
a page that happens to ship a wasm file, not a module that happens to ship a
page.

Published, the folder becomes a kind `35131` event: same manifest shape as a
napp (`path` tags, `title`, `icon`, `action`, `requires`, `initial_size`), no
extra tags. Kind decides what the app is; tags only describe it. Absent
`initial_size`, the window opens at 640×480.

## The contract

### Imports — verdana calling in

All five come from the module named `env`. A module imports only the ones it
uses.

| name | signature | |
| --- | --- | --- |
| `napp_canvas_width` | `() -> i32` | canvas width, in device pixels |
| `napp_canvas_height` | `() -> i32` | canvas height, in device pixels |
| `napp_now_ms` | `() -> f64` | milliseconds since the window opened |
| `napp_log` | `(offset: i32, length: i32) -> ()` | write one line to the launcher's log |
| `napp_call` | `(method_ptr, method_len, params_ptr, params_len: i32) -> i32` | call the launcher; see [Talking to the launcher](#talking-to-the-launcher) |

A napp asks for the size every frame rather than caching it, which is how a
window resize is something it notices on its own.

### Exports — the napp calling out

| name | signature | |
| --- | --- | --- |
| `napp_frame` | `(dt_ms: f64) -> i32` | paint one frame; non-zero means "call me again without being told to" |
| `napp_canvas_ptr` | `() -> i32` | byte offset of the canvas in the module's own memory |
| `napp_event` | `(kind, x, y, buttons, a, b) -> ()` | optional: one input event |
| `napp_alloc` | `(len: i32) -> i32` | room for a message the host is about to hand over; required with `napp_call` |
| `napp_receive` | `(kind, id, ptr, len: i32) -> ()` | one message from the host; required with `napp_call` |

`napp_frame` returning non-zero is how an animation keeps running and how a
click gets drawn on the frame after it. Returning zero parks the window until
something happens, so an idle window costs nothing — the window stops asking
Gio for frames until an event arrives.

`napp_event` kinds, with `x` and `y` in canvas pixels and `buttons` a mask of
`1` primary, `2` secondary, `4` middle:

| kind | |
| --- | --- |
| 0 | pointer moved |
| 1 | pointer pressed |
| 2 | pointer released |
| 3 | scroll, in `a` and `b` |

A module may leave `napp_event` out entirely if it only draws.

## The canvas

`napp_canvas_ptr()` is a byte offset into the module's own linear memory.
`width * height * 4` bytes live there:

- **RGBA8888**, four bytes per pixel: R, G, B, A.
- **Alpha premultiplied.** R, G and B are already multiplied by A.
- Rows **top-down**, and the stride is exactly `width * 4`: no padding.

That is Go's `image.RGBA` and egui's `Color32` byte for byte, which is where
most napp toolkits already live, and it is what Gio's texture upload wants.

The napp owns the pixels and the launcher reads them, out of the module's
memory and into a texture the window paints. That direction is not a choice: a
host cannot hand a wasm module a pointer to memory the module cannot address,
so "give the napp a canvas" is in practice "the napp keeps a canvas and the
host reads it".

## Talking to the launcher

Everything a web napp gets from `window.nostr`, `window.nostrdb` and
`window.napp` a wasm napp gets through one import:

```
id = napp_call(method_ptr, method_len, params_ptr, params_len)
```

`method` is a name from the table below and `params` its JSON, both utf-8 in
the module's own memory (empty params read as `null`). The host copies them
before returning, so the buffers are free again at once. It never blocks: it
returns a call id (always above zero; `0` means there was no host to call)
and the answer comes later, through `napp_receive`, under that id.

### Messages — `napp_receive(kind, id, ptr, len)`

The host asks `napp_alloc(len)` for room, writes the message there, and hands
it over; from then on the bytes are the napp's to free. An empty message comes
with `ptr` and `len` both `0` and no allocation. Messages are only ever
delivered between frames — never from inside `napp_call` or `napp_frame` — and
the frame after one always runs, so a napp parked on an idle canvas still sees
its answer at once.

| kind | `id` | bytes | |
| --- | --- | --- | --- |
| 0 result | the call's | JSON | what the call returned |
| 1 error | the call's | text | why the call failed (including "denied by the user") |
| 2 feed | the feed call's | `{"events": [...], "synced": bool}` | one batch from a feed |
| 3 action | the dispatch's | `{"name", "payload", "idx"}` | someone dispatched an action to this window |
| 4 storage | 0 | `{"op", "key", "value"}` | another window of this napp changed its storage |
| 5 theme | 0 | `{"name", "vars"}` | the user switched the launcher's theme |

### Methods

These are bridge.js's own rpc names, with the params bridge.js sends for each
function. Calls that would prompt a web napp (signing, encrypting, publishing,
links, files, clipboard) prompt a wasm napp too: the question covers the
window until it is answered, exactly like a web napp's overlay.

| method | params | answers | bridge.js |
| --- | --- | --- | --- |
| `getPublicKey` | — | hex pubkey | `nostr.getPublicKey()` |
| `signEvent` | event template | signed event | `nostr.signEvent(evt)` |
| `nip04.encrypt` / `nip44.encrypt` | `{pubkey, plaintext}` | ciphertext | `nostr.nip04/nip44.encrypt` |
| `nip04.decrypt` / `nip44.decrypt` | `{pubkey, ciphertext}` | plaintext | `nostr.nip04/nip44.decrypt` |
| `nostrdb.add` | `{event}` | bool | `nostrdb.add` |
| `nostrdb.query` | `{filters}` (one or an array) | events | `nostrdb.query` |
| `nostrdb.count` | `{filters}` | number | `nostrdb.count` |
| `nostrdb.event` | `{id}` | event or null | `nostrdb.event` |
| `nostrdb.remove` | `{ids}` | removed ids | `nostrdb.remove` |
| `nostrdb.replaceable` | `{kind, author, identifier?}` | event or null | `nostrdb.replaceable` |
| `nostrdb.supports` | — | `[]` | `nostrdb.supports` |
| `napp.instance` | — | instance id | `napp.instance` |
| `napp.theme` | — | `{name, vars}` | `window.__nappTheme` |
| `napp.action` | `{name, payload, options?}` | the handler's result | `napp.action` |
| `napp.registerAction` | `{pattern, idx?}` | null | `napp.registerAction` |
| `napp.dispatchResult` | `{id, result}` or `{id, error}` | null | (answering an action) |
| `napp.close` | — | null | `napp.close` |
| `napp.link` | url string | null | `napp.link` |
| `napp.feeds.profile` | `{pubkey, kinds, ...opts}` | null, then feed messages | `napp.feeds.profile` |
| `napp.feeds.following` | `{source, kinds, ...opts}` | 〃 | `napp.feeds.following` |
| `napp.feeds.inbox` | `{pubkey, kinds, ...opts}` | 〃 | `napp.feeds.inbox` |
| `napp.feeds.outbox` | `{pubkeys, kinds, ...opts}` | 〃 | `napp.feeds.outbox` |
| `napp.feeds.cancel` | `{callbackId}` | null | `feed.close()` |
| `napp.load*`, `napp.fetch*WithSets` | user (hex, npub, nprofile, nip05) | the list | `napp.utils.load*` / `fetch*` |
| `napp.loadRelayInfo` | url string | NIP-11 info | `napp.utils.loadRelayInfo` |
| `napp.loadNostrUser` | user, or `{pubkey, relays}` | profile | `napp.utils.loadNostrUser` |
| `napp.searchUser` / `napp.searchUserLocal` | term string | profiles | `napp.utils.searchUser*` |
| `napp.loadEvent` | `{code, relays?, author?}` | event or null | `napp.utils.loadEvent` |
| `napp.loadEvents` | ids | events | `napp.utils.loadEvents` |
| `napp.verifyEvent` | event | bool | `napp.utils.verifyEvent` |
| `napp.generateKey` | — | `{sk, pk}` | `napp.utils.generateKey` |
| `napp.signWithKey` | `{event, sk}` | signed event | `napp.utils.signWithKey` |
| `napp.saveFile` | `{name, data, type}`, data **base64** | null | `napp.utils.saveFile` |
| `napp.copyText` | `{text}` | null | `napp.utils.copyText` |
| `napp.publish` | `{event, relays?}` | `{relays, published, failed}` | `napp.utils.publish` |
| `napp.storageGet` | key string | value or null | `localStorage.getItem` |
| `napp.storageKeys` | — | keys, sorted | `localStorage.key` / `length` |
| `napp.storageSet` | `{key, value}` | null | `localStorage.setItem` |
| `napp.storageRemove` | `{key}` | null | `localStorage.removeItem` |
| `napp.storageClear` | — | null | `localStorage.clear` |
| `nip19.decode` | code string | `{type, data}` | `napp.nip19.decode` |
| `nip19.npubEncode` | hex string | bech32 | `napp.nip19.npubEncode` |
| `nip19.noteEncode` | hex id | nevent (alias for `neventEncode({id})`) | `napp.nip19.noteEncode` |
| `nip19.neventEncode` | `{id, relays?, author?, kind?}` | bech32 | `napp.nip19.neventEncode` |
| `nip19.naddrEncode` | `{identifier, pubkey, kind, relays?}` | bech32 | `napp.nip19.naddrEncode` |
| `fx.isHex64` | string | bool | `napp.fx.isHex64` |
| `fx.parseCoordinate` | string | `{kind, pubkey, identifier}` or null | `napp.fx.parseCoordinate` |
| `fx.formatCoordinate` | `{kind, pubkey, identifier}` | string | `napp.fx.formatCoordinate` |
| `fx.satsFromBolt11` | invoice string | sats or null | `napp.fx.satsFromBolt11` |

The `napp.storage*`, `nip19.*`, `fx.*`, `napp.instance`, `napp.theme` and
`nostrdb.supports` calls are answered by the window itself, without a round
trip, the way bridge.js answers them inside the page — but they still answer
through `napp_receive`, like everything else. Storage is the same 5MB store
per napp a web napp's `localStorage` is, shared by all its windows (web or
wasm), and a write over the quota fails with an error message.

### Feeds

A feed call's own id is its `callbackId`. The call answers `null` once the
subscription is up; the events then keep arriving as feed messages under that
same id, `synced` turning true once the relays have sent what they had stored.
`napp.feeds.cancel` with `{"callbackId": id}` ends it, and nothing more is
delivered for it after that.

### Actions

A wasm napp handles actions the way a web napp's `registerAction` handler
does:

1. `napp.registerAction` with `{"pattern": "view:1"}` once it is ready, for
   each pattern it declares in its manifest. `idx` is any number the napp
   wants handed back with the action (to tell its handlers apart), `0` if
   left out; `-1` says it wants to hear about the action but will not answer.
2. When another napp, the launcher or a shortcut dispatches the action, it
   arrives as an action message: `id` is the dispatch, and the bytes carry
   the action's `name`, its `payload` and that `idx`.
3. The napp answers with `napp.dispatchResult`, `{"id": <the dispatch id>,
   "result": ...}` or `{"id", "error": "..."}`, and whoever dispatched it gets
   the result. An id of `-1` means nobody is waiting, so there is nothing to
   answer.

Calling out is `napp.action`, the same as a web napp: its answer is the
handler's result.

## What a wasm napp does not have

**No WASI.** wazero is started with no `wasi_snapshot_preview1` module, no
filesystem, no socket and no clock. A module that imports anything beyond the
five above is refused before it starts, with what it asked for in the message
— a host cannot half-support a napp, because the parts it does support are the
parts nobody had to think about. Whatever a napp needs from the outside comes
through `napp_call`, which is where permissions live.

No history: a web napp's actions also become history entries it can go
back and forward through; a canvas has no history, so an action is only ever
the message.

Android has no wasm host yet and says so rather than opening a blank WebView.

## How it is put together

A wasm napp window is its own process, like a web napp's — a hung or trapping
module is its own crash, not the launcher's. The two shells are:

| | |
| --- | --- |
| `desktop/child` | the webview shell: serves the napp's files, loads the page |
| `desktop/wasmchild` | the canvas shell: runs the module, paints the canvas |

Both speak the same wire protocol (`backend.WireMsg`) over stdin/stdout, so the
backend does not know the difference — `backend.WindowSpec.Mode` is the only
thing that says which shell gets used.

`backend/wasm/abi.go` is the single place the names come from.

## Trying one

```
cd desktop && go build -o wasmchild/wasmchild ./wasmchild && go build -o verdana -tags 'dev,novulkan' && ./verdana
```

Then the Dev tab, "Load folder", and a napp folder with a `metadata.json`
and a built `app.wasm` in it. Reload re-reads the folder, so `just wasm` in
the napp's own directory and the window's reload button is the whole loop.

## Where the names live

`backend/wasm/abi.go`, with the reference above. `desktop/wasmchild/napp.go`
is the host side; `../napps/wasm-egui` is a working module that needs no GPU.
