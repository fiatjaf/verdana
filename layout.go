package main

import (
	"context"
	"image"
	"strings"

	"gioui.org/f32"
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

func setPhase(p string) {
	ui.mu.Lock()
	ui.phase = p
	ui.mu.Unlock()
	if gioWin != nil {
		gioWin.Invalidate()
	}
}

func setTab(t int) {
	ui.mu.Lock()
	ui.tab = t
	ui.mu.Unlock()
	if gioWin != nil {
		gioWin.Invalidate()
	}
}

// emph is an italic Verdana label for emphasis-ish subtle text.
func emph(l material.LabelStyle) material.LabelStyle {
	l.Font.Style = font.Italic
	return l
}

func parseRelays(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !strings.Contains(line, "://") {
			line = "wss://" + line
		}
		out = append(out, line)
	}
	return out
}

// layoutPrompt draws the dialog a blocked rpc is waiting on: either an
// approve/deny question or a list of napps that can handle an action.
func layoutPrompt(
	gtx layout.Context,
	th *material.Theme,
	p *prompt,
	approveBtn, denyBtn *widget.Clickable,
	optBtns []widget.Clickable,
) layout.Dimensions {
	children := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			t := material.H6(th, p.title)
			t.Font.Weight = font.Bold
			return t.Layout(gtx)
		}),
	}

	if p.detail != "" {
		children = append(children,
			layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				l := material.Body2(th, p.detail)
				l.Color = currentTheme().subtle
				return l.Layout(gtx)
			}),
		)
	}

	if p.code != "" {
		children = append(children,
			layout.Rigid(layout.Spacer{Height: unit.Dp(10)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				macro := op.Record(gtx.Ops)
				dims := layout.UniformInset(unit.Dp(8)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					l := material.Body2(th, p.code)
					l.Color = currentTheme().codeFg
					return l.Layout(gtx)
				})
				call := macro.Stop()
				bg := clip.RRect{
					Rect: image.Rectangle{Max: image.Point{X: gtx.Constraints.Max.X, Y: dims.Size.Y}},
					NW:   6, NE: 6, SW: 6, SE: 6,
				}
				defer bg.Push(gtx.Ops).Pop()
				paint.Fill(gtx.Ops, currentTheme().codeBg)
				call.Add(gtx.Ops)
				return dims
			}),
		)
	}

	children = append(children, layout.Rigid(layout.Spacer{Height: unit.Dp(16)}.Layout))

	if len(p.options) > 0 {
		for i := range p.options {
			i := i
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if i >= len(optBtns) {
					return layout.Dimensions{}
				}
				return layout.Inset{Bottom: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					pointer.CursorPointer.Add(gtx.Ops)
					label := p.options[i].label
					if p.options[i].detail != "" {
						label += " — " + preview(p.options[i].detail, 40)
					}
					b := material.Button(th, &optBtns[i], label)
					b.TextSize = unit.Sp(14)
					return b.Layout(gtx)
				})
			}))
		}
		children = append(children,
			layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				pointer.CursorPointer.Add(gtx.Ops)
				b := material.Button(th, denyBtn, "Cancel")
				b.Background = currentTheme().chipBg
				b.Color = currentTheme().chipFg
				return b.Layout(gtx)
			}),
		)
	} else {
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					pointer.CursorPointer.Add(gtx.Ops)
					return material.Button(th, approveBtn, "Allow").Layout(gtx)
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					pointer.CursorPointer.Add(gtx.Ops)
					b := material.Button(th, denyBtn, "Deny")
					b.Background = currentTheme().chipBg
					b.Color = currentTheme().chipFg
					return b.Layout(gtx)
				}),
			)
		}))
	}

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}

func layoutLogin(gtx layout.Context, th *material.Theme, ed *widget.Editor, btn *widget.Clickable, loginErr string) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			t := material.H5(th, "Log in to Verdana")
			t.Font.Weight = font.Bold
			return t.Layout(gtx)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			l := emph(material.Body2(th, "Paste your nsec or a bunker:// URL"))
			l.Color = currentTheme().subtle
			return l.Layout(gtx)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(12)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return editorBox(gtx, th, ed, "nsec1... or bunker://...")
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(12)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			pointer.CursorPointer.Add(gtx.Ops)
			return material.Button(th, btn, "Log in").Layout(gtx)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if loginErr == "" {
				return layout.Dimensions{}
			}
			return layout.Inset{Top: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				l := material.Body2(th, loginErr)
				l.Color = currentTheme().danger
				return l.Layout(gtx)
			})
		}),
	)
}

func layoutMain(gtx layout.Context, th *material.Theme, tabNappsBtn, tabDiscoBtn, themeBtn *widget.Clickable, tab int, installedList, discoveryList *widget.List, relaysEd *widget.Editor, fetchBtn *widget.Clickable, runBtns, actionBtns []widget.Clickable, profName, profPic, fetchErr string, fetching bool, installed, discovery []Napp, installedSet, busy map[string]bool) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layoutProfile(gtx, th, themeBtn, profName, profPic)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(16)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layoutTabs(gtx, th, tabNappsBtn, tabDiscoBtn, tab, len(installed), len(discovery))
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(12)}.Layout),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			if tab == 0 {
				return layoutNappsTab(gtx, th, installedList, runBtns, installed)
			}
			return layoutDiscoveryTab(gtx, th, discoveryList, relaysEd, fetchBtn, actionBtns,
				fetchErr, fetching, discovery, installedSet, busy)
		}),
	)
}

func layoutTabs(gtx layout.Context, th *material.Theme, nappsBtn, discoBtn *widget.Clickable, tab, nInstalled, nDiscovery int) layout.Dimensions {
	tabBtn := func(gtx layout.Context, btn *widget.Clickable, label string, active bool) layout.Dimensions {
		pointer.CursorPointer.Add(gtx.Ops)
		b := material.Button(th, btn, label)
		if active {
			b.Background = th.Palette.ContrastBg
		} else {
			b.Background = currentTheme().chipBg
			b.Color = currentTheme().chipFg
		}
		return b.Layout(gtx)
	}
	return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return tabBtn(gtx, nappsBtn, "Installed", tab == 0)
		}),
		layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return tabBtn(gtx, discoBtn, "Discovery", tab == 1)
		}),
	)
}

func layoutNappsTab(gtx layout.Context, th *material.Theme, list *widget.List, runBtns []widget.Clickable, installed []Napp) layout.Dimensions {
	if len(installed) == 0 {
		l := material.Body2(th, "No napps installed yet. Find some in the Discovery tab.")
		l.Color = currentTheme().muted
		return l.Layout(gtx)
	}
	return material.List(th, list).Layout(gtx, len(installed), func(gtx layout.Context, i int) layout.Dimensions {
		var btn *widget.Clickable
		if i < len(runBtns) {
			btn = &runBtns[i]
		}
		return renderNappCard(gtx, th, btn, "Open", installed[i])
	})
}

func layoutDiscoveryTab(gtx layout.Context, th *material.Theme, list *widget.List, relaysEd *widget.Editor, fetchBtn *widget.Clickable, actionBtns []widget.Clickable, fetchErr string, fetching bool, discovery []Napp, installedSet, busy map[string]bool) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			l := emph(material.Body2(th, "Relays (one per line)"))
			l.Color = currentTheme().subtle
			return l.Layout(gtx)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return editorBox(gtx, th, relaysEd, "relay.example.com")
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					pointer.CursorPointer.Add(gtx.Ops)
					label := "Fetch napps"
					if fetching {
						label = "Fetching\u2026"
					}
					return material.Button(th, fetchBtn, label).Layout(gtx)
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if fetchErr == "" {
						return layout.Dimensions{}
					}
					l := material.Body2(th, fetchErr)
					l.Color = currentTheme().danger
					return l.Layout(gtx)
				}),
			)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(12)}.Layout),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			if len(discovery) == 0 {
				msg := "No napps yet. Click \"Fetch napps\"."
				if fetching {
					msg = "Searching relays\u2026"
				}
				l := material.Body2(th, msg)
				l.Color = currentTheme().muted
				return l.Layout(gtx)
			}
			return material.List(th, list).Layout(gtx, len(discovery), func(gtx layout.Context, i int) layout.Dimensions {
				var btn *widget.Clickable
				if i < len(actionBtns) {
					btn = &actionBtns[i]
				}
				n := discovery[i]
				label := "Install"
				if installedSet[n.ID] {
					label = "Uninstall"
				}
				if busy[n.ID] {
					label = "Working\u2026"
				}
				return renderNappCard(gtx, th, btn, label, n)
			})
		}),
	)
}

func layoutProfile(gtx layout.Context, th *material.Theme, themeBtn *widget.Clickable, name, pic string) layout.Dimensions {
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return avatar(gtx, pic, 48)
		}),
		layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			t := material.H6(th, name)
			t.Font.Weight = font.Bold
			return t.Layout(gtx)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if themeBtn == nil {
				return layout.Dimensions{}
			}
			pointer.CursorPointer.Add(gtx.Ops)
			p := currentTheme()
			label := "\u263e Dark"
			if p.name == "dark" {
				label = "\u2600 Light"
			}
			b := material.Button(th, themeBtn, label)
			b.Background = p.chipBg
			b.Color = p.chipFg
			b.TextSize = unit.Sp(13)
			b.Inset = layout.UniformInset(unit.Dp(8))
			return b.Layout(gtx)
		}),
	)
}

func avatar(gtx layout.Context, url string, size int) layout.Dimensions {
	imgOp, ok := getImage(url)
	return imageSquare(gtx, size, imgOp, ok)
}

// nappIcon draws a napp's icon, or a plain square for the napps that declare
// none (and while one is still being fetched).
func nappIcon(gtx layout.Context, n Napp, size int) layout.Dimensions {
	imgOp, ok := nappIconImage(n)
	return imageSquare(gtx, size, imgOp, ok)
}

// imageSquare paints an image cropped to a rounded square, filling it with
// the theme's placeholder colour when there is nothing to paint yet.
func imageSquare(gtx layout.Context, size int, imgOp paint.ImageOp, ok bool) layout.Dimensions {
	px := gtx.Dp(unit.Dp(size))
	sq := image.Point{X: px, Y: px}
	defer clip.RRect{Rect: image.Rectangle{Max: sq}, NW: 6, NE: 6, SW: 6, SE: 6}.Push(gtx.Ops).Pop()
	if ok {
		isz := imgOp.Size()
		if isz.X > 0 && isz.Y > 0 {
			scale := float32(px) / float32(isz.X)
			if s := float32(px) / float32(isz.Y); s > scale {
				scale = s
			}
			defer op.Affine(f32.Affine2D{}.Scale(f32.Pt(0, 0), f32.Pt(scale, scale))).Push(gtx.Ops).Pop()
		}
		imgOp.Add(gtx.Ops)
		paint.PaintOp{}.Add(gtx.Ops)
	} else {
		paint.Fill(gtx.Ops, currentTheme().imageBg)
	}
	return layout.Dimensions{Size: sq}
}

func editorBox(gtx layout.Context, th *material.Theme, ed *widget.Editor, hint string) layout.Dimensions {
	border := widget.Border{
		Color:        currentTheme().border,
		CornerRadius: unit.Dp(6),
		Width:        unit.Dp(1),
	}
	return border.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.UniformInset(unit.Dp(8)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			style := material.Editor(th, ed, hint)
			style.HintColor = currentTheme().inputHnt
			return style.Layout(gtx)
		})
	})
}

func renderNappCard(gtx layout.Context, th *material.Theme, btn *widget.Clickable, btnLabel string, napp Napp) layout.Dimensions {
	pm := sys.FetchProfileMetadata(context.Background(), napp.Author)
	return layout.Inset{Bottom: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		sz := gtx.Constraints.Max
		macro := op.Record(gtx.Ops)
		dims := layout.Inset{
			Top: unit.Dp(12), Bottom: unit.Dp(12),
			Left: unit.Dp(12), Right: unit.Dp(12),
		}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Right: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return nappIcon(gtx, napp, 40)
					})
				}),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							label := material.Body1(th, napp.Name)
							label.Font.Weight = font.Bold
							return label.Layout(gtx)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							if napp.Description == "" {
								return layout.Dimensions{}
							}
							return layout.Inset{Top: unit.Dp(2)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								l := material.Body2(th, napp.Description)
								l.Color = currentTheme().subtle
								return l.Layout(gtx)
							})
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return layout.Inset{Top: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
									layout.Rigid(func(gtx layout.Context) layout.Dimensions {
										return avatar(gtx, pm.Picture, 18)
									}),
									layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
									layout.Rigid(func(gtx layout.Context) layout.Dimensions {
										c := material.Caption(th, pm.ShortName())
										c.Color = currentTheme().muted
										return c.Layout(gtx)
									}),
								)
							})
						}),
					)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if btn == nil {
						return layout.Dimensions{}
					}
					return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						pointer.CursorPointer.Add(gtx.Ops)
						b := material.Button(th, btn, btnLabel)
						b.TextSize = unit.Sp(13)
						b.Inset = layout.UniformInset(unit.Dp(8))
						return b.Layout(gtx)
					})
				}),
			)
		})
		call := macro.Stop()

		bg := clip.RRect{
			Rect: image.Rectangle{Max: image.Point{X: sz.X, Y: dims.Size.Y}},
			NW:   8, NE: 8, SW: 8, SE: 8,
		}
		defer bg.Push(gtx.Ops).Pop()
		paint.Fill(gtx.Ops, currentTheme().card)
		call.Add(gtx.Ops)
		return dims
	})
}
