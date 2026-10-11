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
}

// layoutPrompt draws the dialog a blocked rpc is waiting on: either an
// approve/deny question or a list of napps that can handle an action.
func layoutPrompt(
	gtx layout.Context,
	th *material.Theme,
	p *backend.Prompt,
	btns *promptButtons,
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
					optTip := "Open " + opt.Label + " to handle this"
					if opt.Instance != "" {
						optTip = "Send this to the " + opt.Label + " window that is already open"
					}
					return withTooltip(gtx, &optBtns[i], optTip, func(gtx layout.Context) layout.Dimensions {
						return optBtns[i].Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							open := opt.Instance != ""
							// the fill says what kind of napp this is: one the user
							// keeps picking for this action (which outranks the
							// rest, it is the reason the list is in this order),
							// one being developed, or any other. Whether it is a
							// window already open is told apart by a stripe and a
							// pill of its own, so it shows whatever the fill is.
							bgColor, fgColor := p.chipBg, p.chipFg
							switch {
							case opt.Suggested:
								bgColor, fgColor = p.suggestBg, p.suggestFg
							case opt.Dev:
								bgColor, fgColor = p.devBg, p.devFg
							}
							macro := op.Record(gtx.Ops)
							dims := layout.Inset{Top: unit.Dp(10), Bottom: unit.Dp(10), Left: unit.Dp(16), Right: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
									layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
										return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
											layout.Rigid(func(gtx layout.Context) layout.Dimensions {
												l := material.Body1(th, opt.Label)
												l.Color = fgColor
												if open {
													l.Font.Weight = font.Bold
												}
												return l.Layout(gtx)
											}),
											layout.Rigid(func(gtx layout.Context) layout.Dimensions {
												if opt.Detail == "" {
													return layout.Dimensions{}
												}
												l := material.Caption(th, truncate(opt.Detail, 80))
												if opt.Dev || opt.Suggested {
													l.Color = fgColor
												} else {
													l.Color = p.muted
												}
												return l.Layout(gtx)
											}),
										)
									}),
									layout.Rigid(func(gtx layout.Context) layout.Dimensions {
										if !open {
											return layout.Dimensions{}
										}
										return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
											m := op.Record(gtx.Ops)
											d := layout.Inset{Top: unit.Dp(2), Bottom: unit.Dp(2), Left: unit.Dp(8), Right: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
												l := material.Caption(th, "open")
												l.Color = p.contrastFg
												l.Font.Weight = font.Bold
												return l.Layout(gtx)
											})
											c := m.Stop()
											r := gtx.Dp(9)
											defer clip.UniformRRect(image.Rectangle{Max: d.Size}, r).Push(gtx.Ops).Pop()
											paint.Fill(gtx.Ops, p.contrastBg)
											c.Add(gtx.Ops)
											return d
										})
									}),
								)
							})
							call := macro.Stop()
							bg := clip.RRect{Rect: image.Rectangle{Max: image.Point{X: gtx.Constraints.Max.X, Y: dims.Size.Y}}, NW: 6, NE: 6, SW: 6, SE: 6}
							defer bg.Push(gtx.Ops).Pop()
							paint.Fill(gtx.Ops, bgColor)
							if open {
								stripe := clip.Rect{Max: image.Point{X: gtx.Dp(5), Y: dims.Size.Y}}.Push(gtx.Ops)
								paint.Fill(gtx.Ops, p.contrastBg)
								stripe.Pop()
							}
							call.Add(gtx.Ops)
							return dims
						})
					})
				})
			}))
		}
		children = append(children,
			layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				pointer.CursorPointer.Add(gtx.Ops)
				b := material.Button(th, &btns.deny, "Cancel")
				b.Background = currentTheme().chipBg
				b.Color = currentTheme().chipFg
				return withTooltip(gtx, &btns.deny, "Don't send this anywhere", b.Layout)
			}),
		)
	} else if !p.Remember {
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
	tabWindowsBtn,
	tabNappsBtn,
	tabDiscoBtn,
	tabDevBtn,
	themeBtn,
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
	loadFolderBtn *widget.Clickable,
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
			return layoutTabs(gtx, th, tabHomeBtn, tabWindowsBtn, tabNappsBtn, tabDiscoBtn, tabDevBtn, tab, len(st.Installed), len(st.Discovery), extra, extraBtn)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(12)}.Layout),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			if editing := currentBundleEdit(); editing != nil {
				return layoutBundleEditor(gtx, th, saveBundleBtn, cancelBundleBtn, editing)
			}
			if tab == tabHome {
				return home.Layout(gtx, th, st)
			}
			if tab == 0 {
				return layoutWindowsTab(gtx, th, windowsList,
					closeBtns, reopenBtns, createBundleBtn, bundleBtns, st)
			}
			if tab == 1 {
				return layoutNappsTab(gtx, th, installedList, installedFilterEd, cardBtns, uninstBtns, installedUpdateBtns, installedOpenBtns, installedAuthorBtns, pinBtns, checkUpdBtn, updateAllBtn, instVis, st)
			}
			if tab == 2 {
				return layoutDiscoveryTab(gtx, th, discoveryList, relaysEd, filterEd, fetchBtn, actionBtns,
					updateBtns, discoCardBtns, discoOpenBtns, discoAuthorBtns, vis, st.FetchErr, st.Fetching, st.Discovery, installedSet, busy)
			}
			if tab == 4 && extra != nil {
				if extra.kind == "napp" {
					return layoutNappDetail(gtx, th, extra, detailOpenBtn, detailPrimaryBtn, detailUpdateBtn, detailAuthorBtn, detailPinBtn, installedSet, busy, st)
				}
				return layoutProfileDetail(gtx, th, extra, profileList, profileCardBtns, profileOpenBtns, profileActionBtns, profileUpdateBtns, installedSet, busy)
			}
			return layoutDevTab(gtx, th, devList, devURLed, devPathEd, loadURLBtn, browseBtn, loadFolderBtn,
				devOpenBtns, devUnloadBtns, devPublishBtns, st)
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
	windowsBtn,
	nappsBtn,
	discoBtn,
	devBtn *widget.Clickable,
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
			return tabBtn(gtx, homeBtn, "Home", "The napps and bundles you pinned, one click away", tab == tabHome)
		}),
		layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return tabBtn(gtx, windowsBtn, "Windows", "The napp windows opened this run, and the bundles made from them", tab == 0)
		}),
		layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return tabBtn(gtx, nappsBtn, "Installed", "The napps on this computer: open, pin, update or uninstall them", tab == 1)
		}),
		layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return tabBtn(gtx, discoBtn, "Discovery", "Find napps published on the relays and install them", tab == 2)
		}),
	}
	// devBtn is nil outside dev builds: no dev tab there
	if devBtn != nil {
		children = append(children,
			layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return tabBtn(gtx, devBtn, "Dev", "Load a napp you are working on from a folder or a dev server", tab == 3)
			}),
		)
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
	return layout.Flex{Axis: layout.Horizontal}.Layout(gtx, children...)
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
// click opens over the Windows tab: the bundle name, one actions textarea
// per napp of the bundle (one action per line), save and cancel.
func layoutBundleEditor(
	gtx layout.Context,
	th *material.Theme,
	saveBtn,
	cancelBtn *widget.Clickable,
	edit *bundleEditState,
) layout.Dimensions {
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
				ed := &edit.entries[i].ed
				label := edit.entries[i].label
				return layout.Inset{Bottom: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							l := emph(material.Body2(th, "Actions of "+label+" (one JSON object per line)"))
							l.Color = p.subtle
							return l.Layout(gtx)
						}),
						layout.Rigid(layout.Spacer{Height: unit.Dp(4)}.Layout),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return editorBoxHeight(gtx, th, ed, 120, `{"type": "profile", "payload": "npub1…"}`)
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

// editorBoxHeight is an editorBox with a minimum height, for the multi-line
// actions textareas.
func editorBoxHeight(gtx layout.Context, th *material.Theme, ed *widget.Editor, minHeight int, hint string) layout.Dimensions {
	gtx.Constraints.Min.Y = gtx.Dp(unit.Dp(minHeight))
	return editorBox(gtx, th, ed, hint)
}

func layoutNappsTab(
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
		l := material.Body2(th, "No napps installed yet. Find some in the Discovery tab.")
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
	fetchErr string,
	fetching bool,
	discovery []backend.Napp,
	installedSet,
	busy map[string]bool,
) layout.Dimensions {
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
				if installedSet[n.ID] && n.UpdateAvailable != nil {
					updLabel = "Update"
				}
				var cardBtn, openBtn, authorBtn *widget.Clickable
				if row < len(cardBtns) {
					cardBtn = &cardBtns[row]
				}
				if row < len(openBtns) && installedSet[n.ID] {
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

// layoutDevTab is the dev-build tab for loading ephemeral napps: either a
// dev-server url (used directly) or a local folder (served from disk by the
// throwaway server). The cards open on tap, like the
// installed tab's.
func layoutDevTab(
	gtx layout.Context,
	th *material.Theme,
	list *widget.List,
	urlEd,
	pathEd *widget.Editor,
	loadURLBtn,
	browseBtn,
	loadFolderBtn *widget.Clickable,
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
			tip := "Switch the launcher and every napp window to the dark theme"
			if p.name == "dark" {
				tip = "Switch the launcher and every napp window to the light theme"
			}
			b := material.Button(th, themeBtn, label)
			b.Background = p.chipBg
			b.Color = p.chipFg
			b.TextSize = unit.Sp(13)
			b.Inset = layout.UniformInset(unit.Dp(8))
			return withTooltip(gtx, themeBtn, tip, b.Layout)
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
	if napp.Author.Hex() != "" {
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
						b := material.Button(th, openBtn, "Open")
						b.Background = currentTheme().suggestBg
						b.Color = currentTheme().suggestFg
						b.TextSize = unit.Sp(13)
						b.Inset = layout.UniformInset(unit.Dp(8))
						return withTooltip(gtx, openBtn, "Open "+napp.Label()+" in a new window", b.Layout)
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
	case "Install":
		return "Download " + napp.Label() + " to this computer, so it can be opened"
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
