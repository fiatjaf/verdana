package main

import (
	"image"
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
)

// gridColumns is how many tiles of at least tileMinWidth fit across width.
func gridColumns(gtx layout.Context, width int) int {
	gap := gtx.Dp(tileGap)
	cols := (width + gap) / (gtx.Dp(tileMinWidth) + gap)
	return max(cols, 1)
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
// the next one would not fit.
func wrapFlow(gtx layout.Context, gap unit.Dp, items []layout.Widget) layout.Dimensions {
	g := gtx.Dp(gap)
	maxW := gtx.Constraints.Max.X
	cg := gtx
	cg.Constraints.Min = image.Point{}
	x, y, lineH, w := 0, 0, 0, 0
	for _, item := range items {
		m := op.Record(gtx.Ops)
		d := item(cg)
		call := m.Stop()
		if x > 0 && x+d.Size.X > maxW {
			x, y, lineH = 0, y+lineH+g, 0
		}
		t := op.Offset(image.Pt(x, y)).Push(gtx.Ops)
		call.Add(gtx.Ops)
		t.Pop()
		w = max(w, x+d.Size.X)
		lineH = max(lineH, d.Size.Y)
		x += d.Size.X + g
	}
	return layout.Dimensions{Size: image.Pt(w, y+lineH)}
}

// renderNappTile draws one napp as a grid tile: icon and name on top, then
// the description, its actions and author, and the buttons along the bottom.
// The clickable parts work as renderNappCard's do. Given a minimum height
// (gridRow's second pass) the tile stretches to it and keeps its buttons at
// the bottom edge.
func renderNappTile(
	gtx layout.Context,
	th *material.Theme,
	cardBtn,
	authorBtn,
	openBtn,
	btn,
	secondBtn *widget.Clickable,
	btnLabel,
	secondLabel string,
	napp backend.Napp,
) layout.Dimensions {
	authorName, authorPic := nappAuthor(napp)
	fill := gtx.Constraints.Min.Y > 0
	width := gtx.Constraints.Max.X

	button := func(b *widget.Clickable, label string, primary bool) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if b == nil || label == "" {
				return layout.Dimensions{}
			}
			return layout.Inset{Left: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				pointer.CursorPointer.Add(gtx.Ops)
				s := material.Button(th, b, label)
				if !primary {
					s.Background = currentTheme().suggestBg
					s.Color = currentTheme().suggestFg
				}
				s.TextSize = unit.Sp(13)
				s.Inset = layout.Inset{Top: unit.Dp(6), Bottom: unit.Dp(6), Left: unit.Dp(10), Right: unit.Dp(10)}
				return s.Layout(gtx)
			})
		})
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
				l := material.Body2(th, strings.Join(strings.Fields(napp.Description), " "))
				l.Color = currentTheme().subtle
				l.MaxLines = 3
				return l.Layout(gtx)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			var chips []layout.Widget
			for _, action := range napp.Actions {
				if strings.TrimSpace(action) == "" {
					continue
				}
				chips = append(chips, func(gtx layout.Context) layout.Dimensions {
					return actionChip(gtx, th, action)
				})
			}
			if len(chips) == 0 {
				return layout.Dimensions{}
			}
			return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return wrapFlow(gtx, unit.Dp(4), chips)
			})
		}),
	}
	if fill {
		children = append(children, layout.Flexed(1, layout.Spacer{}.Layout))
	}
	children = append(children,
		// the author on the left, the buttons on the right
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
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
					}),
					button(openBtn, "Open", false),
					button(secondBtn, secondLabel, true),
					button(btn, btnLabel, true),
				)
			})
		}),
	)

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
