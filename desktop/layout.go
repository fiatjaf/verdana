package main

import (
	"image"
	"strconv"
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
					return optBtns[i].Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						open := opt.Instance != ""
						bgColor, fgColor := p.chipBg, p.chipFg
						// the napp the user keeps picking for this action gets
						// its own color, and it outranks the other two: it is
						// the reason the list is in this order
						switch {
						case opt.Suggested:
							bgColor, fgColor = p.suggestBg, p.suggestFg
						case opt.Dev:
							bgColor, fgColor = p.devBg, p.devFg
						case open:
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
									if open || opt.Dev || opt.Suggested {
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
				b := material.Button(th, &btns.deny, "Cancel")
				b.Background = currentTheme().chipBg
				b.Color = currentTheme().chipFg
				return b.Layout(gtx)
			}),
		)
	} else if !p.Remember {
		// nothing to widen the answer into: a plain yes and a plain no
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					pointer.CursorPointer.Add(gtx.Ops)
					return material.Button(th, &btns.allow, "Allow").Layout(gtx)
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					pointer.CursorPointer.Add(gtx.Ops)
					b := material.Button(th, &btns.deny, "Deny")
					b.Background = currentTheme().chipBg
					b.Color = currentTheme().chipFg
					return b.Layout(gtx)
				}),
			)
		}))
	} else {
		// the scopes: a row for this prompt, a row for this session, a row
		// for as long as the user leaves the answer there. Only the plain
		// allow and deny are full size; the wider answers are quieter.
		chipBtn := func(gtx layout.Context, btn *widget.Clickable, label string, textSize unit.Sp) layout.Dimensions {
			pointer.CursorPointer.Add(gtx.Ops)
			t := currentTheme()
			b := material.Button(th, btn, label)
			b.Background = t.chipBg
			b.Color = t.chipFg
			b.TextSize = textSize
			b.Inset = layout.UniformInset(unit.Dp(8))
			return b.Layout(gtx)
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
					return material.Button(th, &btns.allow, "Allow").Layout(gtx)
				},
				func(gtx layout.Context) layout.Dimensions {
					return chipBtn(gtx, &btns.deny, "Deny", 0)
				},
			),
			row(
				func(gtx layout.Context) layout.Dimensions {
					return chipBtn(gtx, &btns.sessionAllow, "Allow this session", unit.Sp(13))
				},
				func(gtx layout.Context) layout.Dimensions {
					return chipBtn(gtx, &btns.sessionDeny, "Deny this session", unit.Sp(13))
				},
			),
			row(
				func(gtx layout.Context) layout.Dimensions {
					return chipBtn(gtx, &btns.alwaysAllow, "Always allow", unit.Sp(13))
				},
				func(gtx layout.Context) layout.Dimensions {
					return chipBtn(gtx, &btns.alwaysDeny, "Always deny", unit.Sp(13))
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
	loginErr string,
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

func layoutMain(
	gtx layout.Context,
	th *material.Theme,
	tabWindowsBtn,
	tabNappsBtn,
	tabDiscoBtn,
	tabDevBtn,
	themeBtn,
	settingsBtn,
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

	bundleNameEd *widget.Editor,
	createShortcutBtn *widget.Clickable,
	saveShortcutBtn *widget.Clickable,
	cancelShortcutBtn *widget.Clickable,
	shortcutDelBtns,
	shortcutEditBtns []widget.Clickable,

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
	detailCopyAddrBtn *widget.Clickable,
	detailSettingsBtn *widget.Clickable,
	profileList *widget.List,
	profileCardBtns,
	profileOpenBtns,
	profileActionBtns,
	profileUpdateBtns []widget.Clickable,
) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layoutProfile(gtx, th, themeBtn, settingsBtn, logoutBtn, st.ProfileName, st.ProfilePicture)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(16)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layoutTabs(gtx, th, tabWindowsBtn, tabNappsBtn, tabDiscoBtn, tabDevBtn, tab, len(st.Installed), len(st.Discovery), extra, extraBtn)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(12)}.Layout),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			if editing := currentShortcutEdit(); editing != nil {
				return layoutShortcutEditor(gtx, th, saveShortcutBtn, cancelShortcutBtn, bundleNameEd, editing)
			}
			if tab == 0 {
				return layoutWindowsTab(gtx, th, windowsList,
					closeBtns, reopenBtns, bundleNameEd, createShortcutBtn,
					shortcutDelBtns, shortcutEditBtns, st)
			}
			if tab == 1 {
				return layoutNappsTab(gtx, th, installedList, installedFilterEd, cardBtns, uninstBtns, installedUpdateBtns, installedOpenBtns, installedAuthorBtns, checkUpdBtn, instVis, st)
			}
			if tab == 2 {
				return layoutDiscoveryTab(gtx, th, discoveryList, relaysEd, filterEd, fetchBtn, actionBtns,
					updateBtns, discoCardBtns, discoOpenBtns, discoAuthorBtns, vis, st.FetchErr, st.Fetching, st.Discovery, st.Lookup, installedSet, busy)
			}
			if tab == 4 && extra != nil {
				if extra.kind == "napp" {
					return layoutNappDetail(gtx, th, extra, detailOpenBtn, detailPrimaryBtn, detailUpdateBtn, detailAuthorBtn, detailCopyAddrBtn, detailSettingsBtn, installedSet, busy, st)
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
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

func layoutTabs(
	gtx layout.Context,
	th *material.Theme,
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
			return tabBtn(gtx, windowsBtn, "Windows", tab == 0)
		}),
		layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return tabBtn(gtx, nappsBtn, "Installed", tab == 1)
		}),
		layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return tabBtn(gtx, discoBtn, "Discovery", tab == 2)
		}),
	}
	// devBtn is nil outside dev builds: no dev tab there
	if devBtn != nil {
		children = append(children,
			layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return tabBtn(gtx, devBtn, "Dev", tab == 3)
			}),
		)
	}
	// the ephemeral detail tab lives at the end and vanishes the moment
	// any other tab is picked
	if extra != nil && extraBtn != nil {
		children = append(children,
			layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return tabBtn(gtx, extraBtn, extra.title, tab == 4)
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
	bundleNameEd *widget.Editor,
	createShortcutBtn *widget.Clickable,
	shortcutDelBtns,
	shortcutEditBtns []widget.Clickable,
	st backend.State,
) layout.Dimensions {
	// nothing but a message only when the tab really has nothing in it: a
	// bundle shortcut outlives the windows it was made from, so a launcher
	// that has opened no window this run still has shortcuts to show.
	if len(st.ManagedWindows) == 0 && len(st.Shortcuts) == 0 {
		l := material.Body2(th, "No windows opened yet.")
		l.Color = currentTheme().muted
		return l.Layout(gtx)
	}
	ui.mu.Lock()
	shortcutErr := ui.shortcutErr
	ui.mu.Unlock()
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		// whatever went wrong the last time a shortcut was saved
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if shortcutErr == "" {
				return layout.Dimensions{}
			}
			return layout.Inset{Bottom: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				l := material.Body2(th, shortcutErr)
				l.Color = currentTheme().danger
				return l.Layout(gtx)
			})
		}),
		// the bundle shortcut builder: only while something is checked, an
		// input for the bundle name next to the create button.
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
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return editorBox(gtx, th, bundleNameEd, "name of the bundle shortcut")
					}),
					layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						pointer.CursorPointer.Add(gtx.Ops)
						return material.Button(th, createShortcutBtn, "Create shortcut\u2026").Layout(gtx)
					}),
				)
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
										hist := historyLabel(w.History)
										if hist == "" {
											return layout.Dimensions{}
										}
										l := material.Caption(th, truncate(hist, 90))
										l.Color = currentTheme().muted
										return l.Layout(gtx)
									}),
								)
							})
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							pointer.CursorPointer.Add(gtx.Ops)
							if w.Open {
								return material.Button(th, &closeBtns[i], "Close").Layout(gtx)
							}
							return material.Button(th, &reopenBtns[i], "Reopen").Layout(gtx)
						}),
					)
				})
			})
		}),
		// the created shortcuts, each with edit and delete
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if len(st.Shortcuts) == 0 {
				return layout.Dimensions{}
			}
			return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						l := emph(material.Body2(th, "Bundle shortcuts"))
						l.Color = currentTheme().subtle
						return l.Layout(gtx)
					}),
					layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx, shortcutsRows(th, st, shortcutDelBtns, shortcutEditBtns)...)
					}),
				)
			})
		}),
	)
}

// shortcutsRows is one row per stored shortcut: the name (and its napps)
// with edit and delete buttons at the end.
func shortcutsRows(th *material.Theme, st backend.State, delBtns, editBtns []widget.Clickable) (out []layout.FlexChild) {
	for i, sc := range st.Shortcuts {
		i := i
		names := make([]string, 0, len(sc.Entries))
		for _, e := range sc.Entries {
			if napp, ok := backend.InstalledNapp(e.NappID); ok {
				names = append(names, napp.Label())
				continue
			}
			names = append(names, e.NappID)
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
						)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						pointer.CursorPointer.Add(gtx.Ops)
						b := material.Button(th, &editBtns[i], "Edit")
						b.TextSize = unit.Sp(13)
						b.Inset = layout.UniformInset(unit.Dp(8))
						return b.Layout(gtx)
					}),
					layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						pointer.CursorPointer.Add(gtx.Ops)
						b := material.Button(th, &delBtns[i], "Delete")
						b.Background = currentTheme().chipBg
						b.Color = currentTheme().danger
						b.TextSize = unit.Sp(13)
						b.Inset = layout.UniformInset(unit.Dp(8))
						return b.Layout(gtx)
					}),
				)
			})
		}))
	}
	return out
}

// layoutShortcutEditor is the overlay a "Create shortcut…" (or an "Edit")
// click opens over the Windows tab: the bundle name, one actions textarea
// per napp of the bundle (one action per line), save and cancel.
func layoutShortcutEditor(
	gtx layout.Context,
	th *material.Theme,
	saveBtn,
	cancelBtn *widget.Clickable,
	nameEd *widget.Editor,
	edit *shortcutEditState,
) layout.Dimensions {
	title := "New bundle shortcut"
	if edit.name != "" {
		title = "Edit shortcut"
	}
	p := currentTheme()
	ui.mu.Lock()
	shortcutErr := ui.shortcutErr
	ui.mu.Unlock()
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			t := material.H6(th, title)
			t.Font.Weight = font.Bold
			return t.Layout(gtx)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return editorBox(gtx, th, nameEd, "name of the bundle")
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if shortcutErr == "" {
				return layout.Dimensions{}
			}
			return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				l := material.Body2(th, shortcutErr)
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
					return material.Button(th, saveBtn, "Save shortcut").Layout(gtx)
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					pointer.CursorPointer.Add(gtx.Ops)
					b := material.Button(th, cancelBtn, "Cancel")
					b.Background = p.chipBg
					b.Color = p.chipFg
					return b.Layout(gtx)
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
	authorBtns []widget.Clickable,
	checkUpdBtn *widget.Clickable,
	vis []int,
	st backend.State,
) layout.Dimensions {
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
				layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					pointer.CursorPointer.Add(gtx.Ops)
					p := currentTheme()
					b := material.Button(th, checkUpdBtn, "\u21bb")
					b.Background = p.chipBg
					b.Color = p.chipFg
					if st.UpdateCheckRunning {
						b.Color = p.muted
					}
					b.TextSize = unit.Sp(15)
					b.Inset = layout.UniformInset(unit.Dp(8))
					return b.Layout(gtx)
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
				return renderNappCard(gtx, th, cardBtn, authorBtn, openBtn, uninstBtn, updateBtn, "Uninstall", "Update", st.Installed[row])
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
	lookup *backend.AddressLookup,
	installedSet,
	busy map[string]bool,
) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		// the filter box comes first, narrowing the entries below by name,
		// author, author name or description, or naming one by its address.
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return editorBox(gtx, th, filterEd, "filter by name, author or description, or paste an naddr")
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
		layout.Rigid(layout.Spacer{Height: unit.Dp(10)}.Layout),
		// the kind tabs (all, napps, napplets) and, at the other end, the
		// refresh button that asks the relays again
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			counts := [3]int{len(discovery)}
			for _, n := range discovery {
				if matchesKind(n, discoKindNapplets) {
					counts[discoKindNapplets]++
				} else {
					counts[discoKindNapps]++
				}
			}
			kindBtn := func(k int) layout.FlexChild {
				return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Right: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						pointer.CursorPointer.Add(gtx.Ops)
						label := discoKindLabels[k]
						if counts[k] > 0 {
							label += " (" + strconv.Itoa(counts[k]) + ")"
						}
						b := material.Button(th, &discoKindBtns[k], label)
						b.TextSize = unit.Sp(13)
						b.Inset = layout.Inset{Top: unit.Dp(6), Bottom: unit.Dp(6), Left: unit.Dp(12), Right: unit.Dp(12)}
						if discoKind == k {
							b.Background = th.Palette.ContrastBg
						} else {
							b.Background = currentTheme().chipBg
							b.Color = currentTheme().chipFg
						}
						return b.Layout(gtx)
					})
				})
			}
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				kindBtn(discoKindAll),
				kindBtn(discoKindNapps),
				kindBtn(discoKindNapplets),
				layout.Flexed(1, layout.Spacer{}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					pointer.CursorPointer.Add(gtx.Ops)
					label := "Refresh"
					if fetching {
						label = "Refreshing\u2026"
					}
					b := material.Button(th, fetchBtn, label)
					b.TextSize = unit.Sp(13)
					b.Inset = layout.Inset{Top: unit.Dp(6), Bottom: unit.Dp(6), Left: unit.Dp(12), Right: unit.Dp(12)}
					return b.Layout(gtx)
				}),
			)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if fetchErr == "" {
				return layout.Dimensions{}
			}
			return layout.Inset{Top: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				l := material.Body2(th, fetchErr)
				l.Color = currentTheme().danger
				return l.Layout(gtx)
			})
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(12)}.Layout),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			if len(vis) == 0 {
				msg := "No napps yet. Pick some good relays and click \"Refresh\"."
				if fetching {
					msg = "Searching relays\u2026"
				}
				if len(discovery) > 0 {
					msg = "Nothing matches the filter."
					if strings.TrimSpace(filterEd.Text()) == "" {
						msg = "No " + strings.ToLower(discoKindLabels[discoKind]) + " found on these relays."
					}
				}
				if lookup != nil {
					switch {
					case lookup.Pending:
						msg = "Looking up that address\u2026"
					case lookup.Err != "":
						msg = "Couldn't open that address: " + lookup.Err + "."
					}
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
				return renderNappCard(gtx, th, cardBtn, authorBtn, openBtn, btn, updBtn, label, updLabel, n)
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
				return renderNappCard(gtx, th, openBtn, nil, nil, publishBtn, unloadBtn, "Publish", "Unload", st.Dev[i])
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

func layoutProfile(
	gtx layout.Context,
	th *material.Theme,
	themeBtn,
	settingsBtn,
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
			label := "\u2699 System"
			switch backend.ThemeMode() {
			case backend.ThemeSystem:
				label = "\u2600 Light"
			case backend.ThemeLight:
				label = "\u263e Dark"
			}
			b := material.Button(th, themeBtn, label)
			b.Background = p.chipBg
			b.Color = p.chipFg
			b.TextSize = unit.Sp(13)
			b.Inset = layout.UniformInset(unit.Dp(8))
			return b.Layout(gtx)
		}),
		layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
		// the launcher's own settings window: relays, Blossom servers
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if settingsBtn == nil {
				return layout.Dimensions{}
			}
			pointer.CursorPointer.Add(gtx.Ops)
			p := currentTheme()
			b := material.Button(th, settingsBtn, "\u2699 Settings")
			b.Background = p.chipBg
			b.Color = p.chipFg
			b.TextSize = unit.Sp(13)
			b.Inset = layout.UniformInset(unit.Dp(8))
			return layout.Inset{Right: unit.Dp(8)}.Layout(gtx, b.Layout)
		}),
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
							if !napp.IsNapplet() {
								return label.Layout(gtx)
							}
							// napplets run sandboxed, through a different runtime:
							// worth telling apart at a glance
							return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
								layout.Rigid(label.Layout),
								layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
								layout.Rigid(func(gtx layout.Context) layout.Dimensions {
									return actionChip(gtx, th, "napplet")
								}),
							)
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
								return authorBtn.Layout(gtx, inner)
							})
						}),
					)
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
						return b.Layout(gtx)
					})
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
