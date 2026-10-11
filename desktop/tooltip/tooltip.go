// Package tooltip gives Gio buttons tooltips, which Gio has none of. A button
// that wants one is laid out through Layer.With, which notices the pointer
// resting on it (Clickable.Hovered). The tooltip itself is drawn once per
// frame, after everything else, next to the pointer and kept inside the
// window: a button can sit anywhere (a tab at the very top, a row at the
// bottom, a card at the left edge) and its tooltip must never be cut off.
package tooltip

import (
	"image"
	"image/color"
	"time"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// Delay is how long the pointer has to rest on a button before its tooltip
// shows.
const Delay = 400 * time.Millisecond

// Layer is one window's tooltip state. Each window has its own, as each runs
// its frames on its own goroutine.
type Layer struct {
	// since is when the pointer came to rest on each button that has a
	// tooltip, gone once it leaves or the button isn't laid out anymore.
	since map[*widget.Clickable]hover

	// frame counts Draws, so a hover nobody laid out this frame is dropped.
	frame int

	// pointer is where the pointer is in the window, from Track.
	pointer f32.Point

	// text is the tooltip due this frame, "" for none.
	text string
}

type hover struct {
	at    time.Time
	frame int
}

func New() *Layer {
	return &Layer{since: make(map[*widget.Clickable]hover)}
}

// Track lays out w (the whole window) under an area that follows the pointer,
// so Draw knows where in the window it is. The area is an ancestor of
// everything in w, so it sees the pointer without taking any event from the
// widgets.
func (l *Layer) Track(gtx layout.Context, w layout.Widget) layout.Dimensions {
	for {
		ev, ok := gtx.Event(pointer.Filter{
			Target: l,
			Kinds:  pointer.Move | pointer.Drag | pointer.Enter | pointer.Press,
		})
		if !ok {
			break
		}
		if e, ok := ev.(pointer.Event); ok {
			l.pointer = e.Position
		}
	}
	area := clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops)
	pass := pointer.PassOp{}.Push(gtx.Ops)
	event.Op(gtx.Ops, l)
	pass.Pop()
	dims := w(gtx)
	area.Pop()
	return dims
}

// With lays out w (a button drawn with btn) and, once the pointer has rested
// on it for a moment, asks for tip to be shown.
func (l *Layer) With(gtx layout.Context, btn *widget.Clickable, tip string, w layout.Widget) layout.Dimensions {
	dims := w(gtx)
	if !btn.Hovered() {
		delete(l.since, btn)
		return dims
	}
	h, ok := l.since[btn]
	if !ok {
		h.at = gtx.Now
	}
	h.frame = l.frame
	l.since[btn] = h
	if wait := h.at.Add(Delay); gtx.Now.Before(wait) {
		gtx.Execute(op.InvalidateCmd{At: wait})
		return dims
	}
	l.Show(tip)
	return dims
}

// Show asks for tip to be shown this frame, for a widget that tracks its own
// hovering. A button inside a clickable card is laid out (and asks) before
// the card does: the innermost one under the pointer is the one meant, so
// the first ask of a frame wins.
func (l *Layer) Show(tip string) {
	if l.text == "" {
		l.text = tip
	}
}

// Draw draws this frame's tooltip, if any, just below and right of the
// pointer — above it, or moved left, where the window ends — as fg text on
// bg. Called with the whole window's gtx, after everything else is laid out.
func (l *Layer) Draw(gtx layout.Context, th *material.Theme, bg, fg color.NRGBA) {
	for btn, h := range l.since {
		if h.frame != l.frame {
			delete(l.since, btn)
		}
	}
	l.frame++

	tip := l.text
	l.text = ""
	if tip == "" {
		return
	}

	// the label is measured first, then placed by its size
	tgtx := gtx
	tgtx.Constraints = layout.Constraints{Max: image.Pt(min(gtx.Dp(300), gtx.Constraints.Max.X), gtx.Constraints.Max.Y)}
	m := op.Record(gtx.Ops)
	size := box(tgtx, th, tip, bg, fg).Size
	call := m.Stop()

	win := gtx.Constraints.Max
	p := image.Pt(int(l.pointer.X)+gtx.Dp(12), int(l.pointer.Y)+gtx.Dp(20))
	if p.Y+size.Y > win.Y {
		p.Y = int(l.pointer.Y) - gtx.Dp(8) - size.Y
	}
	p.X = max(0, min(p.X, win.X-size.X))
	p.Y = max(0, min(p.Y, win.Y-size.Y))

	t := op.Offset(p).Push(gtx.Ops)
	call.Add(gtx.Ops)
	t.Pop()
}

// box is the tooltip itself: a small label on a rounded fill.
func box(gtx layout.Context, th *material.Theme, tip string, bg, fg color.NRGBA) layout.Dimensions {
	m := op.Record(gtx.Ops)
	dims := layout.Inset{Top: unit.Dp(4), Bottom: unit.Dp(4), Left: unit.Dp(8), Right: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		l := material.Caption(th, tip)
		l.Color = fg
		return l.Layout(gtx)
	})
	label := m.Stop()
	r := gtx.Dp(4)
	area := clip.RRect{Rect: image.Rectangle{Max: dims.Size}, NW: r, NE: r, SW: r, SE: r}.Push(gtx.Ops)
	paint.Fill(gtx.Ops, bg)
	area.Pop()
	label.Add(gtx.Ops)
	return dims
}
