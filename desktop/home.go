package main

import (
	"image"
	"image/color"
	"verdana/backend"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// The home tab: the pinned napps as a grid of icons with their names below,
// like a phone's home screen. A click opens a napp; dragging one puts it
// somewhere else in the grid.
//
// Gio has no drag and drop between widgets, so the grid takes the pointer
// itself: one event area over the whole thing, cells found by position.
// Every cell has the same size, so a position maps to a cell with plain
// arithmetic (plus the list's scroll position).

const (
	// homeCellDp is the width of one cell, homeGapDp the space between rows.
	homeCellDp = 92
	homeGapDp  = 12

	// homeSlopDp is how far a press has to move before it is a drag rather
	// than a click.
	homeSlopDp = 6

	// homeEdgeDp is the band at the top and bottom of the grid where a
	// drag scrolls the list.
	homeEdgeDp = 32
)

type homeGrid struct {
	list widget.List

	// geometry of the last layout, in pixels, for mapping positions to cells
	cols, cellW, cellH, rowH, n int

	// the pointer: where it is hovering (-1 for nowhere), and the press
	// being tracked, if any
	hover    int
	pressed  bool
	pressIdx int
	pressPos f32.Point
	pid      pointer.ID

	// dragging is true once the press moved past the slop: dragID is the
	// napp being carried, grabOff where in its cell it was picked up, pos
	// the pointer, target the slot it would land in
	dragging bool
	dragID   string
	grabOff  f32.Point
	pos      f32.Point
	target   int
}

var home = homeGrid{list: widget.List{List: layout.List{Axis: layout.Vertical}}, hover: -1}

// cellAt is the index of the cell under p (relative to the grid), or -1.
// clamp makes positions past the last cell or outside the columns count as
// the nearest cell instead, which is what a drop wants.
func (g *homeGrid) cellAt(p f32.Point, clamp bool) int {
	if g.n == 0 || g.cellW == 0 || g.rowH == 0 {
		return -1
	}
	y := g.list.Position.First*g.rowH + g.list.Position.Offset + int(p.Y)
	row, col := y/g.rowH, int(p.X)/g.cellW
	if !clamp && (p.X < 0 || p.Y < 0 || col >= g.cols || y%g.rowH >= g.cellH) {
		return -1
	}
	col = max(0, min(col, g.cols-1))
	idx := max(0, row)*g.cols + col
	if idx >= g.n {
		if !clamp {
			return -1
		}
		idx = g.n - 1
	}
	return idx
}

// update handles the pointer: hovering, clicks that open a napp, and drags
// that move one.
func (g *homeGrid) update(gtx layout.Context, pinned []backend.Napp) {
	for {
		ev, ok := gtx.Event(pointer.Filter{
			Target: g,
			Kinds:  pointer.Press | pointer.Drag | pointer.Release | pointer.Cancel | pointer.Move | pointer.Enter | pointer.Leave,
		})
		if !ok {
			break
		}
		e, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		switch e.Kind {
		case pointer.Move, pointer.Enter:
			g.hover = g.cellAt(e.Position, false)
		case pointer.Leave:
			g.hover = -1
		case pointer.Press:
			if e.Buttons != pointer.ButtonPrimary {
				continue
			}
			// a fresh press starts over, whatever an earlier one left behind
			// (its release may have gone to another tab)
			g.reset()
			g.pressIdx = g.cellAt(e.Position, false)
			if g.pressIdx < 0 {
				continue
			}
			g.pressed = true
			g.pid = e.PointerID
			g.pressPos = e.Position
		case pointer.Drag:
			if !g.pressed || e.PointerID != g.pid {
				continue
			}
			g.pos = e.Position
			if !g.dragging {
				d := e.Position.Sub(g.pressPos)
				slop := float32(gtx.Dp(homeSlopDp))
				if d.X*d.X+d.Y*d.Y < slop*slop {
					continue
				}
				// picked up: the list must not take this pointer for a
				// scroll from here on
				gtx.Execute(pointer.GrabCmd{Tag: g, ID: e.PointerID})
				g.dragging = true
				g.dragID = pinned[g.pressIdx].ID
				g.grabOff = g.pressPos.Sub(g.cellOrigin(g.pressIdx))
			}
			g.target = g.cellAt(e.Position, true)
		case pointer.Release:
			if !g.pressed || e.PointerID != g.pid {
				continue
			}
			if g.dragging {
				backend.MovePin(g.dragID, g.target)
			} else if idx := g.cellAt(e.Position, false); idx == g.pressIdx && idx < len(pinned) {
				backend.Launch(pinned[idx])
			}
			g.reset()
		case pointer.Cancel:
			g.reset()
		}
	}

	// the napp being carried can vanish under the drag (unpinned or
	// uninstalled meanwhile): drop the drag along with it
	if g.dragging && g.dragFrom(pinned) < 0 {
		g.reset()
	}
}

func (g *homeGrid) reset() {
	g.pressed, g.dragging, g.dragID = false, false, ""
}

// dragFrom is the current index of the napp being dragged, or -1.
func (g *homeGrid) dragFrom(pinned []backend.Napp) int {
	for i, n := range pinned {
		if n.ID == g.dragID {
			return i
		}
	}
	return -1
}

// cellOrigin is the top-left corner of cell idx relative to the grid, at the
// current scroll position.
func (g *homeGrid) cellOrigin(idx int) f32.Point {
	row, col := idx/g.cols, idx%g.cols
	y := row*g.rowH - g.list.Position.First*g.rowH - g.list.Position.Offset
	return f32.Pt(float32(col*g.cellW), float32(y))
}

func (g *homeGrid) Layout(gtx layout.Context, th *material.Theme, st backend.State) layout.Dimensions {
	if len(st.Pinned) == 0 {
		g.reset()
		l := material.Body2(th, "Nothing pinned yet. Pin napps from the Installed tab to have them here.")
		l.Color = currentTheme().muted
		return l.Layout(gtx)
	}

	// cells are all the same size: the icon, then two lines of caption
	captionSize := th.TextSize * 12 / 16
	lineH := gtx.Sp(captionSize * 13 / 10)
	g.cellW = gtx.Dp(homeCellDp)
	g.cellH = gtx.Dp(6+56+6+6) + 2*lineH
	g.rowH = g.cellH + gtx.Dp(homeGapDp)
	g.cols = max(1, gtx.Constraints.Max.X/g.cellW)
	g.n = len(st.Pinned)

	size := gtx.Constraints.Max
	defer clip.Rect{Max: size}.Push(gtx.Ops).Pop()
	event.Op(gtx.Ops, g)
	if g.dragging {
		pointer.CursorGrabbing.Add(gtx.Ops)
	} else if g.hover >= 0 {
		pointer.CursorPointer.Add(gtx.Ops)
	}

	g.update(gtx, st.Pinned)

	// while dragging, the grid shows the order the drop would make, with an
	// empty slot where the carried napp would land
	order := st.Pinned
	if g.dragging {
		from := g.dragFrom(st.Pinned)
		order = make([]backend.Napp, 0, len(st.Pinned))
		order = append(order, st.Pinned[:from]...)
		order = append(order, st.Pinned[from+1:]...)
		order = append(order[:g.target], append([]backend.Napp{st.Pinned[from]}, order[g.target:]...)...)

		// near the top or bottom edge the list scrolls along
		edge := float32(gtx.Dp(homeEdgeDp))
		if g.pos.Y < edge {
			g.list.ScrollBy(-0.05)
			gtx.Execute(op.InvalidateCmd{})
		} else if g.pos.Y > float32(size.Y)-edge {
			g.list.ScrollBy(0.05)
			gtx.Execute(op.InvalidateCmd{})
		}
	}

	rows := (len(order) + g.cols - 1) / g.cols
	dims := material.List(th, &g.list).Layout(gtx, rows, func(gtx layout.Context, r int) layout.Dimensions {
		children := make([]layout.FlexChild, 0, g.cols)
		for c := 0; c < g.cols; c++ {
			i := r*g.cols + c
			if i >= len(order) {
				break
			}
			n := order[i]
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				switch {
				case g.dragging && n.ID == g.dragID:
					return g.placeholder(gtx)
				case !g.dragging && (i == g.hover || (g.pressed && i == g.pressIdx)):
					return g.cell(gtx, th, n, currentTheme().card)
				default:
					return g.cell(gtx, th, n, color.NRGBA{})
				}
			}))
		}
		return layout.Inset{Bottom: unit.Dp(homeGapDp)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal}.Layout(gtx, children...)
		})
	})

	// the carried napp floats over everything, under the pointer
	if g.dragging {
		if from := g.dragFrom(st.Pinned); from >= 0 {
			p := g.pos.Sub(g.grabOff)
			defer op.Offset(image.Pt(int(p.X), int(p.Y))).Push(gtx.Ops).Pop()
			g.cell(gtx, th, st.Pinned[from], currentTheme().chipBg)
		}
	}

	return dims
}

// cell draws one napp: its icon with its name under it, on bg (transparent
// for none).
func (g *homeGrid) cell(gtx layout.Context, th *material.Theme, n backend.Napp, bg color.NRGBA) layout.Dimensions {
	sz := image.Pt(g.cellW, g.cellH)
	gtx.Constraints = layout.Exact(sz)
	if bg.A > 0 {
		r := gtx.Dp(8)
		bgArea := clip.RRect{Rect: image.Rectangle{Max: sz}, NW: r, NE: r, SW: r, SE: r}.Push(gtx.Ops)
		paint.Fill(gtx.Ops, bg)
		bgArea.Pop()
	}
	layout.UniformInset(unit.Dp(6)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min = image.Point{}
		return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return nappIcon(gtx, n, 56)
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				name := n.Name
				if name == "" {
					name = n.ID
				}
				l := material.Caption(th, name)
				l.Alignment = text.Middle
				l.MaxLines = 2
				return l.Layout(gtx)
			}),
		)
	})
	return layout.Dimensions{Size: sz}
}

// placeholder is the empty slot the carried napp would land in.
func (g *homeGrid) placeholder(gtx layout.Context) layout.Dimensions {
	sz := image.Pt(g.cellW, g.cellH)
	r := gtx.Dp(8)
	inset := gtx.Dp(6)
	rect := image.Rect(inset, inset, sz.X-inset, sz.Y-inset)
	defer clip.RRect{Rect: rect, NW: r, NE: r, SW: r, SE: r}.Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, currentTheme().imageBg)
	return layout.Dimensions{Size: sz}
}
