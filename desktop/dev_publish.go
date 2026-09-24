package main

import (
	"context"
	"fmt"
	"image"
	"strings"
	"sync"
	"time"
	"verdana/backend"

	"gioui.org/app"
	"gioui.org/font"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

func openDevPublishWindow(id string) {
	info, err := backend.DevPublishInfoFor(id)
	if err != nil {
		log.Error().Err(err).Str("napp", id).Msg("could not prepare dev publish window")
		return
	}

	servers, relays := backend.DevPublishDefaults()
	go runDevPublishWindow(info, servers, relays)
}

type devPublishState struct {
	sync.Mutex
	publishing bool
	log        []string
}

func (s *devPublishState) append(w *app.Window, line string) {
	s.Lock()
	s.log = append(s.log, line)
	s.Unlock()
	w.Invalidate()
}

func (s *devPublishState) snapshot() (bool, []string) {
	s.Lock()
	defer s.Unlock()
	out := make([]string, len(s.log))
	copy(out, s.log)
	return s.publishing, out
}

func runDevPublishWindow(info backend.DevPublishInfo, servers, relays []string) {
	w := new(app.Window)
	w.Option(app.Title("Publish "+info.Napp.Name), app.Size(unit.Dp(660), unit.Dp(780)))

	var serverEd, relayEd widget.Editor
	serverEd.SingleLine = false
	relayEd.SingleLine = false
	serverEd.SetText(strings.Join(servers, "\n"))
	relayEd.SetText(strings.Join(relays, "\n"))
	protected := widget.Bool{}
	confirm := widget.Clickable{}
	form := widget.List{}
	form.Axis = layout.Vertical
	logList := widget.List{}
	logList.Axis = layout.Vertical
	logList.ScrollToEnd = true
	state := &devPublishState{}
	var ops op.Ops
	th := material.NewTheme()
	th.Shaper = text.NewShaper(text.WithCollection(fontCollection()))
	th.Face = "vFont"

	for {
		switch e := w.Event().(type) {
		case app.DestroyEvent:
			return
		case app.FrameEvent:
			gtx := app.NewContext(&ops, e)
			pal := currentTheme()
			pal.apply(th)
			paint.Fill(gtx.Ops, pal.bg)

			publishing, lines := state.snapshot()
			if confirm.Clicked(gtx) && !publishing {
				chosenServers := parsePublishTargets(serverEd.Text())
				chosenRelays := parsePublishTargets(relayEd.Text())
				protectedValue := protected.Value
				nappID := info.Napp.ID
				state.Lock()
				state.publishing = true
				state.log = []string{
					fmt.Sprintf("publishing %q to %d blossom server(s) and %d relay(s)...",
						info.Napp.Name, len(chosenServers), len(chosenRelays)),
				}
				state.Unlock()
				w.Invalidate()
				go func() {
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
					defer cancel()
					onStep := func(line string) { state.append(w, line) }
					_, _, err := backend.PublishDev(ctx, nappID, chosenServers, chosenRelays, protectedValue, onStep)
					state.Lock()
					state.publishing = false
					if err != nil {
						state.log = append(state.log, "error: "+err.Error())
					}
					state.Unlock()
					w.Invalidate()
				}()
				// re-read after starting so the button flips to "Publishing…"
				// and the first log line shows up on this frame.
				publishing, lines = state.snapshot()
			}

			layout.UniformInset(unit.Dp(18)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layoutPublishForm(gtx, th, &form, &logList, &serverEd, &relayEd, &protected, &confirm,
					info, publishing, lines)
			})
			e.Frame(gtx.Ops)
		}
	}
}

func parsePublishTargets(value string) []string {
	var out []string
	for _, line := range strings.FieldsFunc(value, func(r rune) bool { return r == '\n' || r == ',' || r == ' ' || r == '\t' }) {
		line = strings.TrimSpace(line)
		if line != "" && !containsString(out, line) {
			out = append(out, line)
		}
	}
	return out
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

// sectionCard draws a bordered card with a small bold title on top.
func sectionCard(gtx layout.Context, th *material.Theme, title string, content layout.Widget) layout.Dimensions {
	border := widget.Border{
		Color:        currentTheme().border,
		CornerRadius: unit.Dp(8),
		Width:        unit.Dp(1),
	}
	return layout.Inset{Bottom: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return border.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(unit.Dp(12)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						t := material.Body1(th, title)
						t.Font.Weight = font.Bold
						t.TextSize = unit.Sp(14)
						return t.Layout(gtx)
					}),
					layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
					layout.Rigid(content),
				)
			})
		})
	})
}

func publishMetaRow(gtx layout.Context, th *material.Theme, key, value string) layout.Dimensions {
	if value == "" {
		return layout.Dimensions{}
	}
	return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			l := material.Body2(th, key+": ")
			l.Color = currentTheme().muted
			return l.Layout(gtx)
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return material.Body2(th, value).Layout(gtx)
		}),
	)
}

// multiEditorBox is editorBox with a minimum height so multiline fields read
// as clearly editable text areas rather than single-line inputs.
func multiEditorBox(gtx layout.Context, th *material.Theme, ed *widget.Editor, hint string, minHeight unit.Dp) layout.Dimensions {
	gtx.Constraints.Min.Y = gtx.Dp(minHeight)
	return editorBox(gtx, th, ed, hint)
}

func layoutLogLine(gtx layout.Context, th *material.Theme, line string) layout.Dimensions {
	l := material.Body2(th, line)
	l.TextSize = unit.Sp(12)
	switch {
	case strings.Contains(line, "failed") || strings.Contains(line, "mismatch") ||
		strings.HasPrefix(line, "error:"):
		l.Color = currentTheme().danger
	case strings.HasPrefix(strings.TrimSpace(line), "ok") ||
		strings.Contains(line, ": ok") ||
		strings.HasPrefix(line, "signed ") ||
		strings.HasPrefix(line, "done:") ||
		strings.HasPrefix(line, "publishing "):
		l.Color = currentTheme().fg
	default:
		l.Color = currentTheme().subtle
	}
	return layout.Inset{Top: unit.Dp(1), Bottom: unit.Dp(1)}.Layout(gtx, l.Layout)
}

func layoutPublishForm(gtx layout.Context, th *material.Theme, form, logList *widget.List, servers, relays *widget.Editor, protected *widget.Bool, confirm *widget.Clickable, info backend.DevPublishInfo, publishing bool, lines []string) layout.Dimensions {
	return material.List(th, form).Layout(gtx, 1, func(gtx layout.Context, _ int) layout.Dimensions {
		var totalBytes int64
		for _, f := range info.Files {
			totalBytes += f.Size
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				t := material.H6(th, "Publish "+info.Napp.Name)
				t.Font.Weight = font.Bold
				return t.Layout(gtx)
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(12)}.Layout),

			// napp metadata
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return sectionCard(gtx, th, "Napp", func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return publishMetaRow(gtx, th, "id", info.Napp.D)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return publishMetaRow(gtx, th, "description", info.Napp.Description)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return publishMetaRow(gtx, th, "icon", info.Napp.Icon)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return publishMetaRow(gtx, th, "singleton", fmt.Sprint(info.Napp.Singleton))
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return publishMetaRow(gtx, th, "requires", strings.Join(info.Napp.Requires, ", "))
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return publishMetaRow(gtx, th, "actions", strings.Join(info.Napp.Actions, ", "))
						}),
					)
				})
			}),

			// files to upload
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return sectionCard(gtx, th, fmt.Sprintf("Files (%d, %d bytes)", len(info.Files), totalBytes),
					func(gtx layout.Context) layout.Dimensions {
						children := make([]layout.FlexChild, 0, len(info.Files))
						for _, f := range info.Files {
							f := f
							children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return layout.Inset{Top: unit.Dp(2), Bottom: unit.Dp(2)}.Layout(gtx,
									func(gtx layout.Context) layout.Dimensions {
										short := f.Sha256
										if len(short) > 12 {
											short = short[:12]
										}
										l := material.Body2(th, fmt.Sprintf("%s  (%d bytes, %s)", f.Path, f.Size, short))
										l.Color = currentTheme().subtle
										return l.Layout(gtx)
									})
							}))
						}
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
					})
			}),

			// editable targets
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return sectionCard(gtx, th, "Targets", func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							l := material.Body2(th, "Blossom servers, one per line")
							l.Color = currentTheme().subtle
							return l.Layout(gtx)
						}),
						layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return multiEditorBox(gtx, th, servers, "https://blossom.example.com", unit.Dp(64))
						}),
						layout.Rigid(layout.Spacer{Height: unit.Dp(10)}.Layout),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							l := material.Body2(th, "Relays, one per line")
							l.Color = currentTheme().subtle
							return l.Layout(gtx)
						}),
						layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return multiEditorBox(gtx, th, relays, "wss://relay.example.com", unit.Dp(64))
						}),
						layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
						layout.Rigid(material.CheckBox(th, protected, "NIP-70 protected event").Layout),
					)
				})
			}),

			// publish button
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Bottom: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					pointer.CursorPointer.Add(gtx.Ops)
					label := "Upload, sign and publish"
					if publishing {
						label = "Publishing…"
					}
					return material.Button(th, confirm, label).Layout(gtx)
				})
			}),

			// live log of the publish steps
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return sectionCard(gtx, th, "Log", func(gtx layout.Context) layout.Dimensions {
					if len(lines) == 0 {
						l := material.Body2(th, "Nothing published yet. The upload, signing and relay steps will show up here.")
						l.Color = currentTheme().muted
						return l.Layout(gtx)
					}
					border := widget.Border{
						Color:        currentTheme().border,
						CornerRadius: unit.Dp(6),
						Width:        unit.Dp(1),
					}
					return border.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return layout.UniformInset(unit.Dp(8)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							gtx.Constraints.Min.Y = gtx.Dp(unit.Dp(180))
							gtx.Constraints.Max.Y = gtx.Dp(unit.Dp(260))
							macro := op.Record(gtx.Ops)
							dims := material.List(th, logList).Layout(gtx, len(lines),
								func(gtx layout.Context, i int) layout.Dimensions {
									return layoutLogLine(gtx, th, lines[i])
								})
							call := macro.Stop()
							bg := clip.RRect{
								Rect: image.Rectangle{Max: dims.Size},
								NW:   6, NE: 6, SW: 6, SE: 6,
							}
							defer bg.Push(gtx.Ops).Pop()
							paint.Fill(gtx.Ops, currentTheme().codeBg)
							call.Add(gtx.Ops)
							return dims
						})
					})
				})
			}),
		)
	})
}
