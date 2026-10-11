package main

import (
	"image"
	"time"

	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// Tooltips: Gio has none, so a button that wants one is laid out through
// withTooltip, which notices the pointer resting on it (Clickable.Hovered)
// and, after a short wait, floats a small label above it, over everything
// else drawn in the frame.

// tooltipDelay is how long the pointer has to rest on a button before its
// tooltip shows.
const tooltipDelay = 400 * time.Millisecond

// hoverSince is when the pointer came to rest on each button that has a
// tooltip, gone once it leaves.
var hoverSince = make(map[*widget.Clickable]time.Time)

// withTooltip lays out w (a button drawn with btn) and, while the pointer has
// been on it for a moment, tip above it, right-aligned with it so it stays
// inside the window for buttons at the end of a row.
func withTooltip(gtx layout.Context, th *material.Theme, btn *widget.Clickable, tip string, w layout.Widget) layout.Dimensions {
	dims := w(gtx)
	if !btn.Hovered() {
		delete(hoverSince, btn)
		return dims
	}
	since, ok := hoverSince[btn]
	if !ok {
		since = gtx.Now
		hoverSince[btn] = since
	}
	if wait := since.Add(tooltipDelay); gtx.Now.Before(wait) {
		gtx.Execute(op.InvalidateCmd{At: wait})
		return dims
	}

	// the label is measured first, then placed by its size
	tgtx := gtx
	tgtx.Constraints = layout.Constraints{Max: image.Pt(gtx.Dp(280), gtx.Dp(200))}
	inner := op.Record(gtx.Ops)
	tipDims := tooltipBox(tgtx, th, tip)
	box := inner.Stop()

	outer := op.Record(gtx.Ops)
	off := image.Pt(dims.Size.X-tipDims.Size.X, -tipDims.Size.Y-gtx.Dp(4))
	t := op.Offset(off).Push(gtx.Ops)
	pass := pointer.PassOp{}.Push(gtx.Ops)
	box.Add(gtx.Ops)
	pass.Pop()
	t.Pop()
	op.Defer(gtx.Ops, outer.Stop())
	return dims
}

// tooltipBox is the tooltip itself: a small label on the inverted colors.
func tooltipBox(gtx layout.Context, th *material.Theme, tip string) layout.Dimensions {
	p := currentTheme()
	m := op.Record(gtx.Ops)
	dims := layout.Inset{Top: unit.Dp(4), Bottom: unit.Dp(4), Left: unit.Dp(8), Right: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		l := material.Caption(th, tip)
		l.Color = p.bg
		return l.Layout(gtx)
	})
	label := m.Stop()
	r := gtx.Dp(4)
	area := clip.RRect{Rect: image.Rectangle{Max: dims.Size}, NW: r, NE: r, SW: r, SE: r}.Push(gtx.Ops)
	paint.Fill(gtx.Ops, p.fg)
	area.Pop()
	label.Add(gtx.Ops)
	return dims
}
