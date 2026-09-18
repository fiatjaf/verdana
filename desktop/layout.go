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

	"verdana/backend"
)

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
	p *backend.Prompt,
	approveBtn, denyBtn *widget.Clickable,
	optBtns []widget.Clickable,
) layout.Dimensions {
	children := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			t := material.H6(th, p.Title)
			t.Font.Weight = font.Bold
			return t.Layout(gtx)
		}),
	}

	if p.Detail != "" {
		children = append(children,
			layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				l := material.Body2(th, p.Detail)
				l.Color = currentTheme().subtle
				return l.Layout(gtx)
			}),
		)
	}

	if p.Code != "" {
		children = append(children,
			layout.Rigid(layout.Spacer{Height: unit.Dp(10)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				macro := op.Record(gtx.Ops)
				dims := layout.UniformInset(unit.Dp(8)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					l := material.Body2(th, p.Code)
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

	if len(p.Options) > 0 {
		for i := range p.Options {
			i := i
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if i >= len(optBtns) {
					return layout.Dimensions{}
				}
				return layout.Inset{Bottom: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					pointer.CursorPointer.Add(gtx.Ops)
					opt := p.Options[i]
					p := currentTheme()
					return optBtns[i].Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						open := opt.Instance != ""
						bgColor, fgColor := p.chipBg, p.chipFg
						if opt.Dev {
							bgColor, fgColor = p.devBg, p.devFg
						} else if open {
							bgColor, fgColor = p.contrastBg, p.contrastFg
						}
						macro := op.Record(gtx.Ops)
						dims := layout.UniformInset(unit.Dp(10)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
								layout.Rigid(func(gtx layout.Context) layout.Dimensions {
									l := material.Body1(th, opt.Label)
									l.Color = fgColor
									return l.Layout(gtx)
								}),
								layout.Rigid(func(gtx layout.Context) layout.Dimensions {
									if opt.Detail == "" {
										return layout.Dimensions{}
									}
									l := material.Caption(th, truncate(opt.Detail, 80))
									if open || opt.Dev {
										l.Color = fgColor
									} else {
										l.Color = p.muted
									}
									return l.Layout(gtx)
								}),
							)
						})
						call := macro.Stop()
						bg := clip.RRect{Rect: image.Rectangle{Max: image.Point{X: gtx.Constraints.Max.X, Y: dims.Size.Y}}, NW: 6, NE: 6, SW: 6, SE: 6}
						defer bg.Push(gtx.Ops).Pop()
						paint.Fill(gtx.Ops, bgColor)
						call.Add(gtx.Ops)
						return dims
					})
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

func layoutMain(gtx layout.Context, th *material.Theme, tabNappsBtn, tabDiscoBtn, tabDevBtn, themeBtn, logoutBtn *widget.Clickable, tab int, installedList, discoveryList, devList *widget.List, relaysEd, filterEd, installedFilterEd, devURLed, devPathEd *widget.Editor, fetchBtn, checkUpdBtn, loadURLBtn, browseBtn, loadFolderBtn *widget.Clickable, cardBtns, uninstBtns, actionBtns, updateBtns, devOpenBtns, devUnloadBtns []widget.Clickable, vis, instVis []int, st backend.State, installedSet, busy map[string]bool) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layoutProfile(gtx, th, themeBtn, logoutBtn, st.ProfileName, st.ProfilePicture)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(16)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layoutTabs(gtx, th, tabNappsBtn, tabDiscoBtn, tabDevBtn, tab, len(st.Installed), len(st.Discovery))
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(12)}.Layout),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			if tab == 0 {
				return layoutNappsTab(gtx, th, installedList, installedFilterEd, cardBtns, uninstBtns, checkUpdBtn, instVis, st)
			}
			if tab == 1 {
				return layoutDiscoveryTab(gtx, th, discoveryList, relaysEd, filterEd, fetchBtn, actionBtns,
					updateBtns, vis, st.FetchErr, st.Fetching, st.Discovery, installedSet, busy)
			}
			return layoutDevTab(gtx, th, devList, devURLed, devPathEd, loadURLBtn, browseBtn, loadFolderBtn,
				devOpenBtns, devUnloadBtns, st)
		}),
	)
}

// truncate keeps a label short enough for a dialog line.
func truncate(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

func layoutTabs(gtx layout.Context, th *material.Theme, nappsBtn, discoBtn, devBtn *widget.Clickable, tab, nInstalled, nDiscovery int) layout.Dimensions {
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
	children := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return tabBtn(gtx, nappsBtn, "Installed", tab == 0)
		}),
		layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return tabBtn(gtx, discoBtn, "Discovery", tab == 1)
		}),
	}
	// devBtn is nil outside dev builds: no dev tab there
	if devBtn != nil {
		children = append(children,
			layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return tabBtn(gtx, devBtn, "Dev", tab == 2)
			}),
		)
	}
	return layout.Flex{Axis: layout.Horizontal}.Layout(gtx, children...)
}

func layoutNappsTab(gtx layout.Context, th *material.Theme, list *widget.List, filterEd *widget.Editor, cardBtns, uninstBtns []widget.Clickable, checkUpdBtn *widget.Clickable, vis []int, st backend.State) layout.Dimensions {
	if len(st.Installed) == 0 {
		l := material.Body2(th, "No napps installed yet. Find some in the Discovery tab.")
		l.Color = currentTheme().muted
		return l.Layout(gtx)
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		// the filter box, narrowing the entries below by name, author,
		// author name or description.
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return editorBox(gtx, th, filterEd, "filter by name, author or description")
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			if len(vis) == 0 {
				l := material.Body2(th, "Nothing matches the filter.")
				l.Color = currentTheme().muted
				return l.Layout(gtx)
			}
			return material.List(th, list).Layout(gtx, len(vis), func(gtx layout.Context, i int) layout.Dimensions {
				row := vis[i]
				var cardBtn, uninstBtn *widget.Clickable
				if row < len(cardBtns) {
					cardBtn = &cardBtns[row]
				}
				if row < len(uninstBtns) {
					uninstBtn = &uninstBtns[row]
				}
				// the card itself opens the napp: no open button
				return renderNappCard(gtx, th, cardBtn, nil, uninstBtn, "", "Uninstall", st.Installed[row])
			})
		}),
		// the update check button sits at the bottom of the list
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			pointer.CursorPointer.Add(gtx.Ops)
			label := "Check for updates"
			if st.UpdateCheckRunning {
				label = "Checking\u2026"
			}
			b := material.Button(th, checkUpdBtn, label)
			b.Background = currentTheme().chipBg
			b.Color = currentTheme().chipFg
			return b.Layout(gtx)
		}),
	)
}

func layoutDiscoveryTab(gtx layout.Context, th *material.Theme, list *widget.List, relaysEd, filterEd *widget.Editor, fetchBtn *widget.Clickable, actionBtns, updateBtns []widget.Clickable, vis []int, fetchErr string, fetching bool, discovery []backend.Napp, installedSet, busy map[string]bool) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		// the filter box comes first, narrowing the entries below by name,
		// author, author name or description.
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return editorBox(gtx, th, filterEd, "filter by name, author or description")
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
		// then the relays editor
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
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
			)
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
			if len(vis) == 0 {
				msg := "No napps yet. Click \"Fetch napps\"."
				if fetching {
					msg = "Searching relays\u2026"
				}
				if len(discovery) > 0 {
					msg = "Nothing matches the filter."
				}
				l := material.Body2(th, msg)
				l.Color = currentTheme().muted
				return l.Layout(gtx)
			}
			return material.List(th, list).Layout(gtx, len(vis), func(gtx layout.Context, i int) layout.Dimensions {
				row := vis[i]
				var btn, updBtn *widget.Clickable
				if row < len(actionBtns) {
					btn = &actionBtns[row]
				}
				if row < len(updateBtns) {
					updBtn = &updateBtns[row]
				}
				n := discovery[row]
				label := "Install"
				if installedSet[n.ID] {
					label = "Uninstall"
				}
				if busy[n.ID] {
					label = "Working\u2026"
				}
				updLabel := ""
				if installedSet[n.ID] && n.UpdateAvailable {
					updLabel = "Update"
				}
				return renderNappCard(gtx, th, nil, btn, updBtn, label, updLabel, n)
			})
		}),
	)
}

// layoutDevTab is the dev-build tab for loading ephemeral napps: either a
// dev-server url (used directly) or a local folder (served from disk by the
// throwaway server). The cards open on tap, like the
// installed tab's.
func layoutDevTab(gtx layout.Context, th *material.Theme, list *widget.List, urlEd, pathEd *widget.Editor, loadURLBtn, browseBtn, loadFolderBtn *widget.Clickable, openBtns, unloadBtns []widget.Clickable, st backend.State) layout.Dimensions {
	smallBtn := func(gtx layout.Context, btn *widget.Clickable, label string) layout.Dimensions {
		pointer.CursorPointer.Add(gtx.Ops)
		b := material.Button(th, btn, label)
		b.TextSize = unit.Sp(13)
		b.Inset = layout.UniformInset(unit.Dp(8))
		return b.Layout(gtx)
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		// a dev-server url, e.g. http://localhost:5173
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			l := emph(material.Body2(th, "Dev server URL"))
			l.Color = currentTheme().subtle
			return l.Layout(gtx)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return editorBox(gtx, th, urlEd, "http://localhost:5173")
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return smallBtn(gtx, loadURLBtn, "Load URL")
				}),
			)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(10)}.Layout),
		// a local folder carrying metadata.json next to its index.html
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			l := emph(material.Body2(th, "Napp folder"))
			l.Color = currentTheme().subtle
			return l.Layout(gtx)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return editorBox(gtx, th, pathEd, "/path/to/napp")
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return smallBtn(gtx, browseBtn, "Browse…")
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return smallBtn(gtx, loadFolderBtn, "Load folder")
				}),
			)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			msg := ""
			if st.DevErr != "" {
				l := material.Body2(th, st.DevErr)
				l.Color = currentTheme().danger
				return l.Layout(gtx)
			}
			if st.DevLoading {
				msg = "Loading…"
			} else if len(st.Dev) == 0 {
				msg = "No dev napps loaded. They are ephemeral: gone when the launcher quits."
			}
			if msg == "" {
				return layout.Dimensions{}
			}
			l := material.Body2(th, msg)
			l.Color = currentTheme().muted
			return l.Layout(gtx)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			if len(st.Dev) == 0 {
				return layout.Dimensions{}
			}
			return material.List(th, list).Layout(gtx, len(st.Dev), func(gtx layout.Context, i int) layout.Dimensions {
				var openBtn, unloadBtn *widget.Clickable
				if i < len(openBtns) {
					openBtn = &openBtns[i]
				}
				if i < len(unloadBtns) {
					unloadBtn = &unloadBtns[i]
				}
				return renderNappCard(gtx, th, openBtn, nil, unloadBtn, "", "Unload", st.Dev[i])
			})
		}),
	)
}

// layoutConfirmLogout is the dialog shown when the user hits "Log out":
// logging out closes every open napp, so it deserves a second look.
func layoutConfirmLogout(gtx layout.Context, th *material.Theme, yesBtn, noBtn *widget.Clickable) layout.Dimensions {
	p := currentTheme()
	return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		macro := op.Record(gtx.Ops)
		dims := layout.UniformInset(unit.Dp(20)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					t := material.H6(th, "Log out?")
					t.Font.Weight = font.Bold
					return t.Layout(gtx)
				}),
				layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					l := material.Body2(th, "This closes every open napp and forgets the key on this device.")
					l.Color = p.subtle
					return l.Layout(gtx)
				}),
				layout.Rigid(layout.Spacer{Height: unit.Dp(16)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							pointer.CursorPointer.Add(gtx.Ops)
							b := material.Button(th, yesBtn, "Log out")
							b.Background = p.chipBg
							b.Color = p.danger
							return b.Layout(gtx)
						}),
						layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							pointer.CursorPointer.Add(gtx.Ops)
							b := material.Button(th, noBtn, "Cancel")
							b.Background = p.chipBg
							b.Color = p.chipFg
							return b.Layout(gtx)
						}),
					)
				}),
			)
		})
		call := macro.Stop()
		// card behind the dialog, like the prompt dialogs
		bg := clip.RRect{
			Rect: image.Rectangle{Max: image.Point{X: dims.Size.X, Y: dims.Size.Y}},
			NW:   10, NE: 10, SW: 10, SE: 10,
		}
		defer bg.Push(gtx.Ops).Pop()
		paint.Fill(gtx.Ops, p.card)
		call.Add(gtx.Ops)
		return dims
	})
}

func layoutProfile(gtx layout.Context, th *material.Theme, themeBtn, logoutBtn *widget.Clickable, name, pic string) layout.Dimensions {
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
		layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if logoutBtn == nil {
				return layout.Dimensions{}
			}
			pointer.CursorPointer.Add(gtx.Ops)
			p := currentTheme()
			b := material.Button(th, logoutBtn, "Log out")
			b.Background = p.chipBg
			b.Color = p.danger
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
func nappIcon(gtx layout.Context, n backend.Napp, size int) layout.Dimensions {
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

// renderNappCard draws one napp row: icon, name, description, author and the
// action buttons. When cardBtn is not nil the whole card is clickable (the
// installed tab taps it to open the napp); buttons drawn on top of the card's
// area keep working, so the frame handler must check which of them fired
// before acting on the card itself.
func renderNappCard(gtx layout.Context, th *material.Theme, cardBtn, btn, secondBtn *widget.Clickable, btnLabel, secondLabel string, napp backend.Napp) layout.Dimensions {
	authorName, authorPic := napp.AuthorProfile(context.Background())
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
										return avatar(gtx, authorPic, 18)
									}),
									layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
									layout.Rigid(func(gtx layout.Context) layout.Dimensions {
										c := material.Caption(th, authorName)
										c.Color = currentTheme().muted
										return c.Layout(gtx)
									}),
								)
							})
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							// an update-available badge replaces the old
							// "Open — update available!" button label
							if cardBtn == nil || !napp.UpdateAvailable {
								return layout.Dimensions{}
							}
							return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								c := material.Caption(th, "update available — reinstall it in the Discovery tab")
								c.Color = currentTheme().danger
								return c.Layout(gtx)
							})
						}),
					)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if secondBtn != nil && secondLabel != "" {
						pointer.CursorPointer.Add(gtx.Ops)
						ub := material.Button(th, secondBtn, secondLabel)
						ub.TextSize = unit.Sp(13)
						ub.Inset = layout.UniformInset(unit.Dp(8))
						return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, ub.Layout)
					}
					return layout.Dimensions{}
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
		if cardBtn == nil {
			defer bg.Push(gtx.Ops).Pop()
			paint.Fill(gtx.Ops, currentTheme().card)
			call.Add(gtx.Ops)
			return dims
		}
		// a clickable card: the card itself fills its click area, and the
		// painted content replays on top of that (same pattern gio's
		// material buttons use).
		pointer.CursorPointer.Add(gtx.Ops)
		return cardBtn.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			defer bg.Push(gtx.Ops).Pop()
			paint.Fill(gtx.Ops, currentTheme().card)
			call.Add(gtx.Ops)
			return layout.Dimensions{Size: image.Point{X: sz.X, Y: dims.Size.Y}}
		})
	})
}
