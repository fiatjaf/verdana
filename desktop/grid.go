package main

import (
	"image"
	"image/color"
	"strconv"
	"strings"
	"verdana/backend"

	"gioui.org/font"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

const (
	// tileMinWidth is the narrowest a grid tile gets: a window too narrow
	// for two of them lists napps one per row, as cards, instead.
	tileMinWidth = unit.Dp(280)
	// tileGap is the space between tiles, across and down.
	tileGap = unit.Dp(8)
	// footerAuthorMin is the room a tile's author keeps beside its buttons;
	// with less, the buttons go on a line of their own.
	footerAuthorMin = unit.Dp(110)
)

// gridColumns is how many tiles of at least tileMinWidth fit across width.
func gridColumns(gtx layout.Context, width int) int {
	gap := gtx.Dp(tileGap)
	cols := (width + gap) / (gtx.Dp(tileMinWidth) + gap)
	return max(cols, 1)
}

// nappGrid lists the napps vis indexes: as a grid of tiles, as many across as
// the window fits, or one card per row in a window too narrow for two. card
// draws the napp at an index either way. lastCols remembers the column count
// from frame to frame: the list scrolls by row, so when a resize changes how
// many napps a row holds, the position is moved to keep the same ones in view.
func nappGrid(
	gtx layout.Context,
	th *material.Theme,
	list *widget.List,
	lastCols *int,
	vis []int,
	card func(gtx layout.Context, row int, tile bool) layout.Dimensions,
) layout.Dimensions {
	cols := gridColumns(gtx, gtx.Constraints.Max.X)
	if *lastCols != 0 && cols != *lastCols {
		list.Position.First = list.Position.First * *lastCols / cols
		list.Position.Offset = 0
	}
	*lastCols = cols
	if cols == 1 {
		return material.List(th, list).Layout(gtx, len(vis), func(gtx layout.Context, i int) layout.Dimensions {
			return card(gtx, vis[i], false)
		})
	}
	rows := (len(vis) + cols - 1) / cols
	return material.List(th, list).Layout(gtx, rows, func(gtx layout.Context, r int) layout.Dimensions {
		first := r * cols
		n := min(cols, len(vis)-first)
		return layout.Inset{Bottom: tileGap}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return gridRow(gtx, cols, n, func(gtx layout.Context, i int) layout.Dimensions {
				return card(gtx, vis[first+i], true)
			})
		})
	})
}

// gridRow lays out the n cells of one grid row in cols equal columns, every
// one as tall as the tallest, so the tiles of a row line up top and bottom.
// The cells are measured first, with input disabled so no click is handled
// twice, then laid out for real at the row's height.
func gridRow(gtx layout.Context, cols, n int, cell func(gtx layout.Context, i int) layout.Dimensions) layout.Dimensions {
	gap := gtx.Dp(tileGap)
	colW := max((gtx.Constraints.Max.X-gap*(cols-1))/cols, 0)
	h := 0
	for i := range n {
		mg := gtx.Disabled()
		mg.Constraints = layout.Constraints{
			Min: image.Pt(colW, 0),
			Max: image.Pt(colW, gtx.Constraints.Max.Y),
		}
		m := op.Record(gtx.Ops)
		d := cell(mg, i)
		m.Stop()
		h = max(h, d.Size.Y)
	}
	for i := range n {
		t := op.Offset(image.Pt(i*(colW+gap), 0)).Push(gtx.Ops)
		cg := gtx
		cg.Constraints = layout.Exact(image.Pt(colW, h))
		cell(cg, i)
		t.Pop()
	}
	return layout.Dimensions{Size: image.Pt(gtx.Constraints.Max.X, h)}
}

// wrapFlow lays the widgets out left to right, starting a new line whenever
// the next one would not fit. With end set, each line is pushed to the
// right edge.
func wrapFlow(gtx layout.Context, gap unit.Dp, end bool, items []layout.Widget) layout.Dimensions {
	type placed struct {
		call op.CallOp
		size image.Point
	}
	g := gtx.Dp(gap)
	maxW := gtx.Constraints.Max.X
	cg := gtx
	cg.Constraints.Min = image.Point{}
	var lines [][]placed
	var line []placed
	lineW := 0
	for _, item := range items {
		m := op.Record(gtx.Ops)
		d := item(cg)
		call := m.Stop()
		if len(line) > 0 && lineW+g+d.Size.X > maxW {
			lines, line, lineW = append(lines, line), nil, 0
		}
		if len(line) > 0 {
			lineW += g
		}
		lineW += d.Size.X
		line = append(line, placed{call, d.Size})
	}
	if len(line) > 0 {
		lines = append(lines, line)
	}
	w, y := 0, 0
	for i, line := range lines {
		lw, lh := -g, 0
		for _, p := range line {
			lw += p.size.X + g
			lh = max(lh, p.size.Y)
		}
		x := 0
		if end {
			x = max(maxW-lw, 0)
		}
		if i > 0 {
			y += g
		}
		for _, p := range line {
			t := op.Offset(image.Pt(x, y)).Push(gtx.Ops)
			p.call.Add(gtx.Ops)
			t.Pop()
			x += p.size.X + g
		}
		y += lh
		w = max(w, x-g)
	}
	return layout.Dimensions{Size: image.Pt(w, y)}
}

// cardDescription is a napp's description the way a card or tile shows it:
// one paragraph, and cut well short of anything that could take more than
// the few lines the card gives it (the napp page has it in full).
func cardDescription(s string) string {
	return truncate(s, 240)
}

// renderNappTile draws one napp as a grid tile: icon and name on top, then
// the description, optional actions and author, and buttons along the bottom.
// The clickable parts work as renderNappCard's do. Given a minimum height
// (gridRow's second pass) the tile stretches to it and keeps its buttons at
// the bottom edge.
func renderNappTile(
	gtx layout.Context,
	th *material.Theme,
	cardBtn,
	authorBtn,
	openBtn,
	settingsBtn,
	btn,
	secondBtn *widget.Clickable,
	openLabel,
	btnLabel,
	secondLabel string,
	showActions bool,
	napp backend.Napp,
) layout.Dimensions {
	authorName, authorPic := nappAuthor(napp)
	fill := gtx.Constraints.Min.Y > 0
	width := gtx.Constraints.Max.X

	// the buttons: Open and Settings in their softer colours, then the
	// update and install/uninstall ones
	var buttons []layout.Widget
	button := func(b *widget.Clickable, label string, colors *[2]color.NRGBA) {
		if b == nil || label == "" {
			return
		}
		buttons = append(buttons, func(gtx layout.Context) layout.Dimensions {
			pointer.CursorPointer.Add(gtx.Ops)
			s := material.Button(th, b, label)
			if colors != nil {
				s.Background, s.Color = colors[0], colors[1]
			}
			s.TextSize = unit.Sp(13)
			s.Inset = layout.Inset{Top: unit.Dp(6), Bottom: unit.Dp(6), Left: unit.Dp(10), Right: unit.Dp(10)}
			return s.Layout(gtx)
		})
	}
	p := currentTheme()
	button(openBtn, openLabel, &[2]color.NRGBA{p.suggestBg, p.suggestFg})
	button(settingsBtn, "Settings", &[2]color.NRGBA{p.chipBg, p.chipFg})
	button(secondBtn, secondLabel, nil)
	button(btn, btnLabel, nil)

	author := func(gtx layout.Context) layout.Dimensions {
		if authorName == "" {
			return layout.Dimensions{}
		}
		inner := func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return avatar(gtx, authorPic, 18)
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					c := material.Caption(th, authorName)
					c.Color = currentTheme().muted
					c.MaxLines = 1
					return c.Layout(gtx)
				}),
			)
		}
		if authorBtn == nil {
			return inner(gtx)
		}
		pointer.CursorPointer.Add(gtx.Ops)
		return authorBtn.Layout(gtx, inner)
	}

	// the author shares a line with the buttons while there is room for
	// both; otherwise it gets its own, and the buttons wrap below it
	footer := func(gtx layout.Context) layout.Dimensions {
		gap := gtx.Dp(6)
		total := -gap
		mg := gtx.Disabled()
		mg.Constraints.Min = image.Point{}
		for _, b := range buttons {
			m := op.Record(gtx.Ops)
			total += b(mg).Size.X + gap
			m.Stop()
		}
		if total+gtx.Dp(footerAuthorMin) <= gtx.Constraints.Max.X {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, author),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return wrapFlow(gtx, unit.Dp(6), false, buttons)
				}),
			)
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(author),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if authorName == "" || len(buttons) == 0 {
					return layout.Dimensions{}
				}
				return layout.Spacer{Height: unit.Dp(8)}.Layout(gtx)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return wrapFlow(gtx, unit.Dp(6), true, buttons)
			}),
		)
	}

	children := []layout.FlexChild{
		// icon, name and the napplet chip
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Right: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return nappIcon(gtx, napp, 44)
					})
				}),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							l := material.Body1(th, napp.Name)
							l.Font.Weight = font.Bold
							l.MaxLines = 2
							return l.Layout(gtx)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							if !napp.IsNapplet() {
								return layout.Dimensions{}
							}
							gtx.Constraints.Min.X = 0
							return layout.Inset{Top: unit.Dp(3)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								return actionChip(gtx, th, "napplet")
							})
						}),
					)
				}),
			)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if napp.Description == "" {
				return layout.Dimensions{}
			}
			return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				l := material.Body2(th, cardDescription(napp.Description))
				l.Color = currentTheme().subtle
				l.MaxLines = 3
				return l.Layout(gtx)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if !showActions {
				return layout.Dimensions{}
			}
			chips := actionChips(th, napp)
			if len(chips) == 0 {
				return layout.Dimensions{}
			}
			return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return wrapFlow(gtx, unit.Dp(4), false, chips)
			})
		}),
	}
	if fill {
		children = append(children, layout.Flexed(1, layout.Spacer{}.Layout))
	}
	children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: unit.Dp(10)}.Layout(gtx, footer)
	}))

	macro := op.Record(gtx.Ops)
	dims := layout.UniformInset(unit.Dp(12)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
	})
	call := macro.Stop()

	size := image.Pt(width, max(dims.Size.Y, gtx.Constraints.Min.Y))
	bg := clip.RRect{Rect: image.Rectangle{Max: size}, NW: 8, NE: 8, SW: 8, SE: 8}
	paintTile := func(gtx layout.Context) layout.Dimensions {
		defer bg.Push(gtx.Ops).Pop()
		paint.Fill(gtx.Ops, currentTheme().card)
		call.Add(gtx.Ops)
		return layout.Dimensions{Size: size}
	}
	if cardBtn == nil {
		return paintTile(gtx)
	}
	// the tile fills its click area and its content replays on top, so the
	// buttons inside it stay clickable (see renderNappCard)
	pointer.CursorPointer.Add(gtx.Ops)
	return cardBtn.Layout(gtx, paintTile)
}

// maxActionChips is how many of a napp's actions its card or tile shows;
// the rest are counted in one last chip, so a napp handling dozens of
// actions doesn't stretch its card (and, in a grid, its whole row).
const maxActionChips = 6

// actionChips are a napp's actions as chips, for wrapFlow.
func actionChips(th *material.Theme, napp backend.Napp) []layout.Widget {
	var actions []string
	for _, action := range napp.Actions {
		if strings.TrimSpace(action) != "" {
			actions = append(actions, truncate(action, 32))
		}
	}
	if len(actions) > maxActionChips {
		more := len(actions) - (maxActionChips - 1)
		actions = append(actions[:maxActionChips-1], "+"+strconv.Itoa(more)+" more")
	}
	chips := make([]layout.Widget, len(actions))
	for i, action := range actions {
		chips[i] = func(gtx layout.Context) layout.Dimensions {
			return actionChip(gtx, th, action)
		}
	}
	return chips
}
