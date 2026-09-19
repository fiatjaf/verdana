package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"gioui.org/app"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"verdana/backend"
)

// openDevPublishWindow opens a separate Gio form for local folder napps.
func openDevPublishWindow(id string) {
	info, err := backend.DevPublishInfoFor(id)
	if err != nil {
		log.Error().Err(err).Str("napp", id).Msg("could not prepare dev publish window")
		return
	}
	go func() {
		servers, relays := backend.DevPublishDefaults(context.Background())
		runDevPublishWindow(info, servers, relays)
	}()
}

type devPublishState struct {
	sync.Mutex
	publishing bool
	status     string
}

func runDevPublishWindow(info backend.DevPublishInfo, servers, relays []string) {
	w := new(app.Window)
	w.Option(app.Title("Publish "+info.Napp.Name), app.Size(unit.Dp(620), unit.Dp(720)))

	var serverEd, relayEd widget.Editor
	serverEd.SingleLine = false
	relayEd.SingleLine = false
	serverEd.SetText(strings.Join(servers, "\n"))
	relayEd.SetText(strings.Join(relays, "\n"))
	protected := widget.Bool{}
	confirm := widget.Clickable{}
	files := widget.List{}
	files.Axis = layout.Vertical
	form := widget.List{}
	form.Axis = layout.Vertical
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

			state.Lock()
			publishing, status := state.publishing, state.status
			state.Unlock()
			if confirm.Clicked(gtx) && !publishing {
				chosenServers := parsePublishTargets(serverEd.Text())
				chosenRelays := parsePublishTargets(relayEd.Text())
				protectedValue := protected.Value
				state.Lock()
				state.publishing = true
				state.status = "Uploading files…"
				state.Unlock()
				w.Invalidate()
				go func() {
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
					defer cancel()
					published, failed, err := backend.PublishDev(ctx, info.Napp.ID, chosenServers, chosenRelays, protectedValue)
					state.Lock()
					state.publishing = false
					if err != nil {
						state.status = "Error: " + err.Error()
					} else {
						state.status = fmt.Sprintf("Published to %d relay(s), %d failed", published, failed)
					}
					state.Unlock()
					w.Invalidate()
				}()
			}

			layout.UniformInset(unit.Dp(18)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layoutPublishForm(gtx, th, &form, &serverEd, &relayEd, &protected, &confirm,
					&files, info, publishing, status)
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

func layoutPublishForm(gtx layout.Context, th *material.Theme, form *widget.List, servers, relays *widget.Editor, protected *widget.Bool, confirm *widget.Clickable, files *widget.List, info backend.DevPublishInfo, publishing bool, status string) layout.Dimensions {
	label := func(text string) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			return material.Body1(th, text).Layout(gtx)
		}
	}
	return material.List(th, form).Layout(gtx, 1, func(gtx layout.Context, _ int) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(label("Publish "+info.Napp.Name)),
			layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
			layout.Rigid(label("id: "+info.Napp.D)),
			layout.Rigid(label("icon: "+info.Napp.Icon)),
			layout.Rigid(label("description: "+info.Napp.Description)),
			layout.Rigid(label("singleton: "+fmt.Sprint(info.Napp.Singleton))),
			layout.Rigid(label("requires: "+strings.Join(info.Napp.Requires, ", "))),
			layout.Rigid(label("actions: "+strings.Join(info.Napp.Actions, ", "))),
			layout.Rigid(layout.Spacer{Height: unit.Dp(12)}.Layout),
			layout.Rigid(label("Files")),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return material.List(th, files).Layout(gtx, len(info.Files), func(gtx layout.Context, i int) layout.Dimensions {
					f := info.Files[i]
					return layout.Inset{Top: unit.Dp(3)}.Layout(gtx, material.Body2(th, fmt.Sprintf("%s  (%d bytes)", f.Path, f.Size)).Layout)
				})
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(12)}.Layout),
			layout.Rigid(label("Blossom servers, one per line")),
			layout.Rigid(material.Editor(th, servers, "https://blossom.example.com").Layout),
			layout.Rigid(layout.Spacer{Height: unit.Dp(10)}.Layout),
			layout.Rigid(label("Relays, one per line")),
			layout.Rigid(material.Editor(th, relays, "wss://relay.example.com").Layout),
			layout.Rigid(material.CheckBox(th, protected, "NIP-70 protected event").Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				button := material.Button(th, confirm, map[bool]string{true: "Publishing…", false: "Upload, sign and publish"}[publishing])
				return button.Layout(gtx)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if status == "" {
					return layout.Dimensions{}
				}
				return layout.Inset{Top: unit.Dp(10)}.Layout(gtx, material.Body2(th, status).Layout)
			}),
		)
	})
}
