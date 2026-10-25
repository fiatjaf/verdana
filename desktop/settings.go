package main

import (
	"image"
	"time"

	"gioui.org/font"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
	"golang.org/x/exp/shiny/materialdesign/icons"

	"verdana/backend"
)

// The settings tab, opened from the gear in the header: authenticating on
// reads, what napps get when they ask to sign, to publish or to open a link, the default
// handlers napp configurations set (which can only be taken away here), and
// every configuration the launcher ever loaded, each one line that unfolds
// into what it sets, with a button to apply it.

var settingsState struct {
	list          widget.List
	readAuth      [2]widget.Clickable
	openLinks     [3]widget.Clickable
	sign, publish [3]widget.Clickable
	handlers      map[string]*widget.Clickable // their remove buttons, by action
	configs       map[string]*configBtns       // by configuration key
}

// configBtns are one configuration's line, which unfolds it, and its Apply
// button.
type configBtns struct {
	fold  widget.Clickable
	apply widget.Clickable
	open  bool
}

var (
	policyChoices = [3]backend.Decision{backend.DecisionAsk, backend.DecisionAllow, backend.DecisionDeny}
	linkChoices   = [3]backend.LinkPolicy{backend.LinksAll, backend.LinksNonImage, backend.LinksNone}
	gearIcon      = mustIcon(icons.ActionSettings)
)

func mustIcon(data []byte) *widget.Icon {
	ic, err := widget.NewIcon(data)
	if err != nil {
		panic(err)
	}
	return ic
}

// settingsButton is the header's gear.
func settingsButton(gtx layout.Context, btn *widget.Clickable) layout.Dimensions {
	p := currentTheme()
	return withTooltip(gtx, btn, "Settings", func(gtx layout.Context) layout.Dimensions {
		return btn.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			pointer.CursorPointer.Add(gtx.Ops)
			return layout.Stack{}.Layout(gtx,
				layout.Expanded(func(gtx layout.Context) layout.Dimensions {
					sz := gtx.Constraints.Min
					defer clip.UniformRRect(image.Rectangle{Max: sz}, gtx.Dp(6)).Push(gtx.Ops).Pop()
					paint.Fill(gtx.Ops, p.chipBg)
					return layout.Dimensions{Size: sz}
				}),
				layout.Stacked(func(gtx layout.Context) layout.Dimensions {
					return layout.UniformInset(unit.Dp(7)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Min.X = gtx.Dp(20)
						return gearIcon.Layout(gtx, p.chipFg)
					})
				}),
			)
		})
	})
}

// layoutSettings is the settings tab.
func layoutSettings(gtx layout.Context, th *material.Theme, st backend.State) layout.Dimensions {
	s := &settingsState
	s.list.Axis = layout.Vertical
	if s.handlers == nil {
		s.handlers = make(map[string]*widget.Clickable)
		s.configs = make(map[string]*configBtns)
	}

	for i, on := range []bool{true, false} {
		if s.readAuth[i].Clicked(gtx) {
			backend.SetReadAuth(on)
		}
	}
	for i, p := range linkChoices {
		if s.openLinks[i].Clicked(gtx) {
			if err := backend.SetOpenLinks(p); err != nil {
				backend.SetFetchErr(err.Error())
			}
		}
	}
	for i := range policyChoices {
		if s.sign[i].Clicked(gtx) {
			setPolicy(backend.PermSign, policyChoices[i])
		}
		if s.publish[i].Clicked(gtx) {
			setPolicy(backend.PermPublish, policyChoices[i])
		}
	}

	var rows []layout.Widget
	heading := func(text string) {
		rows = append(rows, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(18), Bottom: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				t := emph(material.Body1(th, text))
				t.Color = currentTheme().subtle
				return t.Layout(gtx)
			})
		})
	}
	note := func(text string) {
		rows = append(rows, func(gtx layout.Context) layout.Dimensions {
			l := material.Body2(th, text)
			l.Color = currentTheme().muted
			return layout.Inset{Bottom: unit.Dp(6)}.Layout(gtx, l.Layout)
		})
	}

	heading("Relays")
	rows = append(rows, func(gtx layout.Context) layout.Dimensions {
		readAuth := 1
		if st.ReadAuth {
			readAuth = 0
		}
		return choiceRow(gtx, th, "Authenticate when reading", s.readAuth[:], []string{"Yes", "No"}, []string{
			"Tell relays that ask for it who you are (NIP-42) when reading",
			"Don't tell relays who you are when reading (publishing always does)",
		}, readAuth)
	})

	heading("Permissions")
	note("What napps get when they ask, unless you answered for that napp already.")
	for _, p := range []struct {
		label, what string
		btns        *[3]widget.Clickable
		current     backend.Decision
	}{
		{"Napps signing", "sign", &s.sign, st.SignPolicy},
		{"Napps publishing", "publish", &s.publish, st.PublishPolicy},
	} {
		rows = append(rows, func(gtx layout.Context) layout.Dimensions {
			current := 0
			for i, d := range policyChoices {
				if d == p.current {
					current = i
				}
			}
			return layout.Inset{Bottom: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return choiceRow(gtx, th, p.label, p.btns[:], []string{"Ask", "Allow", "Deny"}, []string{
					"Ask every time a napp wants to " + p.what,
					"Let napps " + p.what + " without asking",
					"Never let napps " + p.what,
				}, current)
			})
		})
	}

	rows = append(rows, func(gtx layout.Context) layout.Dimensions {
		current := 2
		for i, p := range linkChoices {
			if p == st.OpenLinks {
				current = i
			}
		}
		return layout.Inset{Bottom: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return choiceRow(gtx, th, "Open links without asking", s.openLinks[:], []string{"Yes", "Non-image", "No"}, []string{
				"Let napps open every link in your browser without asking, images too",
				"Let napps open links in your browser without asking, but show images over the napp",
				"Ask every time a napp wants to open a link in your browser (images are shown over the napp)",
			}, current)
		})
	})

	heading("Default handlers")
	if len(st.DefaultHandlers) == 0 {
		note("None: every action with more than one napp to take it asks which one. Napp configurations set them.")
	}
	for _, h := range st.DefaultHandlers {
		btn := s.handlers[h.Action]
		if btn == nil {
			btn = new(widget.Clickable)
			s.handlers[h.Action] = btn
		}
		if btn.Clicked(gtx) {
			backend.RemoveDefaultHandler(h.Action)
		}
		rows = append(rows, func(gtx layout.Context) layout.Dimensions {
			mode := string(h.Mode)
			if h.Singleton {
				mode += ", in its open window"
			}
			return layout.Inset{Bottom: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						l := material.Body2(th, h.Action+" → "+h.TargetName+" · "+mode)
						l.MaxLines = 1
						return l.Layout(gtx)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return choiceChip(gtx, th, btn, "Delete", "Ask which napp handles “"+h.Action+"” again", false)
					}),
				)
			})
		})
	}

	heading("Napp configurations")
	if len(st.Configurations) == 0 {
		note("None loaded yet: open the code of a napp configuration (kind 35133) to see it here.")
	} else {
		note("Every one the launcher has loaded. Click one to see what it sets.")
	}
	for _, c := range st.Configurations {
		btns := s.configs[c.Key]
		if btns == nil {
			btns = &configBtns{}
			s.configs[c.Key] = btns
		}
		if btns.fold.Clicked(gtx) {
			btns.open = !btns.open
		}
		if btns.apply.Clicked(gtx) {
			if err := backend.ApplyConfiguration(c.Key); err != nil {
				backend.SetFetchErr(err.Error())
			}
		}
		rows = append(rows, func(gtx layout.Context) layout.Dimensions {
			return configRow(gtx, th, c, btns)
		})
	}

	return layout.UniformInset(unit.Dp(4)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return material.List(th, &s.list).Layout(gtx, len(rows), func(gtx layout.Context, i int) layout.Dimensions {
			return rows[i](gtx)
		})
	})
}

func setPolicy(perm backend.Permission, d backend.Decision) {
	if err := backend.SetNappPolicy(perm, d); err != nil {
		backend.SetFetchErr(err.Error())
	}
}

// configRow is one configuration: a line saying what it is, which unfolds
// into the lines of what it sets, and its Apply button.
func configRow(gtx layout.Context, th *material.Theme, c backend.ConfigurationInfo, btns *configBtns) layout.Dimensions {
	p := currentTheme()
	by := c.AuthorName
	if by == "" && len(c.Author) >= 12 {
		by = c.Author[:12] + "…"
	}
	when := time.Unix(int64(c.CreatedAt), 0).Format("2006-01-02")
	arrow := "▸ "
	if btns.open {
		arrow = "▾ "
	}
	applyLabel, applyTip := "Apply", "Set everything this configuration says"
	if c.Applied {
		applyLabel, applyTip = "Apply again", "Applied already: set everything it says over again"
	}

	line := func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return withTooltip(gtx, &btns.fold, "Show what it sets", func(gtx layout.Context) layout.Dimensions {
					return btns.fold.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						pointer.CursorPointer.Add(gtx.Ops)
						gtx.Constraints.Min.X = gtx.Constraints.Max.X
						return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								l := material.Body2(th, arrow+c.Name)
								l.Font.Weight = font.Bold
								return l.Layout(gtx)
							}),
							layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
								l := material.Body2(th, "  "+c.Summary+" · by "+by+" · "+when)
								l.Color = p.muted
								l.MaxLines = 1
								return l.Layout(gtx)
							}),
						)
					})
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if !c.Applied {
					return layout.Dimensions{}
				}
				l := material.Caption(th, "applied  ")
				l.Color = p.subtle
				return l.Layout(gtx)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return choiceChip(gtx, th, &btns.apply, applyLabel, applyTip, false)
			}),
		)
	}
	children := []layout.FlexChild{layout.Rigid(line)}
	if btns.open {
		lines := c.Lines
		if len(lines) == 0 {
			lines = []string{"nothing"}
		}
		for _, text := range lines {
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: unit.Dp(18), Top: unit.Dp(3)}.Layout(gtx, material.Body2(th, "· "+text).Layout)
			}))
		}
	}
	return layout.Inset{Bottom: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
	})
}

// choiceRow is a label and its choices, the one in force (current)
// highlighted.
func choiceRow(gtx layout.Context, th *material.Theme, label string, btns []widget.Clickable,
	labels, tips []string, current int) layout.Dimensions {
	children := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Dp(200)
			return material.Body2(th, label).Layout(gtx)
		}),
	}
	for i := range btns {
		children = append(children,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return choiceChip(gtx, th, &btns[i], labels[i], tips[i], i == current)
			}),
			layout.Rigid(layout.Spacer{Width: unit.Dp(4)}.Layout),
		)
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
}

// choiceChip is a small button, in the contrast colour when it is the
// choice in force.
func choiceChip(gtx layout.Context, th *material.Theme, btn *widget.Clickable, label, tip string, selected bool) layout.Dimensions {
	p := currentTheme()
	bg, fg := p.chipBg, p.chipFg
	if selected {
		bg, fg = p.contrastBg, p.contrastFg
	}
	return withTooltip(gtx, btn, tip, func(gtx layout.Context) layout.Dimensions {
		return btn.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			pointer.CursorPointer.Add(gtx.Ops)
			l := material.Caption(th, label)
			l.Color = fg
			return layout.Stack{}.Layout(gtx,
				layout.Expanded(func(gtx layout.Context) layout.Dimensions {
					sz := gtx.Constraints.Min
					defer clip.UniformRRect(image.Rectangle{Max: sz}, gtx.Dp(5)).Push(gtx.Ops).Pop()
					paint.Fill(gtx.Ops, bg)
					return layout.Dimensions{Size: sz}
				}),
				layout.Stacked(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Top: 4, Bottom: 4, Left: 8, Right: 8}.Layout(gtx, l.Layout)
				}),
			)
		})
	})
}
