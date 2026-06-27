package main

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"fiatjaf.com/nostr/sdk"
	"gioui.org/app"
	"gioui.org/font/gofont"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

var (
	sys       *sdk.System
	ui        = uiState{phase: "loading", busy: make(map[string]bool)}
	openReqCh = make(chan openReq, 16)
	gioWin    *app.Window
)

var (
	mu         sync.Mutex
	children   []*childInfo
	verdanaDir string
)

func main() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	log.Info().Strs("args", os.Args).Msg("starting verdana")

	if nappID := os.Getenv("VERDANA_NAPP_ID"); nappID != "" {
		childMain(nappID)
		return
	}

	dataDir, err := app.DataDir()
	if err != nil {
		log.Fatal().Err(err).Msg("no data dir")
	}
	verdanaDir = filepath.Join(dataDir, "Verdana")
	os.MkdirAll(verdanaDir, 0755)
	closer := initSystem(verdanaDir)
	defer closer()

	loadState()
	refreshInstalled()

	go gioMain()
	go webviewServer()
	app.Main()

	killAllChildren()
}

func killAllChildren() {
	mu.Lock()
	snapshot := append([]*childInfo(nil), children...)
	mu.Unlock()
	for _, ci := range snapshot {
		if ci.cmd != nil && ci.cmd.Process != nil {
			ci.cmd.Process.Kill()
		}
	}
	log.Info().Int("count", len(snapshot)).Msg("killed all child processes")
}

func gioMain() {
	th := material.NewTheme()
	th.Shaper = text.NewShaper(text.WithCollection(gofont.Collection()))

	w := new(app.Window)
	w.Option(app.Title("Verdana"), app.Size(unit.Dp(560), unit.Dp(640)))
	gioWin = w

	var (
		loginEd       widget.Editor
		loginBtn      widget.Clickable
		relaysEd      widget.Editor
		fetchBtn      widget.Clickable
		tabNappsBtn   widget.Clickable
		tabDiscoBtn   widget.Clickable
		installedList widget.List
		discoveryList widget.List
		runBtns       []widget.Clickable
		actionBtns    []widget.Clickable
	)
	loginEd.SingleLine = true
	relaysEd.SingleLine = false
	installedList.Axis = layout.Vertical
	discoveryList.Axis = layout.Vertical
	relaysEd.SetText(relayListText())

	if strings.TrimSpace(state.Login) != "" {
		go doLogin(state.Login)
	} else {
		setPhase("login")
	}

	var ops op.Ops
	for {
		switch e := w.Event(); e := e.(type) {
		case app.FrameEvent:
			gtx := app.NewContext(&ops, e)

			ui.mu.Lock()
			phase := ui.phase
			tab := ui.tab
			loginErr := ui.loginErr
			profName := ui.profName
			profPic := ui.profPic
			fetchErr := ui.fetchErr
			fetching := ui.fetching
			discoverySnap := ui.discovery
			installedSnap := ui.installed
			busySnap := make(map[string]bool, len(ui.busy))
			for k, v := range ui.busy {
				busySnap[k] = v
			}
			ui.mu.Unlock()

			installedSet := make(map[string]bool, len(installedSnap))
			for _, n := range installedSnap {
				installedSet[n.ID] = true
			}

			layout.UniformInset(unit.Dp(16)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				switch phase {
				case "login":
					if loginBtn.Clicked(gtx) {
						in := strings.TrimSpace(loginEd.Text())
						if in != "" {
							setPhase("loading")
							go doLogin(in)
						}
					}
					return layoutLogin(gtx, th, &loginEd, &loginBtn, loginErr)
				case "main":
					if tabNappsBtn.Clicked(gtx) {
						setTab(0)
					}
					if tabDiscoBtn.Clicked(gtx) {
						setTab(1)
					}
					if fetchBtn.Clicked(gtx) {
						stateMu.Lock()
						state.Relays = parseRelays(relaysEd.Text())
						saveState()
						relays := append([]string(nil), state.Relays...)
						stateMu.Unlock()
						go doFetch(relays)
					}
					for len(runBtns) < len(installedSnap) {
						runBtns = append(runBtns, widget.Clickable{})
					}
					for len(actionBtns) < len(discoverySnap) {
						actionBtns = append(actionBtns, widget.Clickable{})
					}
					if tab == 0 {
						for i := range installedSnap {
							if runBtns[i].Clicked(gtx) {
								launchNapp(installedSnap[i])
							}
						}
					} else {
						for i := range discoverySnap {
							if actionBtns[i].Clicked(gtx) {
								n := discoverySnap[i]
								if busySnap[n.ID] {
									continue
								}
								if installedSet[n.ID] {
									go uninstallNapp(n.ID)
								} else {
									go installNapp(n)
								}
							}
						}
					}
					return layoutMain(gtx, th, &tabNappsBtn, &tabDiscoBtn, tab,
						&installedList, &discoveryList, &relaysEd, &fetchBtn,
						runBtns, actionBtns, profName, profPic, fetchErr, fetching,
						installedSnap, discoverySnap, installedSet, busySnap)
				default:
					return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return material.Body1(th, "Loading\u2026").Layout(gtx)
					})
				}
			})

			e.Frame(gtx.Ops)

		case app.DestroyEvent:
			return
		}
	}
}

func relayListText() string {
	stateMu.Lock()
	relays := state.Relays
	stateMu.Unlock()
	var b strings.Builder
	for i, r := range relays {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(r)
	}
	return b.String()
}
