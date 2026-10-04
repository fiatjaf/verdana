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

All four come from the module named `env`.

| name | signature | |
| --- | --- | --- |
| `napp_canvas_width` | `() -> i32` | canvas width, in device pixels |
| `napp_canvas_height` | `() -> i32` | canvas height, in device pixels |
| `napp_now_ms` | `() -> f64` | milliseconds since the window opened |
| `napp_log` | `(offset: i32, length: i32) -> ()` | write one line to the launcher's log |

A napp asks for the size every frame rather than caching it, which is how a
window resize is something it notices on its own.

### Exports — the napp calling out

| name | signature | |
| --- | --- | --- |
| `napp_frame` | `(dt_ms: f64) -> i32` | paint one frame; non-zero means "call me again without being told to" |
| `napp_canvas_ptr` | `() -> i32` | byte offset of the canvas in the module's own memory |
| `napp_event` | `(kind, x, y, buttons, a, b) -> ()` | optional: one input event |

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

## What a wasm napp does not have

**No WASI.** wazero is started with no `wasi_snapshot_preview1` module, no
filesystem, no socket and no clock. A module that imports anything beyond the
four above is refused before it starts, with what it asked for in the message
— a host cannot half-support a napp, because the parts it does support are the
parts nobody had to think about.

Also not there yet: `window.nostr`, `window.nostrdb`, `window.napp`, actions,
permissions and local storage. **The canvas is the whole surface for now.**
Those all belong to a web napp's bridge, and a bridge needs a page. A napp
that wants one goes through the launcher, which is where permissions live.

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
