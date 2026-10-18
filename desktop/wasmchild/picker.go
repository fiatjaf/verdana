package main

import (
	"bytes"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"strings"
	"unicode/utf8"

	"gioui.org/f32"
	"gioui.org/font"
	"gioui.org/font/gofont"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"verdana/backend/wasmhost"

	"fiatjaf.com/verdana/desktop/tooltip"
)

// A picker is this window before it runs any napp: the launcher opened it to
// ask which napp handles an action, and it shows the napps that could as a
// grid of icons, the way the launcher's home tab shows the pinned ones. The
// windows already open come first, as they do in the launcher's answer
// order. Once the user picks, the launcher either closes this window or
// sends it the napp to become (see main.go).

const (
	// pickerCellDp is the width of one cell, pickerIconDp its icon's size.
	pickerCellDp = 96
	pickerIconDp = 56

	// pickerFilterOver is how many choices there have to be before the
	// picker offers a box to filter them by name.
	pickerFilterOver = 4
)

// pickerCell is one napp, or one open window, to pick.
type pickerCell struct {
	index  int
	name   string
	napp   string // the napp's id, which the filter also looks at
	window string // the instance, for a window already open
	tone   string
	tip    string
	icon   paint.ImageOp
	iconOK bool
	click  widget.Clickable
}

type pickerView struct {
	th      *material.Theme
	tips    *tooltip.Layer
	list    widget.List
	shownID int

	open, fresh []*pickerCell
	cancel      widget.Clickable

	// filter narrows the cells down by name, when there are enough of
	// them to need it
	filter     widget.Editor
	filtering  bool
	focusAsked bool

	fold     widget.Clickable
	foldOpen bool

	// opening is the name of what was just picked, shown until the launcher
	// does something about it
	opening string
}

func newPickerView() *pickerView {
	th := material.NewTheme()
	th.Shaper = text.NewShaper(text.WithCollection(gofont.Collection()))
	p := &pickerView{th: th, tips: tooltip.New()}
	p.list.Axis = layout.Vertical
	p.filter.SingleLine = true
	p.filter.Submit = true
	return p
}

// build makes the cells for a prompt the first time it is shown, so their
// click state survives from frame to frame and each icon is decoded once.
func (p *pickerView) build(pv *wasmhost.Prompt) {
	if p.shownID == pv.ID {
		return
	}
	p.shownID = pv.ID
	p.open, p.fresh = nil, nil
	p.foldOpen = false
	p.opening = ""
	p.filter.SetText("")
	p.filtering = len(pv.Options) > pickerFilterOver
	p.focusAsked = false
	for i, opt := range pv.Options {
		c := &pickerCell{index: i, name: opt.Name, napp: opt.Detail, window: opt.Instance}
		if c.name == "" {
			c.name = opt.Label
		}
		if opt.Dev {
			c.tone = "dev"
		}
		if len(opt.Icon) > 0 {
			if img, _, err := image.Decode(bytes.NewReader(opt.Icon)); err == nil {
				c.icon, c.iconOK = paint.NewImageOp(img), true
			}
		}
		if c.window != "" {
			c.tip = "Send this to " + c.name + " in window " + c.window + ", already open"
			p.open = append(p.open, c)
		} else {
			c.tip = "Open " + c.name + " to handle this"
			if opt.Detail != "" && opt.Detail != c.name {
				c.tip += " (" + opt.Detail + ")"
			}
			p.fresh = append(p.fresh, c)
		}
	}
}

// Layout draws the picker over the whole window and reports a click on a
// napp (ok, and its index among the prompt's options) or on Cancel (not ok)
// through answer. With no prompt it is the window waiting: for the question
// to arrive, or for the launcher to act on the answer.
func (p *pickerView) Layout(gtx layout.Context, pv *wasmhost.Prompt, theme string,
	answer func(id int, ok bool, index int)) layout.Dimensions {
	pal := paletteFor(theme)
	th := p.th
	th.Palette.Bg, th.Palette.Fg = pal.bg, pal.fg
	paint.Fill(gtx.Ops, pal.bg)

	if pv == nil {
		msg := "…"
		if p.opening != "" {
			msg = "Opening " + p.opening + "…"
		}
		return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			l := material.Body1(th, msg)
			l.Color = pal.muted
			return l.Layout(gtx)
		})
	}

	p.build(pv)
	pick := func(c *pickerCell) {
		p.opening = c.name
		answer(pv.ID, true, c.index)
	}

	// the filter has the keyboard from the start, so typing narrows the
	// cells down right away; Enter in it picks the first one left
	if p.filtering {
		if !p.focusAsked {
			p.focusAsked = true
			gtx.Execute(key.FocusCmd{Tag: &p.filter})
		}
		for {
			ev, ok := p.filter.Update(gtx)
			if !ok {
				break
			}
			if _, ok := ev.(widget.SubmitEvent); ok {
				if first := p.first(); first != nil {
					pick(first)
					return layout.Dimensions{Size: gtx.Constraints.Max}
				}
			}
		}
	}
	open, fresh := p.visible(p.open), p.visible(p.fresh)

	for _, c := range append(append([]*pickerCell(nil), p.open...), p.fresh...) {
		if c.click.Clicked(gtx) {
			pick(c)
			return layout.Dimensions{Size: gtx.Constraints.Max}
		}
	}
	if p.cancel.Clicked(gtx) {
		answer(pv.ID, false, 0)
		return layout.Dimensions{Size: gtx.Constraints.Max}
	}
	// Escape is Cancel, Enter the napp the user picks most for this
	// action, which the launcher put first
	for {
		ev, ok := gtx.Event(key.Filter{Name: key.NameEscape}, key.Filter{Name: key.NameReturn}, key.Filter{Name: key.NameEnter})
		if !ok {
			break
		}
		if e, ok := ev.(key.Event); ok && e.State == key.Press {
			if e.Name == key.NameEscape {
				answer(pv.ID, false, 0)
				return layout.Dimensions{Size: gtx.Constraints.Max}
			}
			if first := p.first(); first != nil {
				pick(first)
				return layout.Dimensions{Size: gtx.Constraints.Max}
			}
		}
	}
	if p.fold.Clicked(gtx) {
		p.foldOpen = !p.foldOpen
	}

	var children []layout.Widget
	children = append(children, func(gtx layout.Context) layout.Dimensions {
		l := material.H6(th, pv.Title)
		l.Font.Weight = font.Bold
		return layout.Inset{Bottom: 6}.Layout(gtx, l.Layout)
	})
	if pv.Code != "" {
		// what is being sent, folded: it is rarely what the user picks by
		children = append(children, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return p.fold.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					pointer.CursorPointer.Add(gtx.Ops)
					arrow := "▸ "
					if p.foldOpen {
						arrow = "▾ "
					}
					l := material.Body2(th, arrow+"what is being sent")
					l.Color = pal.muted
					return l.Layout(gtx)
				})
			})
		})
		if p.foldOpen {
			children = append(children, func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: 4, Bottom: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return card(gtx, pal.card, func(gtx layout.Context) layout.Dimensions {
						l := material.Body2(th, pv.Code)
						l.Font.Typeface = "Go Mono"
						l.MaxLines = 8
						return l.Layout(gtx)
					})
				})
			})
		}
	}
	section := func(title string, cells []*pickerCell) {
		if len(cells) == 0 {
			return
		}
		children = append(children, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: 14, Bottom: 6}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				l := material.Caption(th, strings.ToUpper(title))
				l.Color = pal.muted
				l.Font.Weight = font.Bold
				return l.Layout(gtx)
			})
		})
		children = append(children, func(gtx layout.Context) layout.Dimensions {
			return p.grid(gtx, pal, cells)
		})
	}
	if p.filtering {
		children = append(children, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return widget.Border{Color: pal.border, CornerRadius: 6, Width: 1}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.UniformInset(8).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						ed := material.Editor(th, &p.filter, "filter by name")
						ed.HintColor = pal.muted
						return ed.Layout(gtx)
					})
				})
			})
		})
	}
	section("Already open", open)
	section("Open in a new window", fresh)
	if len(open)+len(fresh) == 0 {
		children = append(children, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: 18}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				l := material.Body2(th, "No napp matches “"+strings.TrimSpace(p.filter.Text())+"”")
				l.Color = pal.muted
				return l.Layout(gtx)
			})
		})
	}
	children = append(children, func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: 18}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.E.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return p.tips.With(gtx, &p.cancel, "Don't send this anywhere (Esc)", func(gtx layout.Context) layout.Dimensions {
					return material.ButtonLayoutStyle{
						Background:   pal.card,
						CornerRadius: 8,
						Button:       &p.cancel,
					}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Top: 8, Bottom: 8, Left: 18, Right: 18}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							l := material.Body1(th, "Cancel")
							l.Color = pal.fg
							return l.Layout(gtx)
						})
					})
				})
			})
		})
	})

	dims := p.tips.Track(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.UniformInset(20).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return material.List(th, &p.list).Layout(gtx, len(children), func(gtx layout.Context, i int) layout.Dimensions {
				return children[i](gtx)
			})
		})
	})
	p.tips.Draw(gtx, th, pal.fg, pal.bg)
	return dims
}

// visible is the cells the filter leaves, all of them with no filter.
func (p *pickerView) visible(cells []*pickerCell) []*pickerCell {
	q := strings.ToLower(strings.TrimSpace(p.filter.Text()))
	if !p.filtering || q == "" {
		return cells
	}
	out := make([]*pickerCell, 0, len(cells))
	for _, c := range cells {
		if strings.Contains(strings.ToLower(c.name), q) || strings.Contains(strings.ToLower(c.napp), q) ||
			strings.HasPrefix(c.window, q) {
			out = append(out, c)
		}
	}
	return out
}

// first is the option the launcher put first among those the filter leaves:
// the habitual handler when there is one.
func (p *pickerView) first() *pickerCell {
	var best *pickerCell
	for _, c := range append(p.visible(p.open), p.visible(p.fresh)...) {
		if best == nil || c.index < best.index {
			best = c
		}
	}
	return best
}

// grid lays cells out in as many columns as fit.
func (p *pickerView) grid(gtx layout.Context, pal palette, cells []*pickerCell) layout.Dimensions {
	cols := max(1, gtx.Constraints.Max.X/gtx.Dp(pickerCellDp))
	rows := make([]layout.FlexChild, 0, (len(cells)+cols-1)/cols)
	for start := 0; start < len(cells); start += cols {
		row := cells[start:min(start+cols, len(cells))]
		rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			flex := make([]layout.FlexChild, 0, len(row))
			for _, c := range row {
				flex = append(flex, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return p.cell(gtx, pal, c)
				}))
			}
			return layout.Flex{}.Layout(gtx, flex...)
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
}

// cell draws one choice: its icon with its name under it (and the window,
// for one already open), on its tone's colour, or the card's when hovered.
func (p *pickerView) cell(gtx layout.Context, pal palette, c *pickerCell) layout.Dimensions {
	return p.tips.With(gtx, &c.click, c.tip, func(gtx layout.Context) layout.Dimensions {
		return c.click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			pointer.CursorPointer.Add(gtx.Ops)
			h := 112
			if c.window != "" {
				h = 128
			}
			sz := image.Pt(gtx.Dp(pickerCellDp), gtx.Dp(unit.Dp(h)))
			gtx.Constraints = layout.Exact(sz)

			bg, fg := pal.tone(c.tone)
			if c.tone == "" {
				bg.A = 0
				if c.click.Hovered() {
					bg = pal.card
				}
			} else if c.click.Hovered() {
				bg = mix(bg, pal.fg, 0.08)
			}
			if bg.A > 0 {
				r := gtx.Dp(10)
				area := clip.UniformRRect(image.Rectangle{Max: sz}, r).Push(gtx.Ops)
				paint.Fill(gtx.Ops, bg)
				area.Pop()
			}

			layout.UniformInset(8).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.Y = 0
				return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return p.icon(gtx, pal, c)
					}),
					layout.Rigid(layout.Spacer{Height: 6}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						l := material.Caption(p.th, c.name)
						l.Color = fg
						l.Alignment = text.Middle
						l.MaxLines = 2
						return l.Layout(gtx)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if c.window == "" {
							return layout.Dimensions{}
						}
						// the heading already says these are windows
						l := material.Caption(p.th, c.window)
						l.TextSize = p.th.TextSize * 11 / 16
						l.Color = pal.muted
						l.Alignment = text.Middle
						l.MaxLines = 1
						return layout.Inset{Top: 2}.Layout(gtx, l.Layout)
					}),
				)
			})
			return layout.Dimensions{Size: sz}
		})
	})
}

// icon is the napp's icon cropped to a rounded square, or its initial on a
// tile when it has none.
func (p *pickerView) icon(gtx layout.Context, pal palette, c *pickerCell) layout.Dimensions {
	px := gtx.Dp(pickerIconDp)
	sq := image.Pt(px, px)
	defer clip.UniformRRect(image.Rectangle{Max: sq}, gtx.Dp(10)).Push(gtx.Ops).Pop()
	if c.iconOK {
		if isz := c.icon.Size(); isz.X > 0 && isz.Y > 0 {
			scale := max(float32(px)/float32(isz.X), float32(px)/float32(isz.Y))
			off := f32.Pt((float32(px)-float32(isz.X)*scale)/2, (float32(px)-float32(isz.Y)*scale)/2)
			t := op.Affine(f32.AffineId().Scale(f32.Point{}, f32.Pt(scale, scale)).Offset(off)).Push(gtx.Ops)
			c.icon.Add(gtx.Ops)
			paint.PaintOp{}.Add(gtx.Ops)
			t.Pop()
			return layout.Dimensions{Size: sq}
		}
	}
	paint.Fill(gtx.Ops, mix(pal.card, pal.fg, 0.06))
	initial, _ := utf8.DecodeRuneInString(strings.TrimSpace(c.name))
	if initial == utf8.RuneError {
		initial = '?'
	}
	gtx.Constraints = layout.Exact(sq)
	return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		l := material.H5(p.th, strings.ToUpper(string(initial)))
		l.Color = pal.muted
		l.Font.Weight = font.Bold
		return l.Layout(gtx)
	})
}

// mix is a with t of b in it.
func mix(a, b color.NRGBA, t float32) color.NRGBA {
	l := func(x, y uint8) uint8 { return uint8(float32(x)*(1-t) + float32(y)*t) }
	return color.NRGBA{R: l(a.R, b.R), G: l(a.G, b.G), B: l(a.B, b.B), A: a.A}
}
