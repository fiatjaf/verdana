// Package wasm is the contract between a wasm napp and the launcher hosting
// it: a napp declared with the "wasm" mode ships one WebAssembly module
// instead of a page, and is handed a window with a canvas buffer to paint
// into. There is no DOM, no iframe and no JavaScript in the picture.
//
// The contract is deliberately tiny, because everything in it has to be
// implemented twice — once by the desktop host, which runs the module in a
// native window, and once by nostrapps, which runs it against a <canvas>.
// WASM.md documents it for napp authors; this file is the single place both
// hosts take the names from, so the two cannot drift.
//
// # The canvas
//
// The napp owns the pixels: it allocates one RGBA8888 buffer in its own
// linear memory and publishes where it lives. The host only reads it. That
// direction matters — a host cannot hand a wasm module a pointer to memory
// the module cannot address, so "the host gives the napp a canvas" is in
// practice "the napp keeps a canvas and the host reads it".
//
// Canvas bytes are width*height*4, four bytes per pixel as R, G, B, A with
// alpha premultiplied, rows running top-down and no padding, so the stride is
// exactly width*4. That is Go's image.RGBA and egui's Color32 byte for byte,
// which is where most napp toolkits already live.
package wasm

import "slices"

// The import module the host provides. A module may import any of the
// functions below from it, and nothing else.
const HostModule = "env"

// Imports: the host calling into the napp.
const (
	// ImportCanvasWidth returns the canvas width in device pixels. Called
	// once per frame, so a napp notices a window resize by comparing
	// against what it last laid out for.
	ImportCanvasWidth = "napp_canvas_width"

	// ImportCanvasHeight returns the canvas height in device pixels.
	ImportCanvasHeight = "napp_canvas_height"

	// ImportNowMs returns milliseconds since the window opened. A napp
	// that only needs "how long since the last frame" can use the argument
	// napp_frame is given instead.
	ImportNowMs = "napp_now_ms"

	// ImportLog writes one line to the launcher's log, so a napp that is
	// misbehaving is diagnosable without a console. The line is length bytes
	// at offset in the napp's own memory, which is how anything at all
	// crosses into wasm.
	ImportLog = "napp_log"

	// ImportCall is napp_call(method_ptr, method_len, params_ptr,
	// params_len) -> id: the wasm side of everything bridge.js gives a web
	// napp (window.nostr, window.nostrdb, window.napp). method is one of the
	// bridge's rpc names and params its JSON, both utf-8 in the napp's own
	// memory. It never blocks: it returns a call id at once (always > 0) and
	// the answer arrives later through ExportReceive as MsgResult or
	// MsgError carrying that id. A module importing this must export
	// ExportAlloc and ExportReceive, or there is nowhere to put the answer.
	ImportCall = "napp_call"
)

// Exports: the napp calling out to the host.
const (
	// ExportFrame advances the napp by dtMs and paints one frame into the
	// canvas. It returns non-zero while the napp wants to be called again
	// without waiting for input — that is how an animation keeps running —
	// and zero when it is done until something happens. The very first call
	// has dtMs 0.
	ExportFrame = "napp_frame"

	// ExportCanvasPtr returns the byte offset of the canvas inside the
	// module's own linear memory. The host reads width*height*4 bytes from
	// there after every frame, whatever napp_frame returned.
	ExportCanvasPtr = "napp_canvas_ptr"

	// ExportEvent is optional: napp_event(kind, x, y, buttons, a, b) hands
	// the napp one input event, where x and y are canvas pixels, buttons is
	// the mask below, and a and b mean whatever kind says. A host only calls
	// it when the module exports it, and only from between two frames, so a
	// napp can simply append to a queue and look at it when it next paints.
	ExportEvent = "napp_event"

	// ExportAlloc is napp_alloc(len) -> ptr: room for len bytes in the
	// napp's memory, which the host fills before handing it over through
	// ExportReceive. Ownership passes to the napp with the message, so the
	// napp frees it whenever it is done with it.
	ExportAlloc = "napp_alloc"

	// ExportReceive is napp_receive(kind, id, ptr, len): the host handing
	// the napp one message, len bytes at ptr (from napp_alloc; ptr is 0 when
	// len is). Like ExportEvent it is only ever called between two frames,
	// and the frame after it always runs.
	ExportReceive = "napp_receive"
)

// The messages a host hands over through ExportReceive.
const (
	// MsgResult answers the napp_call with this id; the bytes are its JSON
	// result.
	MsgResult int32 = iota

	// MsgError says the napp_call with this id failed; the bytes are the
	// error message, as plain text.
	MsgError

	// MsgFeed is one batch from the feed the napp_call with this id
	// started: {"events": [...], "synced": bool}. synced turns true once
	// the relays have sent everything stored, after which only live events
	// follow.
	MsgFeed

	// MsgAction is the outside world calling the napp: an action it
	// registered with napp.registerAction was dispatched to this window.
	// The bytes are {"name", "payload", "idx"}, idx being whatever the
	// napp registered the pattern with. The napp answers by calling
	// napp.dispatchResult with {"id": <this id>, "result"} or {"id",
	// "error"}; an id of -1 means nobody is waiting for an answer.
	MsgAction

	// MsgStorage says another window of the same napp changed its storage:
	// {"op": "set"|"remove"|"clear", "key", "value"}. This window's
	// storage already has the change by the time the napp hears of it.
	MsgStorage

	// MsgTheme says the user switched the launcher's theme: {"name",
	// "vars"}, the same thing napp.theme answers with.
	MsgTheme
)

// The input events a host reports through ExportEvent. Pointer coordinates
// are in canvas pixels.
const (
	EventPointerMove int32 = iota
	EventPointerDown
	EventPointerUp
	EventScroll
)

// Pointer buttons, as the bit mask both the down/up events and pointer-move's
// third argument carry.
const (
	ButtonPrimary   = 1
	ButtonSecondary = 2
	ButtonMiddle    = 4
)

// DefaultEntry is the module a wasm napp gets when its manifest does
// not name one: /app.wasm.
const DefaultEntry = "app.wasm"

// DefaultWidth and DefaultHeight are the canvas a wasm napp opens at when its
// initial_size says nothing useful. Smaller than a web napp's window on
// purpose: a canvas napp is its own window's whole content, so there is no
// page chrome to make room for.
const (
	DefaultWidth  = 640
	DefaultHeight = 480
)

// BytesPerPixel is the canvas stride per pixel: RGBA, four bytes.
const BytesPerPixel = 4

// CanvasBytes is how much room a width x height canvas takes.
func CanvasBytes(width, height int) int {
	return width * height * BytesPerPixel
}

// Import is one thing a module wants from the host: a module name and a
// function name, as reported by a WebAssembly.Module's import list.
type Import struct {
	Module string
	Name   string
}

// NeededImports are the imports a host provides from HostModule, which are
// also the only ones a wasm napp may have.
func NeededImports() []Import {
	return []Import{
		{HostModule, ImportCanvasWidth},
		{HostModule, ImportCanvasHeight},
		{HostModule, ImportNowMs},
		{HostModule, ImportLog},
		{HostModule, ImportCall},
	}
}

// MissingImports lists what a module imports that no host provides, so
// refusing one can say what was wrong instead of just failing. The napp's own
// extra exports are fine — only imports count, and those are capabilities.
func MissingImports(imports []Import) []string {
	provided := make(map[Import]bool, len(NeededImports()))
	for _, imp := range NeededImports() {
		provided[imp] = true
	}
	var missing []string
	for _, imp := range imports {
		if provided[imp] || slices.Contains(missing, imp.String()) {
			continue
		}
		missing = append(missing, imp.String())
	}
	return missing
}

func (i Import) String() string {
	if i.Module == "" {
		return i.Name
	}
	return i.Module + "." + i.Name
}
