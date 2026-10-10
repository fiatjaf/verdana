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

| name                 | signature                                                      |                                                                            |
| ---                  | ---                                                            | ---                                                                        |
| `napp_canvas_width`  | `() -> i32`                                                    | canvas width, in device pixels                                             |
| `napp_canvas_height` | `() -> i32`                                                    | canvas height, in device pixels                                            |
| `napp_now_ms`        | `() -> f64`                                                    | milliseconds since the window opened                                       |
| `napp_log`           | `(offset: i32, length: i32) -> ()`                             | write one line to the launcher's log                                       |
| `napp_call`          | `(method_ptr, method_len, params_ptr, params_len: i32) -> i32` | call the launcher; see [Talking to the launcher](#talking-to-the-launcher) |

A napp asks for the size every frame rather than caching it, which is how a
window resize is something it notices on its own.

### Exports — the napp calling out

| name              | signature                           |                                                                              |
| ---               | ---                                 | ---                                                                          |
| `napp_frame`      | `(dt_ms: f64) -> i32`               | paint one frame; non-zero means "call me again without being told to"        |
| `napp_canvas_ptr` | `() -> i32`                         | byte offset of the canvas in the module's own memory                         |
| `napp_event`      | `(kind, x, y, buttons, a, b) -> ()` | optional: one input event                                                    |
| `napp_alloc`      | `(len: i32) -> i32`                 | room for a message the host is about to hand over; required with `napp_call` |
| `napp_receive`    | `(kind, id, ptr, len: i32) -> ()`   | one message from the host; required with `napp_call`                         |

`napp_frame` returning non-zero is how an animation keeps running and how a
click gets drawn on the frame after it. Returning zero parks the window until
something happens, so an idle window costs nothing — the window stops asking
Gio for frames until an event arrives.

`napp_event` kinds. For the pointer ones `x` and `y` are canvas pixels and
`buttons` a mask of `1` primary, `2` secondary, `4` middle; for the keyboard
ones `x` is the key or the character and `buttons` the modifiers, a mask of
`1` shift, `2` ctrl, `4` alt, `8` meta (super or command):

| kind |                                                           |
| ---  | ---                                                       |
| 0    | pointer moved                                             |
| 1    | pointer pressed                                           |
| 2    | pointer released                                          |
| 3    | scroll, in `a` and `b`                                    |
| 4    | key down: the key in `x`; `a` is `1` when the key repeats |
| 5    | key up: the key in `x`                                    |
| 6    | text: one code point typed (or pasted), in `x`            |

A key is the code point of the character it types, lowercased (`a` for both
`a` and `A`, the shift being in `buttons`), or for a key that types nothing:

| key        | `x`        | key       | `x`        |
| ---        | ---        | ---       | ---        |
| Backspace  | `8`        | ArrowUp   | `0x110002` |
| Tab        | `9`        | ArrowDown | `0x110003` |
| Enter      | `13`       | Home      | `0x110004` |
| Escape     | `27`       | End       | `0x110005` |
| Delete     | `127`      | PageUp    | `0x110006` |
| ArrowLeft  | `0x110000` | PageDown  | `0x110007` |
| ArrowRight | `0x110001` |           |            |

Other keys (a modifier on its own, function keys) are not reported. A text
field wants the text events, which carry what was typed with the layout and
shift already applied (Gio's edit events); the key events are for shortcuts
and editing keys. The window gets the keyboard once it has been clicked, and
ctrl+v (command+v on macOS) reads the clipboard and sends what is in it as
text events, after the key events for the shortcut itself.

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

| kind           | `id`                 | bytes                               |                                                               |
| ---            | ---                  | ---                                 | ---                                                           |
| 0 result       | the call's           | JSON                                | what the call returned                                        |
| 1 error        | the call's           | text                                | why the call failed (including "denied by the user")          |
| 2 stored       | the subscribe call's | the event                           | stored: see [Store subscriptions](#store-subscriptions)       |
| 3 action       | the dispatch's       | `{"name", "payload", "idx"}`        | someone dispatched an action to this window                   |
| 4 storage      | 0                    | `{"op", "key", "value"}`            | another window of this napp changed its storage               |
| 5 theme        | 0                    | `{"name", "vars"}`                  | the user switched the launcher's theme                        |
| 6 subscription | the subscribe call's | `{"type", ...}`                     | news from a subscription; see [Subscriptions](#subscriptions) |
| 7 bitmap       | the image call's     | width, height, RGBA                 | a decoded image; see [The web](#the-web)                      |

### Methods

These are bridge.js's own rpc names, with the params bridge.js sends for each
function. Calls that would prompt a web napp (signing, encrypting, publishing,
links, files, clipboard) prompt a wasm napp too: the question covers the
window until it is answered, exactly like a web napp's overlay.

| method                                     | params                                      | answers                                 | bridge.js                      |
| ---                                        | ---                                         | ---                                     | ---                            |
| `getPublicKey`                             | —                                           | hex pubkey                              | `nostr.getPublicKey()`         |
| `signEvent`                                | event template                              | signed event                            | `nostr.signEvent(evt)`         |
| `nip04.encrypt` / `nip44.encrypt`          | `{pubkey, plaintext}`                       | ciphertext                              | `nostr.nip04/nip44.encrypt`    |
| `nip04.decrypt` / `nip44.decrypt`          | `{pubkey, ciphertext}`                      | plaintext                               | `nostr.nip04/nip44.decrypt`    |
| `nostrdb.add`                              | `{event}`                                   | bool                                    | `nostrdb.add`                  |
| `nostrdb.query`                            | `{filters}` (one or an array)               | events                                  | `nostrdb.query`                |
| `nostrdb.count`                            | `{filters}`                                 | number                                  | `nostrdb.count`                |
| `nostrdb.event`                            | `{id}`                                      | event or null                           | `nostrdb.event`                |
| `nostrdb.remove`                           | `{ids}`                                     | removed ids                             | `nostrdb.remove`               |
| `nostrdb.replaceable`                      | `{kind, author, identifier?}`               | event or null                           | `nostrdb.replaceable`          |
| `nostrdb.subscribe`                        | `{filters}` (one or an array)               | null, then stored messages              | `nostrdb.subscribe`            |
| `nostrdb.unsubscribe`                      | `{callbackId}`                              | null                                    | leaving the `for await` loop   |
| `nostrdb.supports`                         | —                                           | `[]`                                    | `nostrdb.supports`             |
| `napp.instance`                            | —                                           | instance id                             | `napp.instance`                |
| `napp.theme`                               | —                                           | `{name, vars}`                          | `window.__nappTheme`           |
| `napp.now`                                 | —                                           | unix time, seconds                      | `Date.now()` (no clock here)   |
| `napp.action`                              | `{name, payload, options?}`                 | the handler's result                    | `napp.action`                  |
| `napp.registerAction`                      | `{pattern, idx?}`                           | null                                    | `napp.registerAction`          |
| `napp.dispatchResult`                      | `{id, result}` or `{id, error}`             | null                                    | (answering an action)          |
| `napp.close`                               | —                                           | null                                    | `napp.close`                   |
| `napp.link`                                | url string                                  | null                                    | `napp.link`                    |
| `napp.sync`                                | `{authors, kinds, since?, until?, limit?}`  | `{success, newEvents, error?}`          | `napp.sync`                    |
| `napp.outbox`                              | `{filter, label?, maxEoseTimeout?}`         | null, then subscription messages        | `napp.outbox`                  |
| `napp.load*`, `napp.fetch*WithSets`        | user (hex, npub, nprofile, nip05)           | the list                                | `napp.utils.load*` / `fetch*`  |
| `napp.loadRelayInfo`                       | url string                                  | NIP-11 info                             | `napp.utils.loadRelayInfo`     |
| `napp.loadNostrUser`                       | user, or `{pubkey, relays}`                 | profile                                 | `napp.utils.loadNostrUser`     |
| `napp.searchUser` / `napp.searchUserLocal` | term string                                 | profiles                                | `napp.utils.searchUser*`       |
| `napp.loadEvent`                           | `{code, relays?, author?}`                  | event or null                           | `napp.utils.loadEvent`         |
| `napp.loadEvents`                          | ids                                         | events                                  | `napp.utils.loadEvents`        |
| `napp.verifyEvent`                         | event                                       | bool                                    | `napp.utils.verifyEvent`       |
| `napp.generateKey`                         | —                                           | `{sk, pk}`                              | `napp.utils.generateKey`       |
| `napp.signWithKey`                         | `{event, sk}`                               | signed event                            | `napp.utils.signWithKey`       |
| `napp.saveFile`                            | `{name, data, type}`, data **base64**       | null                                    | `napp.utils.saveFile`          |
| `napp.copyText`                            | `{text}`                                    | null                                    | `napp.utils.copyText`          |
| `napp.publish`                             | `{event, relays?}`                          | `{relays, published, failed}`           | `napp.utils.publish`           |
| `napp.subscribe`                           | `{relays, filter, label?, maxEoseTimeout?}` | null, then subscription messages        | `napp.utils.subscribe`         |
| `napp.unsubscribe`                         | `{callbackId}`                              | null                                    | the closer `subscribe` returns |
| `napp.storageGet`                          | key string                                  | value or null                           | `localStorage.getItem`         |
| `napp.storageKeys`                         | —                                           | keys, sorted                            | `localStorage.key` / `length`  |
| `napp.storageSet`                          | `{key, value}`                              | null                                    | `localStorage.setItem`         |
| `napp.storageRemove`                       | `{key}`                                     | null                                    | `localStorage.removeItem`      |
| `napp.storageClear`                        | —                                           | null                                    | `localStorage.clear`           |
| `nip19.decode`                             | code string                                 | `{type, data}`                          | `napp.nip19.decode`            |
| `nip19.npubEncode`                         | hex string                                  | bech32                                  | `napp.nip19.npubEncode`        |
| `nip19.noteEncode`                         | hex id                                      | nevent (alias for `neventEncode({id})`) | `napp.nip19.noteEncode`        |
| `nip19.neventEncode`                       | `{id, relays?, author?, kind?}`             | bech32                                  | `napp.nip19.neventEncode`      |
| `nip19.naddrEncode`                        | `{identifier, pubkey, kind, relays?}`       | bech32                                  | `napp.nip19.naddrEncode`       |
| `fx.isHex64`                               | string                                      | bool                                    | `napp.fx.isHex64`              |
| `fx.parseCoordinate`                       | string                                      | `{kind, pubkey, identifier}` or null    | `napp.fx.parseCoordinate`      |
| `fx.formatCoordinate`                      | `{kind, pubkey, identifier}`                | string                                  | `napp.fx.formatCoordinate`     |
| `fx.satsFromBolt11`                        | invoice string                              | sats or null                            | `napp.fx.satsFromBolt11`       |
| `http_fetch`                               | `{url, method?, headers?, body?}` or a url  | `{status, url, headers, body}`          | `fetch()`; see [The web](#the-web) |
| `http_fetch_image`                         | `{url, width?, height?, fit?}` or a url     | a bitmap message                        | see [The web](#the-web)        |

The `napp.storage*`, `nip19.*`, `fx.*`, `napp.instance`, `napp.theme`,
`napp.now` and `nostrdb.supports` calls are answered by the window itself, without a round
trip, the way bridge.js answers them inside the page — but they still answer
through `napp_receive`, like everything else. Storage is the same 5MB store
per napp a web napp's `localStorage` is, shared by all its windows (web or
wasm), and a write over the quota fails with an error message.

`signEvent` may leave `created_at` out: a wasm napp has no wall clock, so a
template without one is stamped with the time it is signed at. For the time
itself (a `since` for `napp.sync`, say), ask `napp.now`.

### Store subscriptions

`nostrdb.subscribe` holds `filters` open on the launcher's store. The call's
own id is its `callbackId`: it answers `null` once the subscription is up,
then every event saved to the store from then on that matches one of the
filters arrives as a stored message under that id, one by one — whatever
saved it: a `napp.sync`, a subscription, another napp. What was stored before
comes from `nostrdb.query`. `nostrdb.unsubscribe` with `{"callbackId": id}`
ends it, and nothing more is delivered for it after that.

### The web

A web napp has `fetch()`. A wasm napp has no sockets, so the window fetches
for it:

- `http_fetch` makes one request: `method` defaults to `GET`, `headers` is an
  object of strings and `body` a string (base64 bytes yourself if you need
  to send any). It answers `{"status", "url", "headers", "body"}`, with the
  header names lowercased and **`body` base64**, since JSON has no bytes. A
  non-2xx status is still an answer; only not getting one is an error.
- `http_fetch_image` fetches an image and decodes it (png, jpeg, gif, webp),
  so a napp can show a picture without a decoder in its module. It answers
  with a **bitmap message** (kind 7) under the call's id instead of a result:
  `width` and `height` as little-endian u32s, then `width * height * 4` bytes
  of RGBA, rows top-down, **alpha not premultiplied** — what iced's
  `image::Handle::from_rgba` and egui's `ColorImage::from_rgba_unmultiplied`
  take. With no `width`/`height` the image comes at its own size, scaled down
  to fit 1024×1024. With them, `fit: "contain"` (the default) scales it to fit
  inside, never up; `fit: "cover"` fills exactly `width`×`height` and crops
  what overhangs evenly — the square avatar out of any photo. Only one of the
  two sides is a square of that side. An animated image gives its first
  frame.

Both read at most 16MB and give up after 20 seconds. They run beside the
frame loop (`backend/wasmhost/http.go`), so a slow server never holds up a
frame. There is no CORS here, as there is none for a
web napp's page in verdana; in nostrapps the browser's rules apply.

### Subscriptions

`napp.subscribe` is a plain REQ with `filter` to exactly `relays`. Like a
store subscription, the call's own id is its `callbackId`: it answers `null` once the
subscription is up, then subscription messages arrive under that id:

1. `{"type": "eose", "events": [...]}` once, with every event that came before
   all the relays sent EOSE — or before `maxEoseTimeout` milliseconds (20000
   if left out), whichever is first.
2. `{"type": "event", "event": {...}}` for each new event after that, one by
   one.
3. `{"type": "closed", "reasons": {"<relay>": "<reason>"}}` if every relay
   ends it on its own (a CLOSED, or no connection to begin with). Nothing
   comes after this.

`label` is what the relays see the subscription id prefixed with,
`<author prefix>-<d tag>` by default. `napp.unsubscribe` with
`{"callbackId": id}` ends it, and nothing more is delivered for it after
that.

`napp.outbox` is the same, with the same messages, except that there are no
`relays`: `filter` must have `authors`, and goes to each author's write relays
(two per author, each relay asked only for its own authors).

### Actions

A wasm napp handles actions the way a web napp's `registerAction` handler
does:

1. `napp.registerAction` with `{"pattern": "view:1"}` once it is ready, for
   each pattern it declares in its manifest. `idx` is any number the napp
   wants handed back with the action (to tell its handlers apart), `0` if
   left out; `-1` says it wants to hear about the action but will not answer.
2. When another napp, the launcher or a shortcut dispatches the action, it
   arrives as an action message: `id` is the dispatch, and the bytes carry
   the action's `name`, its `payload` and that `idx`. A napp declaring
   `view` gets every `view:<kind>` action, named just `view`; only a napp
   declaring `view:<kind>` itself sees the kind in the name.
3. The napp answers with `napp.dispatchResult`, `{"id": <the dispatch id>,
   "result": ...}` or `{"id", "error": "..."}`, and whoever dispatched it gets
   the result. An id of `-1` means nobody is waiting, so there is nothing to
   answer.

Calling out is `napp.action`, the same as a web napp: its answer is the
handler's result.

## What a wasm napp does not have

**No WASI.** wazero is started with no `wasi_snapshot_preview1` module, no
filesystem, no socket (the web is `http_fetch`) and no clock. A module that imports anything beyond the
five above is refused before it starts, with what it asked for in the message
— a host cannot half-support a napp, because the parts it does support are the
parts nobody had to think about. Whatever a napp needs from the outside comes
through `napp_call`, which is where permissions live.

No history: a web napp's actions also become history entries it can go
back and forward through; a canvas has no history, so an action is only ever
the message.

## On Android

The same contract, with what a phone changes:

- **The canvas is in dp**, not device pixels: a napp has no idea of screen
  density, so at a phone's real resolution it would draw the size of a
  stamp. It gets the size a desktop window of the same dp would give it, and
  the canvas is scaled up to the screen.
- **A finger is a mouse** with its primary button held: move, press, drag,
  release, and then a move to `(-1, -1)`, so no hover is left behind. A mouse
  or touchpad plugged in hovers and scrolls as on the desktop.
- **Keys come from a hardware keyboard only.** There is no way yet for a napp
  to ask for the on-screen keyboard.
- **No compositor between frames**: `napp_frame` returning non-zero gets the
  next frame on the next vsync, never faster.
- The first open of a napp compiles its module, which takes seconds on a
  phone; the compiled code is kept (keyed on the module's bytes) and later
  opens skip it.

## How it is put together

`backend/wasmhost` is the host: the module (wazero, no WASI) and the bridge
that carries its `napp_call`s to the launcher and the answers back. It knows
nothing about windows. Around it:

|                     |                                                                       |
| ---                 | ---                                                                   |
| `desktop/child`     | the desktop webview shell: serves the napp's files, loads the page    |
| `desktop/wasmchild` | the desktop canvas shell: a Gio window running `wasmhost`             |
| `backend/mobile`    | on Android, runs `wasmhost` inside the launcher (`wasm.go`)           |
| `NappCanvas.kt`     | the Android window: shows the frames, passes the touches and keys on  |

On the desktop a wasm napp window is its own process, like a web napp's — a
hung or trapping module is its own crash, not the launcher's. Both desktop
shells speak the same wire protocol (`backend.WireMsg`) over stdin/stdout, so
the backend does not know the difference — `backend.WindowSpec.Mode` is the
only thing that says which shell gets used. On Android there is one process,
so the module runs on its window's own thread instead: a stuck napp freezes
its window, not the launcher, and a trap closes the window saying why.

`backend/wasm/abi.go` is the single place the names come from.

## Trying one

```
cd desktop && go build -o wasmchild/wasmchild ./wasmchild && go build -o verdana -tags 'dev,novulkan' && ./verdana
```

Then the Dev tab, "Load folder", and a napp folder with a `metadata.json`
and a built `app.wasm` in it. Reload re-reads the folder, so `just wasm` in
the napp's own directory and the window's reload button is the whole loop.

## Where the names live

`backend/wasm/abi.go`, with the reference above. `backend/wasmhost/napp.go`
is the host side; `../napps/wasm-egui` is a working module that needs no GPU.
