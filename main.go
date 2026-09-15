package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"fiatjaf.com/nostr/sdk"
	"gioui.org/app"
	"gioui.org/io/clipboard"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
	"github.com/rs/zerolog"
)

var (
	sys       *sdk.System
	ui        = uiState{phase: "loading", busy: make(map[string]bool)}
	openReqCh = make(chan openReq, 16)
	gioWin    *app.Window
	log       zerolog.Logger
)

var (
	mu         sync.Mutex
	children   []*childInfo
	verdanaDir string
)

const APP_TITLE = "Verdana"

func main() {
	pid := os.Getpid()
	log = zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr}).With().
		Int("_", pid).
		Timestamp().
		Logger()
	log.Info().Msg("starting verdana")

	dataDir, err := app.DataDir()
	if err != nil {
		log.Fatal().Err(err).Msg("no data dir")
	}
	verdanaDir = filepath.Join(dataDir, "Verdana")
	os.MkdirAll(verdanaDir, 0755)
	closer := initSystem(verdanaDir)
	defer closer()

	loadState()
	applyStoredTheme()
	refreshInstalled()
	go buildUserIndex()

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
	th.Shaper = text.NewShaper(text.WithCollection(fontCollection()))
	th.Face = "vFont"

	w := new(app.Window)
	w.Option(app.Title(APP_TITLE), app.Size(unit.Dp(560), unit.Dp(640)))
	gioWin = w

	var (
		loginEd       widget.Editor
		loginBtn      widget.Clickable
		relaysEd      widget.Editor
		fetchBtn      widget.Clickable
		tabNappsBtn   widget.Clickable
		tabDiscoBtn   widget.Clickable
		themeBtn      widget.Clickable
		installedList widget.List
		discoveryList widget.List
		runBtns       []widget.Clickable
		actionBtns    []widget.Clickable
		approveBtn    widget.Clickable
		denyBtn       widget.Clickable
		optBtns       []widget.Clickable
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

			// the palette is re-read every frame, so a theme switch (which can
			// come from any goroutine) never touches th concurrently
			pal := currentTheme()
			pal.apply(th)
			paint.Fill(gtx.Ops, pal.bg)

			ui.mu.Lock()
			activePrompt := ui.prompt
			pendingCopies := ui.clipboard
			ui.clipboard = nil
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

			// copyText can only reach the clipboard from inside a frame
			for _, text := range pendingCopies {
				gtx.Execute(clipboard.WriteCmd{
					Type: "application/text",
					Data: io.NopCloser(strings.NewReader(text)),
				})
			}

			layout.UniformInset(unit.Dp(16)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				// a prompt takes over the window until it is answered: the napp
				// that asked is blocked on it
				if activePrompt != nil {
					for len(optBtns) < len(activePrompt.options) {
						optBtns = append(optBtns, widget.Clickable{})
					}
					if approveBtn.Clicked(gtx) {
						answerPrompt(activePrompt, true, 0)
					}
					if denyBtn.Clicked(gtx) {
						answerPrompt(activePrompt, false, 0)
					}
					for i := range activePrompt.options {
						if optBtns[i].Clicked(gtx) {
							answerPrompt(activePrompt, true, i)
						}
					}
					return layoutPrompt(gtx, th, activePrompt, &approveBtn, &denyBtn, optBtns)
				}

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
					if themeBtn.Clicked(gtx) {
						toggleTheme()
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
					return layoutMain(gtx, th, &tabNappsBtn, &tabDiscoBtn, &themeBtn, tab,
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
