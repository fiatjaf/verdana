// Command wasmchild is the window a wasm napp gets.
//
// A web napp's window is a webview showing a page. A wasm napp has no page:
// this is a Gio window that runs the napp's module and shows the canvas the
// module paints, nothing else. It is its own process for the same reason the
// webview shell is — a napp that hangs, traps or spins belongs to its own
// crash, not to the launcher's.
//
// It talks to the launcher over stdin/stdout with the same wire messages the
// webview shell uses (see backend.WireMsg), so the backend does not know the
// difference.
package main

import (
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"gioui.org/app"
	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"github.com/rs/zerolog"

	"verdana/backend/wasm"
)

var log = zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr}).With().
	Int("_", os.Getpid()).
	Timestamp().
	Logger()

// nappMeta is what the launcher told us about this window, through the
// environment.
type nappMeta struct {
	ID       string
	Name     string
	Wasm     string
	Instance string
	Number   string
	Width    int
	Height   int
}

var meta nappMeta

// br is this window's bridge to the launcher: napp_call goes out through it,
// and everything the launcher sends comes in through it.
var br *bridge

func main() {
	meta = nappMeta{
		ID:       os.Getenv("VERDANA_NAPP_ID"),
		Name:     os.Getenv("VERDANA_NAPP_NAME"),
		Wasm:     os.Getenv("VERDANA_NAPP_WASM"),
		Instance: os.Getenv("VERDANA_INSTANCE_ID"),
		Number:   os.Getenv("VERDANA_WINDOW_NUMBER"),
		Width:    envSize("VERDANA_WINDOW_WIDTH", wasm.DefaultWidth),
		Height:   envSize("VERDANA_WINDOW_HEIGHT", wasm.DefaultHeight),
	}
	if meta.Name == "" {
		meta.Name = meta.ID
	}
	if meta.Wasm == "" {
		fail("the launcher did not say which module to run")
	}

	log.Info().Str("napp", meta.ID).Str("instance", meta.Instance).Str("wasm", meta.Wasm).
		Msg("wasm napp window started")

	br = newBridge(os.Stdout)

	// Everything the launcher says comes down stdin, the same pipe the
	// webview shell reads: answers to the napp's calls, actions, feeds,
	// prompts, and "close", which is the one thing about a window's lifetime
	// that is not ours to decide. The module is in this process's memory and
	// goes away with it, so leaving is the whole cleanup there is.
	stop := make(chan struct{})
	go func() {
		dec := json.NewDecoder(os.Stdin)
		for {
			var m wireMsg
			if err := dec.Decode(&m); err != nil {
				break
			}
			if !br.handleWire(m) {
				break
			}
		}
		close(stop)
	}()

	go func() {
		<-stop
		os.Exit(0)
	}()

	// Gio wants the main goroutine for itself (macOS will not draw a window
	// from any other), so the window's loop is the one that goes elsewhere.
	go func() {
		runWindow()
		os.Exit(0)
	}()
	app.Main()
}

// fail says what went wrong where the user will see it and leaves. An empty
// window would look like a napp that simply draws nothing.
func fail(msg string) {
	fmt.Fprintln(os.Stderr, "verdana: "+msg)
	log.Error().Str("napp", meta.ID).Msg(msg)
	os.Exit(1)
}

var started = time.Now()

// nowMS is the host clock the napp sees through napp_now_ms.
func nowMS() float64 { return float64(time.Since(started)) / float64(time.Millisecond) }

func envSize(key string, fallback int) int {
	if raw := strings.TrimSpace(os.Getenv(key)); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			return v
		}
	}
	return fallback
}

// ─── the window ──────────────────────────────────────────────────

// background is what a window shows before its first canvas: the same near
// black the launcher's own windows use, so an opening wasm window does not
// flash white.
var background = color.NRGBA{R: 0x14, G: 0x15, B: 0x18, A: 0xff}

// runWindow is the whole window: a canvas, a module, and an event loop. No
// chrome of its own — the napp painted every pixel there is.
func runWindow() {
	w := new(app.Window)
	w.Option(app.Title(windowTitle(meta.Name, meta.Number)),
		app.Size(unit.Dp(meta.Width), unit.Dp(meta.Height)))

	loaded := make(chan *napp, 1)
	failures := make(chan error, 1)

	// Compiling the module takes a moment and must not hold up the window, so
	// it happens beside the event loop and the first frames just wait. The
	// window is nudged when it lands, because otherwise a module that is
	// ready while nothing else is happening would sit there unseen until the
	// user moved the mouse.
	go func() {
		n, err := loadWasmNapp(meta.Wasm, nowMS, br.call)
		if err != nil {
			failures <- err
		} else {
			loaded <- n
		}
		w.Invalidate()
	}()

	var (
		n      *napp
		canvas = image.NewRGBA(image.Rect(0, 0, 1, 1))
		ops    op.Ops
		last   = time.Now()
		// input is the tag the window's pointer events are addressed to.
		input  int
		prompt = newPromptOverlay()
	)

	// Whatever the launcher sends wakes the window, so a napp parked on an
	// idle canvas still hears its answers on the frame right after.
	br.setWake(w.Invalidate)

	for {
		ev := w.Event()

		// A module that finished loading (or failed to) is picked up after
		// the event, not before: the loader sends and then invalidates, so
		// the frame that wakes us is the one that has to see the module.
		// Checked before blocking instead, that frame would paint nothing
		// and a napp that settles at once would never be asked again.
		select {
		case n = <-loaded:
			log.Info().Msg("first canvas coming up")
		case err := <-failures:
			fail(err.Error())
		default:
		}

		switch e := ev.(type) {
		case app.DestroyEvent:
			if n != nil {
				n.Close()
			}
			return

		case app.FrameEvent:
			now := time.Now()
			dt := now.Sub(last).Seconds() * 1000
			last = now

			size := e.Size
			if size.X < 1 {
				size.X = 1
			}
			if size.Y < 1 {
				size.Y = 1
			}
			fitCanvas(canvas, size)

			gtx := app.NewContext(&ops, e)

			// What the launcher sent goes in first: answers, feeds and
			// actions all land before the frame that can show them. A
			// module that is still loading keeps its messages queued.
			if n != nil {
				for _, m := range br.drain() {
					if err := n.Deliver(m.kind, m.id, m.data); err != nil {
						fail("wasm napp trapped on a message: " + err.Error())
					}
				}
			}

			// A prompt covers the canvas and takes the input with it: the
			// napp must not be clicked through a question about itself.
			pv := br.currentPrompt()

			// Gio hands pointer events only to a target that registered
			// for them, and only through the frame's context. They go to
			// the napp before it paints, so a click shows on this frame.
			for {
				ev, ok := gtx.Event(pointer.Filter{
					Target:  &input,
					Kinds:   pointer.Move | pointer.Drag | pointer.Press | pointer.Release | pointer.Scroll | pointer.Leave | pointer.Cancel,
					ScrollX: pointer.ScrollRange{Min: math.MinInt32, Max: math.MaxInt32},
					ScrollY: pointer.ScrollRange{Min: math.MinInt32, Max: math.MaxInt32},
				})
				if !ok {
					break
				}
				if pe, ok := ev.(pointer.Event); ok && n != nil && pv == nil {
					sendPointer(n, pe)
				}
			}

			animating := false
			if n != nil {
				n.Resize(size.X, size.Y)
				if err := n.Frame(dt); err != nil {
					// A trap is the napp's own crash. Take the window down
					// rather than sit on a half-drawn canvas.
					fail("wasm napp trapped: " + err.Error())
				}
				// The canvas is the napp's own memory, so it is copied out
				// of it rather than referenced: Gio may still be reading
				// the last frame's texture when the next frame lands.
				copy(canvas.Pix, n.Pixels())
				animating = n.Animating()
			}

			if pv != nil {
				prompt.Layout(gtx, pv, br.themeName(), func(id int, ok bool, index int, scope string) {
					br.answerPrompt(id, ok, index, scope)
					w.Invalidate()
				})
			} else {
				drawCanvas(gtx, canvas, n != nil && n.HasCanvas())

				// The whole window is the napp's input area.
				area := clip.Rect(image.Rectangle{Max: size}).Push(gtx.Ops)
				event.Op(gtx.Ops, &input)
				area.Pop()
			}

			e.Frame(&ops)

			// A napp that is animating asked for the next frame; anything
			// else waits for an event, which is a lot cheaper than a window
			// redrawing an unchanged canvas forever.
			if animating {
				w.Invalidate()
			}
		}
	}
}

// fitCanvas keeps the image the napp's pixels land in exactly the canvas
// size, reallocating only when the window really changed size.
func fitCanvas(img *image.RGBA, size image.Point) {
	if img.Rect.Dx() == size.X && img.Rect.Dy() == size.Y {
		return
	}
	*img = *image.NewRGBA(image.Rect(0, 0, size.X, size.Y))
}

// drawCanvas puts the napp's pixels on screen, letterboxed so a window
// dragged to a strange aspect stretches the napp's output evenly instead of
// squashing it.
func drawCanvas(gtx layout.Context, canvas *image.RGBA, ready bool) {
	if !ready {
		paint.Fill(gtx.Ops, background)
		return
	}

	// A fresh ImageOp every frame on purpose: Gio keys its texture cache on
	// the op's identity, so reusing one would keep showing the first canvas
	// for ever. The previous texture is released as the frame is swept.
	sw, sh := float32(canvas.Rect.Dx()), float32(canvas.Rect.Dy())
	w, h := float32(gtx.Constraints.Max.X), float32(gtx.Constraints.Max.Y)
	if sw <= 0 || sh <= 0 || w <= 0 || h <= 0 {
		return
	}
	paint.Fill(gtx.Ops, background)
	scale := min(w/sw, h/sh)
	off := f32.Pt((w-sw*scale)/2, (h-sh*scale)/2)
	defer op.Affine(f32.AffineId().Scale(f32.Point{}, f32.Pt(scale, scale)).Offset(off)).Push(gtx.Ops).Pop()
	paint.NewImageOp(canvas).Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
}

// sendPointer turns one Gio pointer event into the small ABI event, dropping
// the parts a canvas napp has no use for (hover, leave, per-pointer ids). A
// scroll carries its position too, so a napp can scroll whatever is under the
// pointer.
func sendPointer(n *napp, e pointer.Event) {
	pos := [2]int32{int32(e.Position.X + 0.5), int32(e.Position.Y + 0.5)}
	buttons := nappButtons(e.Buttons)

	switch e.Kind {
	case pointer.Press:
		n.Input(wasm.EventPointerDown, pos[0], pos[1], int32(buttons), 0, 0)
	case pointer.Release:
		n.Input(wasm.EventPointerUp, pos[0], pos[1], int32(buttons), 0, 0)
	case pointer.Scroll:
		// Gio's scroll is the distance the content should travel down and
		// right; the ABI's, like the browser's, is the other way round.
		n.Input(wasm.EventScroll, pos[0], pos[1], 0, -e.Scroll.X, -e.Scroll.Y)
	case pointer.Leave, pointer.Cancel:
		// A napp that never hears the pointer left keeps showing a hover.
		n.Input(wasm.EventPointerMove, -1, -1, 0, 0, 0)
	default:
		n.Input(wasm.EventPointerMove, pos[0], pos[1], int32(buttons), 0, 0)
	}
}

// nappButtons is Gio's button set as the mask the ABI carries.
func nappButtons(b pointer.Buttons) uint8 {
	var out uint8
	if b&pointer.ButtonPrimary != 0 {
		out |= wasm.ButtonPrimary
	}
	if b&pointer.ButtonSecondary != 0 {
		out |= wasm.ButtonSecondary
	}
	if b&pointer.ButtonTertiary != 0 {
		out |= wasm.ButtonMiddle
	}
	return out
}

func windowTitle(name, number string) string {
	if name == "" {
		name = "Napp"
	}
	if number == "" {
		return name
	}
	return name + " - window #" + number
}
