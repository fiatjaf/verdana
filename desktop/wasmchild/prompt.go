package main

import (
	"image"
	"image/color"

	"gioui.org/font"
	"gioui.org/font/gofont"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"verdana/backend/wasmhost"

	"fiatjaf.com/verdana/desktop/tooltip"
)

// A prompt fired by this napp (signing, publishing, picking a handler…) is
// shown over its own window, as the webview shell does it: an opaque cover
// that hides the canvas until the user answers. The launcher decides what is
// asked and remembers the answer; this only draws the question.

// promptButton is one answer the overlay offers.
type promptButton struct {
	label  string
	detail string
	tip    string
	tone   string
	open   bool // a window already open: striped and marked, whatever its tone
	ok     bool
	index  int
	scope  string
	click  widget.Clickable
}

type promptOverlay struct {
	th      *material.Theme
	shownID int
	buttons []*promptButton
	// rows groups buttons laid side by side (an approval's scopes); a
	// picker's options are one per row.
	rows [][]*promptButton
	list widget.List
	tips *tooltip.Layer
	// fold opens and closes the payload when it comes with a summary
	fold     widget.Clickable
	foldOpen bool
	// ticks are the boxes of the prompt's items, ticked to start with
	ticks []widget.Bool
}

func newPromptOverlay() *promptOverlay {
	th := material.NewTheme()
	th.Shaper = text.NewShaper(text.WithCollection(gofont.Collection()))
	p := &promptOverlay{th: th, tips: tooltip.New()}
	p.list.Axis = layout.Vertical
	return p
}

// build makes the buttons for a prompt the first time it is shown, so their
// click state survives from frame to frame.
func (p *promptOverlay) build(pv *wasmhost.Prompt) {
	if p.shownID == pv.ID && p.buttons != nil {
		return
	}
	p.shownID = pv.ID
	p.buttons, p.rows = nil, nil
	p.foldOpen = false
	p.ticks = make([]widget.Bool, len(pv.Items))
	for i := range p.ticks {
		p.ticks[i].Value = true
	}
	add := func(row ...*promptButton) {
		p.rows = append(p.rows, row)
		p.buttons = append(p.buttons, row...)
	}
	btn := func(label, tone string, ok bool, index int, scope string) *promptButton {
		return &promptButton{label: label, tone: tone, ok: ok, index: index, scope: scope, tip: promptTip(label, ok, scope)}
	}

	switch {
	case len(pv.Options) > 0:
		for i, opt := range pv.Options {
			tone := "chip"
			switch {
			case opt.Suggested:
				tone = "suggest"
			case opt.Dev:
				tone = "dev"
			}
			b := btn(opt.Label, tone, true, i, "once")
			b.detail = opt.Detail
			b.open = opt.Instance != ""
			b.tip = "Open " + opt.Label + " to handle this"
			if b.open {
				b.tip = "Send this to the " + opt.Label + " window that is already open"
			}
			add(b)
		}
		add(btn("Cancel", "chip", false, 0, "once"))
	case pv.Remember:
		add(btn("Allow", "accent", true, 0, "once"), btn("Deny", "chip", false, 0, "once"))
		add(btn("Allow this session", "chip", true, 0, "session"), btn("Deny this session", "chip", false, 0, "session"))
		add(btn("Always allow", "chip", true, 0, "always"), btn("Always deny", "chip", false, 0, "always"))
	default:
		add(btn("Allow", "accent", true, 0, "once"))
		add(btn("Deny", "chip", false, 0, "once"))
	}
}

// promptTip is what an answer button says it does, on hover.
func promptTip(label string, ok bool, scope string) string {
	if label == "Cancel" {
		return "Don't send this anywhere"
	}
	switch {
	case ok && scope == "session":
		return "Allow it without asking again until the launcher quits"
	case !ok && scope == "session":
		return "Refuse it without asking again until the launcher quits"
	case ok && scope == "always":
		return "Allow it from now on, without ever asking again"
	case !ok && scope == "always":
		return "Refuse it from now on, without ever asking again"
	case ok:
		return "Allow it this time only"
	}
	return "Refuse it this time only"
}

// palette is the overlay's colours for the launcher's current theme.
type palette struct {
	bg, card, fg, muted, accent, accentText, dev, devText, suggest, suggestText, danger color.NRGBA
}

func rgb(c uint32) color.NRGBA {
	return color.NRGBA{R: uint8(c >> 16), G: uint8(c >> 8), B: uint8(c), A: 0xff}
}

func paletteFor(theme string) palette {
	if theme == "dark" {
		return palette{
			bg: rgb(0x17181b), card: rgb(0x23252b), fg: rgb(0xe8e8ea), muted: rgb(0x7d818a),
			accent: rgb(0x5c6bc0), accentText: rgb(0xffffff), dev: rgb(0x5a3b1a), devText: rgb(0xffd79a),
			suggest: rgb(0x1e3a2a), suggestText: rgb(0xb6e3c1), danger: rgb(0xff6b6b),
		}
	}
	return palette{
		bg: rgb(0xffffff), card: rgb(0xf2f2f2), fg: rgb(0x000000), muted: rgb(0x999999),
		accent: rgb(0x3f51b5), accentText: rgb(0xffffff), dev: rgb(0xffe5b4), devText: rgb(0x704000),
		suggest: rgb(0xd8efdc), suggestText: rgb(0x14532d), danger: rgb(0xcc2222),
	}
}

func (pal palette) tone(t string) (bg, fg color.NRGBA) {
	switch t {
	case "accent":
		return pal.accent, pal.accentText
	case "dev":
		return pal.dev, pal.devText
	case "suggest":
		return pal.suggest, pal.suggestText
	}
	return pal.card, pal.fg
}

// Layout draws the prompt over the whole window and reports a click on one
// of its answers through answer.
func (p *promptOverlay) Layout(gtx layout.Context, pv *wasmhost.Prompt, theme string,
	answer func(id int, ok bool, index int, scope string, skip []int)) layout.Dimensions {
	p.build(pv)
	if p.fold.Clicked(gtx) {
		p.foldOpen = !p.foldOpen
	}
	for _, b := range p.buttons {
		if b.click.Clicked(gtx) {
			var skip []int
			for i := range p.ticks {
				if !p.ticks[i].Value {
					skip = append(skip, i)
				}
			}
			answer(pv.ID, b.ok, b.index, b.scope, skip)
			return layout.Dimensions{Size: gtx.Constraints.Max}
		}
	}

	pal := paletteFor(theme)
	th := p.th
	th.Palette.Bg, th.Palette.Fg = pal.bg, pal.fg
	paint.Fill(gtx.Ops, pal.bg)

	var children []layout.Widget
	children = append(children, func(gtx layout.Context) layout.Dimensions {
		l := material.H6(th, pv.Title)
		l.Font.Weight = font.Bold
		return layout.Inset{Bottom: 10}.Layout(gtx, l.Layout)
	})
	if pv.Detail != "" {
		children = append(children, func(gtx layout.Context) layout.Dimensions {
			l := material.Body1(th, pv.Detail)
			l.Color = pal.muted
			return layout.Inset{Bottom: 14}.Layout(gtx, l.Layout)
		})
	}
	if len(pv.Items) > 0 {
		children = append(children, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: 14}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return card(gtx, pal.card, func(gtx layout.Context) layout.Dimensions {
					rows := make([]layout.FlexChild, 0, len(pv.Items))
					for i, it := range pv.Items {
						rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							cb := material.CheckBox(th, &p.ticks[i], it)
							cb.Font.Typeface = "Go Mono"
							cb.TextSize = th.TextSize * 14 / 16
							cb.Color = pal.fg
							cb.IconColor = pal.accent
							return cb.Layout(gtx)
						}))
					}
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
				})
			})
		})
	}
	if pv.Warning != "" {
		children = append(children, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: 14}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				l := material.Body1(th, pv.Warning)
				l.Color = pal.danger
				l.Font.Weight = font.Bold
				return l.Layout(gtx)
			})
		})
	}
	if pv.Code != "" && pv.Summary != "" {
		// the payload folded under its summary, opened by clicking that
		children = append(children, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: 6}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return p.fold.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					arrow := "▸ "
					if p.foldOpen {
						arrow = "▾ "
					}
					l := material.Body2(th, arrow+pv.Summary)
					l.Font.Typeface = "Go Mono"
					l.Color = pal.muted
					return l.Layout(gtx)
				})
			})
		})
	}
	if pv.Code != "" && (pv.Summary == "" || p.foldOpen) {
		children = append(children, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: 14}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return card(gtx, pal.card, func(gtx layout.Context) layout.Dimensions {
					l := material.Body2(th, pv.Code)
					l.Font.Typeface = "Go Mono"
					if pv.Summary == "" {
						l.MaxLines = 8
					}
					return l.Layout(gtx)
				})
			})
		})
	}
	for _, row := range p.rows {
		children = append(children, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				flex := make([]layout.FlexChild, 0, len(row)*2)
				for i, b := range row {
					if i > 0 {
						flex = append(flex, layout.Rigid(layout.Spacer{Width: 8}.Layout))
					}
					flex = append(flex, layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return p.button(gtx, pal, b)
					}))
				}
				return layout.Flex{}.Layout(gtx, flex...)
			})
		})
	}

	dims := p.tips.Track(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.UniformInset(24).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				if max := gtx.Dp(520); gtx.Constraints.Max.X > max {
					gtx.Constraints.Max.X = max
				}
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return material.List(th, &p.list).Layout(gtx, len(children), func(gtx layout.Context, i int) layout.Dimensions {
					return children[i](gtx)
				})
			})
		})
	})
	p.tips.Draw(gtx, th, pal.fg, pal.bg)
	return dims
}

func (p *promptOverlay) button(gtx layout.Context, pal palette, b *promptButton) layout.Dimensions {
	return p.tips.With(gtx, &b.click, b.tip, func(gtx layout.Context) layout.Dimensions {
		return p.buttonBody(gtx, pal, b)
	})
}

func (p *promptOverlay) buttonBody(gtx layout.Context, pal palette, b *promptButton) layout.Dimensions {
	bg, fg := pal.tone(b.tone)
	return material.ButtonLayoutStyle{
		Background:   bg,
		CornerRadius: 8,
		Button:       &b.click,
	}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		dims := layout.Inset{Top: 10, Bottom: 10, Left: 16, Right: 14}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							l := material.Body1(p.th, b.label)
							l.Color = fg
							if b.open {
								l.Font.Weight = font.Bold
							}
							return l.Layout(gtx)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							if b.detail == "" {
								return layout.Dimensions{}
							}
							l := material.Caption(p.th, b.detail)
							l.Color = fg
							l.Color.A = 0xc0
							return layout.Inset{Top: 3}.Layout(gtx, l.Layout)
						}),
					)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if !b.open {
						return layout.Dimensions{}
					}
					return layout.Inset{Left: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return layout.Background{}.Layout(gtx,
							func(gtx layout.Context) layout.Dimensions {
								sz := gtx.Constraints.Min
								defer clip.UniformRRect(image.Rectangle{Max: sz}, gtx.Dp(9)).Push(gtx.Ops).Pop()
								paint.Fill(gtx.Ops, pal.accent)
								return layout.Dimensions{Size: sz}
							},
							func(gtx layout.Context) layout.Dimensions {
								return layout.Inset{Top: 2, Bottom: 2, Left: 8, Right: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									l := material.Caption(p.th, "open")
									l.Color = pal.accentText
									l.Font.Weight = font.Bold
									return l.Layout(gtx)
								})
							},
						)
					})
				}),
			)
		})
		if b.open {
			// the button doesn't clip what it holds, so the stripe follows its
			// rounded corners by itself
			defer clip.UniformRRect(image.Rectangle{Max: dims.Size}, gtx.Dp(8)).Push(gtx.Ops).Pop()
			defer clip.Rect{Max: image.Point{X: gtx.Dp(5), Y: dims.Size.Y}}.Push(gtx.Ops).Pop()
			paint.Fill(gtx.Ops, pal.accent)
		}
		return dims
	})
}

// card draws w on a rounded block of colour bg.
func card(gtx layout.Context, bg color.NRGBA, w layout.Widget) layout.Dimensions {
	return layout.Background{}.Layout(gtx,
		func(gtx layout.Context) layout.Dimensions {
			sz := gtx.Constraints.Min
			defer clip.UniformRRect(image.Rectangle{Max: sz}, gtx.Dp(8)).Push(gtx.Ops).Pop()
			paint.Fill(gtx.Ops, bg)
			return layout.Dimensions{Size: sz}
		},
		func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return layout.UniformInset(unit.Dp(10)).Layout(gtx, w)
		},
	)
}
