package mobile

import (
	"errors"
	"sync"
	"time"

	"verdana/backend"
	"verdana/backend/wasm"
	"verdana/backend/wasmhost"

	"github.com/puzpuzpuz/xsync/v3"
)

// The ABI's input events and the masks they carry, for the View to build
// WasmInput calls from (see verdana/backend/wasm for what each one means).
const (
	EventPointerMove = int(wasm.EventPointerMove)
	EventPointerDown = int(wasm.EventPointerDown)
	EventPointerUp   = int(wasm.EventPointerUp)
	EventScroll      = int(wasm.EventScroll)
	EventKeyDown     = int(wasm.EventKeyDown)
	EventKeyUp       = int(wasm.EventKeyUp)

	ButtonPrimary = wasm.ButtonPrimary

	ModShift = wasm.ModShift
	ModCtrl  = wasm.ModCtrl
	ModAlt   = wasm.ModAlt
	ModMeta  = wasm.ModMeta

	KeyBackspace  = wasm.KeyBackspace
	KeyTab        = wasm.KeyTab
	KeyEnter      = wasm.KeyEnter
	KeyEscape     = wasm.KeyEscape
	KeyDelete     = wasm.KeyDelete
	KeyArrowLeft  = wasm.KeyArrowLeft
	KeyArrowRight = wasm.KeyArrowRight
	KeyArrowUp    = wasm.KeyArrowUp
	KeyArrowDown  = wasm.KeyArrowDown
	KeyHome       = wasm.KeyHome
	KeyEnd        = wasm.KeyEnd
	KeyPageUp     = wasm.KeyPageUp
	KeyPageDown   = wasm.KeyPageDown
)

// A wasm napp on the phone runs right here, inside the launcher's process:
// there is no process per window to give it, and nothing in Kotlin that could
// run a module anyway. The module and its bridge are the same ones the
// desktop's canvas shell uses (wasmhost); what the desktop shell does with a
// Gio window, the Android window does with a View that asks WasmFrame for
// pixels and hands its touches to WasmInput.
//
// The backend never learns the difference: the window's Transport feeds the
// bridge directly, and what the bridge sends up goes straight to
// backend.HandleMessage, with no JSON round trip through Kotlin.

type wasmWindow struct {
	instance string
	ui       UI
	br       *wasmhost.Bridge
	started  time.Time

	// mu is held around everything that runs the module: frames, input,
	// messages. A module is single-threaded and the View calls in from its
	// own render thread while touches come from the main one.
	mu      sync.Mutex
	n       *wasmhost.Napp
	loadErr error
	closed  bool
	last    time.Time
}

var wasmWindows = xsync.NewMapOf[string, *wasmWindow]()

func lookupWasm(instance string) *wasmWindow {
	w, _ := wasmWindows.Load(instance)
	return w
}

// openWasm starts the module for a wasm napp's window. It returns before the
// module is compiled — that takes a moment, and the window's first frames
// just come back empty until it lands.
func openWasm(ui UI, spec backend.WindowSpec) *wasmWindow {
	w := &wasmWindow{instance: spec.Instance, ui: ui, started: time.Now()}
	w.br = wasmhost.NewBridge(wasmhost.Config{
		Instance:  spec.Instance,
		Storage:   []byte(spec.StorageJSON),
		Theme:     spec.Theme,
		ThemeVars: spec.ThemeVars,
		Send: func(m wasmhost.WireMsg) {
			backend.HandleMessage(spec.Instance, backend.WireMsg{
				T: m.T, ID: m.ID, Method: m.Method, Params: m.Params,
				Result: m.Result, Error: m.Error, Code: m.Code, Idx: m.Idx,
			})
		},
	})

	wasmWindows.Store(spec.Instance, w)

	go func() {
		n, err := wasmhost.Load(spec.Wasm, w.nowMS, w.br.Call)
		w.mu.Lock()
		if w.closed {
			// closed while it was compiling
			if n != nil {
				n.Close()
			}
			w.mu.Unlock()
			return
		}
		w.n, w.loadErr = n, err
		w.mu.Unlock()
		// whatever the launcher sends wakes the window, so a napp parked on
		// an idle canvas still hears its answers on the frame right after
		w.br.SetWake(w.invalidate)
		w.invalidate()
	}()
	return w
}

func (w *wasmWindow) nowMS() float64 {
	return float64(time.Since(w.started)) / float64(time.Millisecond)
}

func (w *wasmWindow) invalidate() { w.ui.WasmInvalidate(w.instance) }

// close takes the window out of reach and gives the module back once it is
// not running. It never waits for the module, because the main thread calls
// it as the window goes away: a napp stuck in a loop keeps its own thread,
// and its memory, but does not take the launcher with it.
func (w *wasmWindow) close() {
	wasmWindows.Compute(w.instance, func(cur *wasmWindow, loaded bool) (*wasmWindow, bool) {
		return cur, !loaded || cur == w
	})

	go func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		w.closed = true
		if w.n != nil {
			w.n.Close()
			w.n = nil
		}
	}()
}

// wasmTransport is the backend's side of the window: what the backend sends
// goes into the bridge, and a "close" closes the Android window.
type wasmTransport struct{ w *wasmWindow }

func (t wasmTransport) Send(m backend.WireMsg) {
	if !t.w.br.HandleWire(wasmhost.WireMsg{
		T: m.T, ID: m.ID, Method: m.Method, Params: m.Params,
		Result: m.Result, Error: m.Error, Code: m.Code, Idx: m.Idx,
	}) {
		t.w.ui.CloseWindow(t.w.instance)
	}
}

func (t wasmTransport) Close() { t.w.ui.CloseWindow(t.w.instance) }

// WasmFrame runs one frame of a wasm napp's window at a width x height
// canvas and returns the pixels it painted: RGBA, premultiplied, rows
// top-down — what an ARGB_8888 Bitmap's copyPixelsFromBuffer takes as is.
// Nil means there is nothing to show yet (the module is still loading). An
// error means the napp cannot go on (it failed to load, or trapped) and the
// window should close, saying why.
//
// Call it from one thread only: the bytes are only copied out after this
// returns.
func WasmFrame(instance string, width, height int) ([]byte, error) {
	w := lookupWasm(instance)
	if w == nil {
		return nil, errors.New("this window is not running anything")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil, errors.New("this window is closed")
	}
	if w.loadErr != nil {
		return nil, w.loadErr
	}
	if w.n == nil {
		return nil, nil
	}

	now := time.Now()
	dt := 0.0
	if !w.last.IsZero() {
		dt = now.Sub(w.last).Seconds() * 1000
	}
	w.last = now

	// what the launcher sent goes in first: answers, store events and
	// actions all land before the frame that can show them
	for _, m := range w.br.Drain() {
		if err := w.n.Deliver(m.Kind, m.ID, m.Data); err != nil {
			return nil, w.fail("the napp crashed on a message: " + err.Error())
		}
	}

	w.n.Resize(max(width, 1), max(height, 1))
	if err := w.n.Frame(dt); err != nil {
		return nil, w.fail("the napp crashed: " + err.Error())
	}
	return w.n.Pixels(), nil
}

// fail takes the module down after a trap, so the window cannot run it again.
// Called with mu held.
func (w *wasmWindow) fail(msg string) error {
	w.loadErr = errors.New(msg)
	w.n.Close()
	w.n = nil
	return w.loadErr
}

// WasmAnimating says whether the napp asked for another frame without being
// poked, after the last WasmFrame.
func WasmAnimating(instance string) bool {
	w := lookupWasm(instance)
	if w == nil {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.n != nil && w.n.Animating()
}

// WasmInput hands a wasm napp one input event, as the ABI has them (see
// verdana/backend/wasm): kind, canvas x and y, the button or modifier mask,
// and the two extra values a scroll or a key repeat carries. Nothing reaches
// the napp while a prompt covers its window.
func WasmInput(instance string, kind, x, y, buttons int, a, b float64) {
	w := lookupWasm(instance)
	if w == nil || w.br.CurrentPrompt() != nil {
		return
	}
	w.mu.Lock()
	if w.n != nil {
		w.n.Input(int32(kind), int32(x), int32(y), int32(buttons), float32(a), float32(b))
	}
	w.mu.Unlock()
}

// WasmText hands a wasm napp what the keyboard typed, one text event per
// code point.
func WasmText(instance string, text string) {
	w := lookupWasm(instance)
	if w == nil || w.br.CurrentPrompt() != nil {
		return
	}
	w.mu.Lock()
	if w.n != nil {
		for _, r := range text {
			w.n.Input(wasm.EventText, r, 0, 0, 0, 0)
		}
	}
	w.mu.Unlock()
}
