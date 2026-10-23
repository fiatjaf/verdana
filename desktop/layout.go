package main

import (
	"image"
	"strings"
	"verdana/backend"

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

// promptButtons are the six answers an approval prompt can get: allow and
// deny, each of them for this prompt only, for this session or always.
type promptButtons struct {
	allow, deny               widget.Clickable
	sessionAllow, sessionDeny widget.Clickable
	alwaysAllow, alwaysDeny   widget.Clickable

	// fold opens and closes the payload of the prompt shownFor, when it
	// comes with a summary, and ticks are the boxes of its items, ticked
	// to start with
	fold     widget.Clickable
	foldOpen bool
	ticks    []widget.Bool
	shownFor int
}

// reset makes the buttons ready for prompt p, the first time it is drawn.
func (btns *promptButtons) reset(p *backend.Prompt) {
	if btns.shownFor == p.ID && len(btns.ticks) == len(p.Items) {
		return
	}
	btns.shownFor, btns.foldOpen = p.ID, false
	btns.ticks = make([]widget.Bool, len(p.Items))
	for i := range btns.ticks {
		btns.ticks[i].Value = true
	}
}

// skipped is the indexes of the items the user unticked.
func (btns *promptButtons) skipped() []int {
	var skip []int
	for i := range btns.ticks {
		if !btns.ticks[i].Value {
			skip = append(skip, i)
		}
	}
	return skip
}

// layoutPrompt draws the approve/deny dialog a blocked rpc is waiting on. A
// picker never comes here: it is a window of its own.
func layoutPrompt(
	gtx layout.Context,
	th *material.Theme,
	p *backend.Prompt,
	btns *promptButtons,
) layout.Dimensions {
	btns.reset(p)
	if btns.fold.Clicked(gtx) {
		btns.foldOpen = !btns.foldOpen
	}
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

	// codeBox draws text on a rounded code-coloured block
	codeBox := func(text string, maxLines int) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			macro := op.Record(gtx.Ops)
			dims := layout.UniformInset(unit.Dp(8)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				l := material.Body2(th, text)
				l.Color = currentTheme().codeFg
				l.MaxLines = maxLines
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
		}
	}

	for i, it := range p.Items {
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			pointer.CursorPointer.Add(gtx.Ops)
			cb := material.CheckBox(th, &btns.ticks[i], it)
			cb.TextSize = th.TextSize * 14 / 16
			cb.Color = currentTheme().codeFg
			return cb.Layout(gtx)
		}))
	}

	if p.Warning != "" {
		children = append(children,
			layout.Rigid(layout.Spacer{Height: unit.Dp(10)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				l := material.Body1(th, p.Warning)
				l.Color = currentTheme().danger
				l.Font.Weight = font.Bold
				return l.Layout(gtx)
			}),
		)
	}

	if p.Code != "" && p.Summary != "" {
		// the payload folded under its summary, opened by clicking that
		children = append(children,
			layout.Rigid(layout.Spacer{Height: unit.Dp(10)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				pointer.CursorPointer.Add(gtx.Ops)
				return btns.fold.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					arrow := "▸ "
					if btns.foldOpen {
						arrow = "▾ "
					}
					l := material.Body2(th, arrow+p.Summary)
					l.Color = currentTheme().muted
					return l.Layout(gtx)
				})
			}),
		)
	}

	if p.Code != "" && (p.Summary == "" || btns.foldOpen) {
		// this view doesn't scroll: a long payload is cut where it would
		// push the answers off the window
		lines := 0
		if p.Summary != "" {
			lines = 24
		}
		children = append(children,
			layout.Rigid(layout.Spacer{Height: unit.Dp(10)}.Layout),
			layout.Rigid(codeBox(p.Code, lines)),
		)
	}

	children = append(children, layout.Rigid(layout.Spacer{Height: unit.Dp(16)}.Layout))

	if !p.Remember {
		// nothing to widen the answer into: a plain yes and a plain no
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					pointer.CursorPointer.Add(gtx.Ops)
					return withTooltip(gtx, &btns.allow, "Let it go ahead", material.Button(th, &btns.allow, "Allow").Layout)
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					pointer.CursorPointer.Add(gtx.Ops)
					b := material.Button(th, &btns.deny, "Deny")
					b.Background = currentTheme().chipBg
					b.Color = currentTheme().chipFg
					return withTooltip(gtx, &btns.deny, "Refuse it", b.Layout)
				}),
			)
		}))
	} else {
		// the scopes: a row for this prompt, a row for this session, a row
		// for as long as the user leaves the answer there. Only the plain
		// allow and deny are full size; the wider answers are quieter.
		chipBtn := func(gtx layout.Context, btn *widget.Clickable, label, tip string, textSize unit.Sp) layout.Dimensions {
			pointer.CursorPointer.Add(gtx.Ops)
			t := currentTheme()
			b := material.Button(th, btn, label)
			b.Background = t.chipBg
			b.Color = t.chipFg
			b.TextSize = textSize
			b.Inset = layout.UniformInset(unit.Dp(8))
			return withTooltip(gtx, btn, tip, b.Layout)
		}
		row := func(left, right func(gtx layout.Context) layout.Dimensions) layout.FlexChild {
			return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Bottom: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
						layout.Flexed(1, left),
						layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
						layout.Flexed(1, right),
					)
				})
			})
		}
		children = append(children,
			row(
				func(gtx layout.Context) layout.Dimensions {
					pointer.CursorPointer.Add(gtx.Ops)
					return withTooltip(gtx, &btns.allow, "Allow it this time only: you will be asked again next time", material.Button(th, &btns.allow, "Allow").Layout)
				},
				func(gtx layout.Context) layout.Dimensions {
					return chipBtn(gtx, &btns.deny, "Deny", "Refuse it this time only: you will be asked again next time", 0)
				},
			),
			row(
				func(gtx layout.Context) layout.Dimensions {
					return chipBtn(gtx, &btns.sessionAllow, "Allow this session", "Allow it without asking again until the launcher quits", unit.Sp(13))
				},
				func(gtx layout.Context) layout.Dimensions {
					return chipBtn(gtx, &btns.sessionDeny, "Deny this session", "Refuse it without asking again until the launcher quits", unit.Sp(13))
				},
			),
			row(
				func(gtx layout.Context) layout.Dimensions {
					return chipBtn(gtx, &btns.alwaysAllow, "Always allow", "Allow it from now on, without ever asking again", unit.Sp(13))
				},
				func(gtx layout.Context) layout.Dimensions {
					return chipBtn(gtx, &btns.alwaysDeny, "Always deny", "Refuse it from now on, without ever asking again", unit.Sp(13))
				},
			),
		)
	}

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}

func layoutLogin(
	gtx layout.Context,
	th *material.Theme,
	ed *widget.Editor,
	btn *widget.Clickable,
	googleBtn *widget.Clickable,
	loginErr string,
	loginStatus string,
) layout.Dimensions {
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
			return withTooltip(gtx, btn, "Log in with the nsec or bunker:// URL pasted above", material.Button(th, btn, "Log in").Layout)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(20)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			pointer.CursorPointer.Add(gtx.Ops)
			return withTooltip(gtx, googleBtn, "Log in with your Google account in the browser: a remote signer holds a key made for you, no nsec needed", material.Button(th, googleBtn, "Log in with Google").Layout)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if loginStatus == "" {
				return layout.Dimensions{}
			}
			return layout.Inset{Top: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				l := material.Body2(th, loginStatus)
				l.Color = currentTheme().subtle
				return l.Layout(gtx)
			})
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

func layoutMain(
	gtx layout.Context,
	th *material.Theme,
	tabHomeBtn,
	tabCollectionBtn,
	tabWindowsBtn,
	tabDiscoBtn,
	tabDevBtn,
	themeBtn,
	readAuthBtn,
	logoutBtn *widget.Clickable,
	tab int,
	windowsList,
	installedList,
	discoveryList,
	devList *widget.List,
	relaysEd,
	filterEd,
	installedFilterEd,
	devURLed,
	devPathEd *widget.Editor,
	fetchBtn,
	checkUpdBtn,
	updateAllBtn,
	loadURLBtn,
	browseBtn,
	loadFolderBtn,
	installFolderBtn *widget.Clickable,
	closeBtns,
	reopenBtns,
	cardBtns,
	uninstBtns,
	installedUpdateBtns,
	actionBtns,
	updateBtns,
	devOpenBtns,
	devUnloadBtns,
	devPublishBtns []widget.Clickable,

	createBundleBtn *widget.Clickable,
	saveBundleBtn *widget.Clickable,
	cancelBundleBtn *widget.Clickable,
	bundleBtns []bundleRowBtns,

	vis,
	instVis []int,
	st backend.State,
	installedSet,
	busy map[string]bool,

	extra *extraTab,
	extraBtn *widget.Clickable,
	installedOpenBtns,
	installedAuthorBtns,
	discoCardBtns,
	discoOpenBtns,
	discoAuthorBtns []widget.Clickable,
	detailOpenBtn,
	detailPrimaryBtn,
	detailUpdateBtn,
	detailAuthorBtn,
	detailPinBtn *widget.Clickable,
	profileList *widget.List,
	profileCardBtns,
	profileOpenBtns,
	profileActionBtns,
	profileUpdateBtns []widget.Clickable,
	pinBtns []widget.Clickable,
) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layoutProfile(gtx, th, themeBtn, logoutBtn, st.ProfileName, st.ProfilePicture)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(16)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layoutTabs(gtx, th, tabHomeBtn, tabCollectionBtn, tabWindowsBtn, tabDiscoBtn, tab, len(st.Installed), len(st.Discovery), extra, extraBtn)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(12)}.Layout),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			if editing := currentBundleEdit(); editing != nil {
				return layoutBundleEditor(gtx, th, saveBundleBtn, cancelBundleBtn, editing)
			}
			if tab == tabHome {
				return home.Layout(gtx, th, st)
			}
			if tab == tabCollection {
				return layoutInstalledList(gtx, th, installedList, installedFilterEd, cardBtns, uninstBtns, installedUpdateBtns, installedOpenBtns, installedAuthorBtns, pinBtns, checkUpdBtn, updateAllBtn, instVis, st)
			}
			if tab == 0 {
				return layoutWindowsTab(gtx, th, windowsList,
					closeBtns, reopenBtns, createBundleBtn, bundleBtns, st)
			}
			if tab == 2 {
				return layoutDiscoveryTab(gtx, th, tabDevBtn, discoveryList, relaysEd, filterEd, fetchBtn, actionBtns,
					updateBtns, discoCardBtns, discoOpenBtns, discoAuthorBtns, vis, st, installedSet, busy)
			}
			if tab == 4 && extra != nil {
				if extra.kind == "napp" {
					return layoutNappDetail(gtx, th, extra, detailOpenBtn, detailPrimaryBtn, detailUpdateBtn, detailAuthorBtn, detailPinBtn, installedSet, busy, st)
				}
				return layoutProfileDetail(gtx, th, extra, profileList, profileCardBtns, profileOpenBtns, profileActionBtns, profileUpdateBtns, installedSet, busy, readAuthBtn, st)
			}
			return layoutDevTab(gtx, th, tabDevBtn, devList, devURLed, devPathEd, loadURLBtn, browseBtn, loadFolderBtn,
				installFolderBtn, devOpenBtns, devUnloadBtns, devPublishBtns, st)
		}),
	)
}

// truncate keeps a label short enough for a dialog line.
func truncate(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > max {
		return string(r[:max]) + "…"
	}
	return s
}

func layoutTabs(
	gtx layout.Context,
	th *material.Theme,
	homeBtn,
	collectionBtn,
	windowsBtn,
	discoBtn *widget.Clickable,
	tab,
	nInstalled,
	nDiscovery int,
	extra *extraTab,
	extraBtn *widget.Clickable,
) layout.Dimensions {
	tabBtn := func(gtx layout.Context, btn *widget.Clickable, label, tip string, active bool) layout.Dimensions {
		pointer.CursorPointer.Add(gtx.Ops)
		b := material.Button(th, btn, label)
		if active {
			b.Background = th.Palette.ContrastBg
		} else {
			b.Background = currentTheme().chipBg
			b.Color = currentTheme().chipFg
		}
		return withTooltip(gtx, btn, tip, b.Layout)
	}
	children := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return tabBtn(gtx, homeBtn, "Home", "The napps and bundles you pinned", tab == tabHome)
		}),
		layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return tabBtn(gtx, collectionBtn, "Collection", "Every napp installed on this computer", tab == tabCollection)
		}),
		layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return tabBtn(gtx, windowsBtn, "Sessions", "The napp windows opened this run, and the bundles made from them", tab == 0)
		}),
		layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return tabBtn(gtx, discoBtn, "Explore", "Find napps published on the relays or by the people you follow, and install them", tab == tabDiscovery || tab == tabDev)
		}),
	}
	// the ephemeral detail tab lives at the end and vanishes the moment
	// any other tab is picked
	if extra != nil && extraBtn != nil {
		children = append(children,
			layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return tabBtn(gtx, extraBtn, extra.title, "Details of "+extra.title, tab == 4)
			}),
		)
	}
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx, children...)
}

func layoutWindowsTab(
	gtx layout.Context,
	th *material.Theme,
	list *widget.List,
	closeBtns,
	reopenBtns []widget.Clickable,
	createBundleBtn *widget.Clickable,
	bundleBtns []bundleRowBtns,
	st backend.State,
) layout.Dimensions {
	// nothing but a message only when the tab really has nothing in it: a
	// bundle outlives the windows it was made from, so a launcher that has
	// opened no window this run still has bundles to show.
	if len(st.ManagedWindows) == 0 && len(st.Bundles) == 0 {
		l := material.Body2(th, "No windows opened yet.")
		l.Color = currentTheme().muted
		return l.Layout(gtx)
	}
	ui.mu.Lock()
	bundleErr := ui.bundleErr
	ui.mu.Unlock()
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		// whatever went wrong the last time a bundle was saved or changed
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if bundleErr == "" {
				return layout.Dimensions{}
			}
			return layout.Inset{Bottom: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				l := material.Body2(th, bundleErr)
				l.Color = currentTheme().danger
				return l.Layout(gtx)
			})
		}),
		// the bundle builder: only while something is checked. The name
		// and the actions are given in the editor it opens.
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			anyChecked := false
			for _, cb := range bundleChecks {
				if cb.Value {
					anyChecked = true
				}
			}
			if !anyChecked {
				return layout.Dimensions{}
			}
			return layout.Inset{Bottom: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				pointer.CursorPointer.Add(gtx.Ops)
				return withTooltip(gtx, createBundleBtn, "Make a bundle of the checked windows: one click opens them all again", material.Button(th, createBundleBtn, "Create bundle\u2026").Layout)
			})
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return material.List(th, list).Layout(gtx, len(st.ManagedWindows), func(gtx layout.Context, i int) layout.Dimensions {
				w := st.ManagedWindows[i]
				return layout.Inset{Bottom: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
						// the checkbox that puts this window into a bundle
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							cb, ok := bundleChecks[w.Instance]
							if !ok {
								return layout.Dimensions{}
							}
							pointer.CursorPointer.Add(gtx.Ops)
							return material.CheckBox(th, cb, "").Layout(gtx)
						}),
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
									layout.Rigid(func(gtx layout.Context) layout.Dimensions {
										l := material.Body1(th, w.Name)
										if !w.Open {
											l.Color = currentTheme().muted
										}
										return l.Layout(gtx)
									}),
									layout.Rigid(func(gtx layout.Context) layout.Dimensions {
										label := w.Instance
										if w.Action != "" {
											label += " · " + w.Action
										}
										if !w.Open {
											label += " · closed"
										}
										l := material.Caption(th, label)
										l.Color = currentTheme().muted
										return l.Layout(gtx)
									}),
									layout.Rigid(func(gtx layout.Context) layout.Dimensions {
										// every action this window was sent, so a
										// row says where it has been and not
										// only where it is
										pills := historyPills(th, w.History)
										if len(pills) == 0 {
											return layout.Dimensions{}
										}
										return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
											return flow(gtx, unit.Dp(4), pills)
										})
									}),
								)
							})
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							pointer.CursorPointer.Add(gtx.Ops)
							if w.Open {
								return withTooltip(gtx, &closeBtns[i], "Close this window: it stays listed here so you can reopen it", material.Button(th, &closeBtns[i], "Close").Layout)
							}
							return withTooltip(gtx, &reopenBtns[i], "Open this window again, back on what it was showing", material.Button(th, &reopenBtns[i], "Reopen").Layout)
						}),
					)
				})
			})
		}),
		// the bundles, each with its pin and shortcut toggles, edit and delete
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if len(st.Bundles) == 0 {
				return layout.Dimensions{}
			}
			return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						l := emph(material.Body2(th, "Bundles"))
						l.Color = currentTheme().subtle
						return l.Layout(gtx)
					}),
					layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx, bundleRows(th, st, bundleBtns)...)
					}),
				)
			})
		}),
	)
}

// bundleRows is one row per stored bundle: the name, its napps and where it
// can be opened from (the home tab, an OS shortcut), with the buttons that
// toggle those, edit and delete at the end.
func bundleRows(th *material.Theme, st backend.State, btns []bundleRowBtns) (out []layout.FlexChild) {
	small := func(b material.ButtonStyle) material.ButtonStyle {
		b.TextSize = unit.Sp(13)
		b.Inset = layout.UniformInset(unit.Dp(8))
		return b
	}
	toggle := func(gtx layout.Context, btn *widget.Clickable, label, tip string) layout.Dimensions {
		pointer.CursorPointer.Add(gtx.Ops)
		b := small(material.Button(th, btn, label))
		b.Background = currentTheme().chipBg
		b.Color = currentTheme().chipFg
		return withTooltip(gtx, btn, tip, b.Layout)
	}
	for i, sc := range st.Bundles {
		bb := &btns[i]
		names := make([]string, 0, len(sc.Entries))
		for _, e := range sc.Entries {
			if napp, ok := backend.InstalledNapp(e.NappID); ok {
				names = append(names, napp.Label())
				continue
			}
			names = append(names, e.NappID)
		}

		var where []string
		if sc.Pinned {
			where = append(where, "pinned to home")
		}
		if sc.Shortcut {
			where = append(where, "OS shortcut")
		}
		if len(where) == 0 {
			where = append(where, "not pinned, no OS shortcut")
		}

		pinLabel, pinTip := "Pin", "Put this bundle on the Home tab, where a click opens all of its windows"
		if sc.Pinned {
			pinLabel, pinTip = "Unpin", "Take this bundle off the Home tab"
		}
		scLabel, scTip := "Create shortcut", "Add this bundle to your system's application menu, so it opens without going through the launcher first"
		if sc.Shortcut {
			scLabel, scTip = "Remove shortcut", "Delete this bundle's shortcut from your system's application menu (the bundle stays)"
		}

		out = append(out, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								l := material.Body1(th, sc.Name)
								l.Font.Weight = font.Bold
								return l.Layout(gtx)
							}),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								l := material.Caption(th, truncate(strings.Join(names, " + "), 90))
								l.Color = currentTheme().muted
								return l.Layout(gtx)
							}),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								l := material.Caption(th, strings.Join(where, " · "))
								l.Color = currentTheme().subtle
								return l.Layout(gtx)
							}),
						)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return toggle(gtx, &bb.pin, pinLabel, pinTip)
					}),
					layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return toggle(gtx, &bb.shortcut, scLabel, scTip)
					}),
					layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						pointer.CursorPointer.Add(gtx.Ops)
						return withTooltip(gtx, &bb.edit, "Change the name, or the actions each napp gets", small(material.Button(th, &bb.edit, "Edit")).Layout)
					}),
					layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						pointer.CursorPointer.Add(gtx.Ops)
						b := small(material.Button(th, &bb.del, "Delete"))
						b.Background = currentTheme().chipBg
						b.Color = currentTheme().danger
						return withTooltip(gtx, &bb.del, "Forget this bundle, taking it off the Home tab and removing its shortcut", b.Layout)
					}),
				)
			})
		}))
	}
	return out
}

// layoutBundleEditor is the overlay a "Create bundle…" (or an "Edit")
// click opens over the Sessions tab: the bundle name, the actions of each napp
// of the bundle as pills that can be removed, save and cancel.
func layoutBundleEditor(
	gtx layout.Context,
	th *material.Theme,
	saveBtn,
	cancelBtn *widget.Clickable,
	edit *bundleEditState,
) layout.Dimensions {
	removeClickedActions(gtx, edit)
	title := "New bundle"
	if edit.name != "" {
		title = "Edit bundle"
	}
	p := currentTheme()
	ui.mu.Lock()
	bundleErr := ui.bundleErr
	ui.mu.Unlock()
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			t := material.H6(th, title)
			t.Font.Weight = font.Bold
			return t.Layout(gtx)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return editorBox(gtx, th, &edit.nameEd, "name of the bundle")
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if bundleErr == "" {
				return layout.Dimensions{}
			}
			return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				l := material.Body2(th, bundleErr)
				l.Color = p.danger
				return l.Layout(gtx)
			})
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(12)}.Layout),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			if len(edit.entries) == 0 {
				return layout.Dimensions{}
			}
			fl := &widget.List{}
			fl.Axis = layout.Vertical
			return material.List(th, fl).Layout(gtx, len(edit.entries), func(gtx layout.Context, i int) layout.Dimensions {
				e := &edit.entries[i]
				return layout.Inset{Bottom: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							l := emph(material.Body2(th, "Actions of "+e.label))
							l.Color = p.subtle
							return l.Layout(gtx)
						}),
						layout.Rigid(layout.Spacer{Height: unit.Dp(4)}.Layout),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							if len(e.actions) == 0 {
								l := material.Caption(th, "No actions: the napp just opens.")
								l.Color = p.muted
								return l.Layout(gtx)
							}
							return flow(gtx, unit.Dp(4), removablePills(th, e))
						}),
					)
				})
			})
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(12)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					pointer.CursorPointer.Add(gtx.Ops)
					return withTooltip(gtx, saveBtn, "Store the bundle with this name and these actions", material.Button(th, saveBtn, "Save bundle").Layout)
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					pointer.CursorPointer.Add(gtx.Ops)
					b := material.Button(th, cancelBtn, "Cancel")
					b.Background = p.chipBg
					b.Color = p.chipFg
					return withTooltip(gtx, cancelBtn, "Close the editor without saving anything", b.Layout)
				}),
			)
		}),
	)
}

// layoutInstalledList is the installed napps, on the collection tab.
func layoutInstalledList(
	gtx layout.Context,
	th *material.Theme,
	list *widget.List,
	filterEd *widget.Editor,
	cardBtns,
	uninstBtns []widget.Clickable,
	installedUpdateBtns []widget.Clickable,
	openBtns,
	authorBtns,
	pinBtns []widget.Clickable,
	checkUpdBtn,
	updateAllBtn *widget.Clickable,
	vis []int,
	st backend.State,
) layout.Dimensions {
	pinnedSet := make(map[string]bool, len(st.Pinned))
	for _, n := range st.Pinned {
		pinnedSet[n.ID] = true
	}
	if len(st.Installed) == 0 {
		l := material.Body2(th, "No napps installed yet. Find some in the Explore tab.")
		l.Color = currentTheme().muted
		return l.Layout(gtx)
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		// the filter box, narrowing the entries below by name, author,
		// author name or description, and the manual update check at its
		// right (the automatic one runs on its own after startup).
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return editorBox(gtx, th, filterEd, "filter by name, author or description")
				}),
				// "Update all" only shows when a check found something
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if updatesAvailable(st) == 0 {
						return layout.Dimensions{}
					}
					return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						pointer.CursorPointer.Add(gtx.Ops)
						b := material.Button(th, updateAllBtn, "Update all")
						b.TextSize = unit.Sp(13)
						b.Inset = layout.UniformInset(unit.Dp(8))
						return withTooltip(gtx, updateAllBtn, "Install the newer version of every napp that has one", b.Layout)
					})
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					p := currentTheme()
					// while a check runs the button turns into a spinner
					// (clicks are ignored then anyway)
					if st.UpdateCheckRunning {
						b := material.ButtonLayout(th, checkUpdBtn)
						b.Background = p.chipBg
						return withTooltip(gtx, checkUpdBtn, "Looking for newer versions of the installed napps\u2026", func(gtx layout.Context) layout.Dimensions {
							return b.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								return layout.UniformInset(unit.Dp(8)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									sz := gtx.Dp(18)
									gtx.Constraints = layout.Exact(image.Pt(sz, sz))
									l := material.Loader(th)
									l.Color = p.chipFg
									return l.Layout(gtx)
								})
							})
						})
					}
					pointer.CursorPointer.Add(gtx.Ops)
					b := material.Button(th, checkUpdBtn, "\u21bb")
					b.Background = p.chipBg
					b.Color = p.chipFg
					b.TextSize = unit.Sp(15)
					b.Inset = layout.UniformInset(unit.Dp(8))
					return withTooltip(gtx, checkUpdBtn, "Check the relays for newer versions of the installed napps", b.Layout)
				}),
			)
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
				// the card opens the napp page; Open launches it
				var updateBtn, openBtn, authorBtn *widget.Clickable
				if row < len(installedUpdateBtns) && st.Installed[row].UpdateAvailable != nil {
					updateBtn = &installedUpdateBtns[row]
				}
				if row < len(openBtns) {
					openBtn = &openBtns[row]
				}
				if row < len(authorBtns) {
					authorBtn = &authorBtns[row]
				}
				var pinBtn *widget.Clickable
				if row < len(pinBtns) {
					pinBtn = &pinBtns[row]
				}
				n := st.Installed[row]
				return renderNappCard(gtx, th, cardBtn, authorBtn, openBtn, uninstBtn, updateBtn, "Uninstall", "Update", pinBtn, pinnedSet[n.ID], n)
			})
		}),
	)
}

func layoutDiscoveryTab(
	gtx layout.Context,
	th *material.Theme,
	devBtn *widget.Clickable,
	list *widget.List,
	relaysEd,
	filterEd *widget.Editor,
	fetchBtn *widget.Clickable,
	actionBtns,
	updateBtns []widget.Clickable,
	cardBtns,
	openBtns,
	authorBtns []widget.Clickable,
	vis []int,
	st backend.State,
	installedSet,
	busy map[string]bool,
) layout.Dimensions {
	discovery := discoNapps(st)
	mode := currentNappsMode(st)
	// the contacts and mine modes only read the local store, which a sync
	// fills in the background (see backend/contacts.go)
	stored := mode != modeDiscovery
	fetchErr, fetching := st.FetchErr, st.Fetching
	if stored {
		fetchErr, fetching = "", st.ContactsFetching
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layoutNappsModes(gtx, th, devBtn, tabDiscovery, st)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
		// the filter box, narrowing the entries below by name, author,
		// author name or description.
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return editorBox(gtx, th, filterEd, "filter by name, author or description")
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
		// the stored modes only say whether the sync is at it
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if mode != modeContacts || !fetching {
				return layout.Dimensions{}
			}
			l := material.Body2(th, "Looking for more napps on your contacts' relays\u2026")
			l.Color = currentTheme().muted
			return l.Layout(gtx)
		}),
		// discovery mode gets the relays editor
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if stored {
				return layout.Dimensions{}
			}
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
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if stored {
				return layout.Dimensions{}
			}
			return layout.Spacer{Height: unit.Dp(8)}.Layout(gtx)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if stored {
				return layout.Dimensions{}
			}
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					pointer.CursorPointer.Add(gtx.Ops)
					label := "Fetch napps"
					if fetching {
						label = "Fetching\u2026"
					}
					return withTooltip(gtx, fetchBtn, "Search the relays above for published napps", material.Button(th, fetchBtn, label).Layout)
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
				msg := "No napps yet. Pick some good relays and click \"Fetch napps\"."
				if fetching {
					msg = "Searching relays\u2026"
				}
				if mode == modeContacts {
					msg = "None of the people you follow published a napp yet."
					if fetching {
						msg = "Searching your contacts' relays\u2026"
					}
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
				label, updLabel, canOpen := cardLabels(n, installedSet, busy)
				var cardBtn, openBtn, authorBtn *widget.Clickable
				if row < len(cardBtns) {
					cardBtn = &cardBtns[row]
				}
				if row < len(openBtns) && canOpen {
					openBtn = &openBtns[row]
				}
				if row < len(authorBtns) {
					authorBtn = &authorBtns[row]
				}
				return renderNappCard(gtx, th, cardBtn, authorBtn, openBtn, btn, updBtn, label, updLabel, nil, false, n)
			})
		}),
	)
}

// layoutNappsModes is the row of sub-tabs on top of the napps tab: the
// discovery relays, the contacts, the user's own napps (only while there
// are any) and, in dev builds (devBtn not nil), the dev one.
func layoutNappsModes(gtx layout.Context, th *material.Theme, devBtn *widget.Clickable, tab int, st backend.State) layout.Dimensions {
	mode := currentNappsMode(st)
	modeBtn := func(gtx layout.Context, btn *widget.Clickable, label, tip string, active bool) layout.Dimensions {
		pointer.CursorPointer.Add(gtx.Ops)
		b := material.Button(th, btn, label)
		b.TextSize = unit.Sp(13)
		b.Inset = layout.Inset{Top: unit.Dp(6), Bottom: unit.Dp(6), Left: unit.Dp(12), Right: unit.Dp(12)}
		if active {
			b.Background = th.Palette.ContrastBg
		} else {
			b.Background = currentTheme().chipBg
			b.Color = currentTheme().chipFg
		}
		return withTooltip(gtx, btn, tip, b.Layout)
	}
	children := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return modeBtn(gtx, &modeDiscoBtn, "Discovery", "Napps published on the relays below", tab == tabDiscovery && mode == modeDiscovery)
		}),
		layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return modeBtn(gtx, &modeContactsBtn, "Contacts", "Napps published by the people you follow, fetched from their own relays in the background", tab == tabDiscovery && mode == modeContacts)
		}),
	}
	if len(st.Mine) > 0 {
		children = append(children,
			layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return modeBtn(gtx, &modeMineBtn, "Mine", "The napps you published", tab == tabDiscovery && mode == modeMine)
			}),
		)
	}
	if devBtn != nil {
		children = append(children,
			layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return modeBtn(gtx, devBtn, "Dev", "Load a napp you are working on from a folder or a dev server", tab == tabDev)
			}),
		)
	}
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx, children...)
}

// layoutDevTab is the dev-build tab for loading ephemeral napps: either a
// dev-server url (used directly) or a local folder (served from disk by the
// throwaway server). The cards open on tap, like the
// installed tab's.
func layoutDevTab(
	gtx layout.Context,
	th *material.Theme,
	devBtn *widget.Clickable,
	list *widget.List,
	urlEd,
	pathEd *widget.Editor,
	loadURLBtn,
	browseBtn,
	loadFolderBtn,
	installFolderBtn *widget.Clickable,
	openBtns,
	unloadBtns,
	publishBtns []widget.Clickable,
	st backend.State,
) layout.Dimensions {
	smallBtn := func(gtx layout.Context, btn *widget.Clickable, label, tip string) layout.Dimensions {
		pointer.CursorPointer.Add(gtx.Ops)
		b := material.Button(th, btn, label)
		b.TextSize = unit.Sp(13)
		b.Inset = layout.UniformInset(unit.Dp(8))
		return withTooltip(gtx, btn, tip, b.Layout)
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layoutNappsModes(gtx, th, devBtn, tabDev, st)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(12)}.Layout),
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
					return smallBtn(gtx, loadURLBtn, "Load URL", "Run the napp served at this URL, like a local dev server")
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
					return smallBtn(gtx, browseBtn, "Browse…", "Pick the napp's folder with a file chooser")
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return smallBtn(gtx, loadFolderBtn, "Load folder", "Run the napp in this folder, straight from its files")
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return smallBtn(gtx, installFolderBtn, "Install folder", "Install a copy of this folder as a local napp, kept across restarts")
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
				var openBtn, unloadBtn, publishBtn *widget.Clickable
				if i < len(openBtns) {
					openBtn = &openBtns[i]
				}
				if i < len(unloadBtns) {
					unloadBtn = &unloadBtns[i]
				}
				if i < len(publishBtns) && backend.DevSourceKind(st.Dev[i].ID) == "folder" {
					publishBtn = &publishBtns[i]
				}
				return renderNappCard(gtx, th, openBtn, nil, nil, publishBtn, unloadBtn, "Publish", "Unload", nil, false, st.Dev[i])
			})
		}),
	)
}

// layoutConfirmLogout is the dialog shown when the user hits "Log out":
// logging out closes every open napp, so it deserves a second look.
func layoutConfirmLogout(
	gtx layout.Context,
	th *material.Theme,
	yesBtn,
	noBtn *widget.Clickable,
) layout.Dimensions {
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
							return withTooltip(gtx, yesBtn, "Log out now, closing every napp window", b.Layout)
						}),
						layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							pointer.CursorPointer.Add(gtx.Ops)
							b := material.Button(th, noBtn, "Cancel")
							b.Background = p.chipBg
							b.Color = p.chipFg
							return withTooltip(gtx, noBtn, "Stay logged in", b.Layout)
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

func layoutProfile(
	gtx layout.Context,
	th *material.Theme,
	themeBtn,
	logoutBtn *widget.Clickable,
	name,
	pic string,
) layout.Dimensions {
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			// the avatar and name lead to the user's own profile tab, where
			// the settings are
			pointer.CursorPointer.Add(gtx.Ops)
			btn := &detailBtns.selfProfile
			return withTooltip(gtx, btn, "See your profile and settings", func(gtx layout.Context) layout.Dimensions {
				return btn.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return avatar(gtx, pic, 48)
						}),
						layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							t := material.H6(th, name)
							t.Font.Weight = font.Bold
							return t.Layout(gtx)
						}),
					)
				})
			})
		}),
		// the box for nostr codes takes up whatever the header leaves
		layout.Rigid(layout.Spacer{Width: unit.Dp(16)}.Layout),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layoutOpenBox(gtx, th)
		}),
		layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return themeButton(gtx, th, themeBtn)
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
			return withTooltip(gtx, logoutBtn, "Forget this login on this computer (you will be asked to confirm)", b.Layout)
		}),
	)
}

// themeButton switches between the light and the dark theme.
func themeButton(gtx layout.Context, th *material.Theme, btn *widget.Clickable) layout.Dimensions {
	pointer.CursorPointer.Add(gtx.Ops)
	p := currentTheme()
	label := "Dark"
	tip := "Switch the launcher and every napp window to the dark theme"
	if p.name == "dark" {
		label = "Light"
		tip = "Switch the launcher and every napp window to the light theme"
	}
	b := material.Button(th, btn, label)
	b.Background = p.chipBg
	b.Color = p.chipFg
	b.TextSize = unit.Sp(13)
	b.Inset = layout.UniformInset(unit.Dp(8))
	return withTooltip(gtx, btn, tip, b.Layout)
}

// readAuthButton turns authenticating to relays when reading on and off.
func readAuthButton(gtx layout.Context, th *material.Theme, btn *widget.Clickable, on bool) layout.Dimensions {
	pointer.CursorPointer.Add(gtx.Ops)
	p := currentTheme()
	label := "Auth on reads: off"
	tip := "Relays that ask for it are not told who you are when reading; turn on to authenticate to them (publishing always does)"
	if on {
		label = "Auth on reads: on"
		tip = "Relays that ask for it are told who you are (NIP-42) when reading; turn off to stop that (publishing always authenticates)"
	}
	b := material.Button(th, btn, label)
	b.Background = p.chipBg
	b.Color = p.chipFg
	b.TextSize = unit.Sp(13)
	b.Inset = layout.UniformInset(unit.Dp(8))
	return withTooltip(gtx, btn, tip, b.Layout)
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

func actionChip(gtx layout.Context, th *material.Theme, action string) layout.Dimensions {
	p := currentTheme()
	macro := op.Record(gtx.Ops)
	label := material.Caption(th, action)
	label.Color = p.chipFg
	dims := layout.UniformInset(unit.Dp(4)).Layout(gtx, label.Layout)
	call := macro.Stop()
	bg := clip.RRect{Rect: image.Rectangle{Max: dims.Size}, NW: 5, NE: 5, SW: 5, SE: 5}
	defer bg.Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, p.chipBg)
	call.Add(gtx.Ops)
	return dims
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
// action buttons. When cardBtn is not nil the whole card is clickable (it
// opens the napp's page); authorBtn alone opens the author's profile page.
// Buttons drawn on top of the card's area keep working, so the frame handler
// must check which of them fired before acting on the card itself.
func renderNappCard(
	gtx layout.Context,
	th *material.Theme,
	cardBtn,
	authorBtn,
	openBtn,
	btn,
	secondBtn *widget.Clickable,
	btnLabel,
	secondLabel string,
	// pinBtn, when given, is the Pin/Unpin toggle for the home tab
	pinBtn *widget.Clickable,
	pinned bool,
	napp backend.Napp,
) layout.Dimensions {
	authorName, authorPic := "", ""
	if napp.IsLocal() {
		// no author to show or open: say where it came from instead
		authorName, authorBtn = "installed from local files", nil
	} else if napp.HasAuthor() {
		if p := cachedProfile(napp.Author.Hex()); p != nil && p.ShortName != "" {
			authorName, authorPic = p.ShortName, p.Picture
		} else {
			authorName = napp.AuthorShortName()
			if authorName == "" {
				authorName = napp.Author.Hex()
				if len(authorName) > 16 {
					authorName = authorName[:16] + "…"
				}
			}
		}
	}
	return layout.Inset{Bottom: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		sz := gtx.Constraints.Max
		macro := op.Record(gtx.Ops)
		dims := layout.Inset{
			Top: unit.Dp(12), Bottom: unit.Dp(12),
			Left: unit.Dp(12), Right: unit.Dp(12),
		}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			content := func(gtx layout.Context) layout.Dimensions {
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
								actions := make([]string, 0, len(napp.Actions))
								for _, action := range napp.Actions {
									if strings.TrimSpace(action) != "" {
										actions = append(actions, action)
									}
								}
								if len(actions) == 0 {
									return layout.Dimensions{}
								}
								return layout.Inset{Top: unit.Dp(5)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									children := make([]layout.FlexChild, 0, len(actions)*2)
									for i, action := range actions {
										if i > 0 {
											children = append(children, layout.Rigid(layout.Spacer{Width: unit.Dp(4)}.Layout))
										}
										children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
											return actionChip(gtx, th, action)
										}))
									}
									return layout.Flex{Axis: layout.Horizontal}.Layout(gtx, children...)
								})
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
								if authorName == "" {
									return layout.Dimensions{}
								}
								return layout.Inset{Top: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									inner := func(gtx layout.Context) layout.Dimensions {
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
									}
									if authorBtn == nil {
										return inner(gtx)
									}
									pointer.CursorPointer.Add(gtx.Ops)
									return withTooltip(gtx, authorBtn, "See "+authorName+"'s profile and the other napps they published", func(gtx layout.Context) layout.Dimensions {
										return authorBtn.Layout(gtx, inner)
									})
								})
							}),
						)
					}),
				)
			}
			buttons := []layout.FlexChild{
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if pinBtn == nil {
						return layout.Dimensions{}
					}
					return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						pointer.CursorPointer.Add(gtx.Ops)
						label := "Pin"
						if pinned {
							label = "Unpin"
						}
						b := material.Button(th, pinBtn, label)
						b.Background = currentTheme().chipBg
						b.Color = currentTheme().chipFg
						b.TextSize = unit.Sp(13)
						b.Inset = layout.UniformInset(unit.Dp(8))
						return withTooltip(gtx, pinBtn, pinTip(pinned), b.Layout)
					})
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if openBtn == nil {
						return layout.Dimensions{}
					}
					return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						pointer.CursorPointer.Add(gtx.Ops)
						label := openLabel(napp)
						b := material.Button(th, openBtn, label)
						b.Background = currentTheme().suggestBg
						b.Color = currentTheme().suggestFg
						b.TextSize = unit.Sp(13)
						b.Inset = layout.UniformInset(unit.Dp(8))
						return withTooltip(gtx, openBtn, nappButtonTip(label, napp), b.Layout)
					})
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if secondBtn != nil && secondLabel != "" {
						pointer.CursorPointer.Add(gtx.Ops)
						ub := material.Button(th, secondBtn, secondLabel)
						ub.TextSize = unit.Sp(13)
						ub.Inset = layout.UniformInset(unit.Dp(8))
						return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return withTooltip(gtx, secondBtn, nappButtonTip(secondLabel, napp), ub.Layout)
						})
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
						return withTooltip(gtx, btn, nappButtonTip(btnLabel, napp), b.Layout)
					})
				}),
			}
			actions := func(gtx layout.Context, justifyEnd bool) layout.Dimensions {
				if gtx.Constraints.Max.X < gtx.Dp(300) {
					return layout.Flex{Axis: layout.Vertical, Alignment: layout.End}.Layout(gtx, buttons...)
				}
				if !justifyEnd {
					return layout.Flex{Axis: layout.Horizontal}.Layout(gtx, buttons...)
				}
				children := append([]layout.FlexChild{
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return layout.Dimensions{Size: image.Point{X: gtx.Constraints.Max.X}}
					}),
				}, buttons...)
				return layout.Flex{Axis: layout.Horizontal}.Layout(gtx, children...)
			}
			if gtx.Constraints.Max.X < gtx.Dp(720) {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(content),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return actions(gtx, true)
						})
					}),
				)
			}
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, content),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return actions(gtx, false)
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
		cardTip := "See everything about " + napp.Label()
		if backend.DevSourceKind(napp.ID) != "" {
			cardTip = "Open " + napp.Label() + " in a new window"
		}
		return withTooltip(gtx, cardBtn, cardTip, func(gtx layout.Context) layout.Dimensions {
			return cardBtn.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				defer bg.Push(gtx.Ops).Pop()
				paint.Fill(gtx.Ops, currentTheme().card)
				call.Add(gtx.Ops)
				return layout.Dimensions{Size: image.Point{X: sz.X, Y: dims.Size.Y}}
			})
		})
	})
}

// updatesAvailable counts the installed napps a newer version was found for.
func updatesAvailable(st backend.State) int {
	n := 0
	for _, in := range st.Installed {
		if in.UpdateAvailable != nil {
			n++
		}
	}
	return n
}

// pinTip is the tooltip of a napp's Pin/Unpin toggle.
func pinTip(pinned bool) string {
	if pinned {
		return "Take this napp off the Home tab"
	}
	return "Put this napp on the Home tab, one click away"
}

// nappButtonTip is the tooltip of a napp card's install, uninstall, update
// (or busy) button, by its label.
func nappButtonTip(label string, napp backend.Napp) string {
	switch label {
	case "Open":
		return "Open " + napp.Label() + " in a new window"
	case "Install":
		return "Download " + napp.Label() + " to this computer, so it can be opened"
	case "Preview":
		return "Try " + napp.Label() + " in a new window without adding it to the installed napps"
	case "Remove":
		return "Delete the previewed " + napp.Label() + " from this computer"
	case "Uninstall":
		return "Remove " + napp.Label() + " from this computer"
	case "Update":
		return "Install the newer version of " + napp.Label()
	case "Publish":
		return "Choose where to publish this napp, so others can find and install it"
	case "Unload":
		return "Close this dev napp's windows and take it off the list (its folder or server is left alone)"
	case "Working\u2026":
		return "Hold on, this napp is being installed, updated or removed"
	}
	return ""
}
