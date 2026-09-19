package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

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

	"verdana/backend"
)

// This is the desktop launcher: a Gio window, and nothing else. Everything it
// shows comes from backend.Snapshot(), everything it does is a backend call,
// and every napp window is a child process (see childproc.go).

// gioState is what belongs to this window alone — the backend owns the rest.
type gioState struct {
	mu  sync.Mutex
	tab int

	// confirmLogout parks the "log out?" dialog over the main screen until
	// the user answers it: logging out closes every napp.
	confirmLogout bool

	// clipboard holds texts napp.utils.copyText asked for: only a Gio frame
	// can execute clipboard.WriteCmd, so the host parks them here and the
	// next frame drains them.
	clipboard []string
}

var (
	ui     gioState
	gioWin *app.Window
	log    zerolog.Logger

	// filterEd and installedFilterEd are the discovery and installed tabs'
	// filter boxes (one window, so one of each is enough; relaysEd stays
	// with the window's other widgets).
	filterEd          widget.Editor
	installedFilterEd widget.Editor
)

const APP_TITLE = "Verdana"

func main() {
	log = zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr}).With().
		Int("_", os.Getpid()).
		Timestamp().
		Logger()
	log.Info().Msg("starting verdana")

	dataDir, err := app.DataDir()
	if err != nil {
		log.Fatal().Err(err).Msg("no data dir")
	}

	closeStores, err := backend.Start(backend.Options{
		DataDir: filepath.Join(dataDir, "Verdana"),
		Host:    gioHost{},
		Log:     &log,
	})
	if err != nil {
		log.Fatal().Err(err).Msg("could not start the backend")
	}
	defer closeStores()

	// the palette the user last chose, and its CSS tokens for napps
	applyStoredTheme()

	go gioMain()
	app.Main()

	backend.CloseAllWindows()
	killAllChildren()
}

func setTab(t int) {
	ui.mu.Lock()
	ui.tab = t
	ui.mu.Unlock()
	if gioWin != nil {
		gioWin.Invalidate()
	}
}

func setConfirmLogout(v bool) {
	ui.mu.Lock()
	ui.confirmLogout = v
	ui.mu.Unlock()
	if gioWin != nil {
		gioWin.Invalidate()
	}
}

// discoveryFilter returns the indices of st.Discovery that pass the filter
// editor's text: a case-insensitive substring on name, description, author
// pubkey and author name. Clicks and rendering both walk this same index
// list, so buttons stay glued to their napp no matter what the filter hides.
func discoveryFilter(st backend.State) []int {
	q := strings.ToLower(strings.TrimSpace(filterEd.Text()))
	return nappFilter(st.Discovery, q)
}

// installedFilter is the same thing for the installed list.
func installedFilter(st backend.State) []int {
	q := strings.ToLower(strings.TrimSpace(installedFilterEd.Text()))
	return nappFilter(st.Installed, q)
}

func nappFilter(list []backend.Napp, q string) []int {
	out := make([]int, 0, len(list))
	for i, n := range list {
		if n.MatchesQuery(q) {
			out = append(out, i)
		}
	}
	return out
}

func gioMain() {
	th := material.NewTheme()
	th.Shaper = text.NewShaper(text.WithCollection(fontCollection()))
	th.Face = "vFont"

	w := new(app.Window)
	w.Option(app.Title(APP_TITLE), app.Size(unit.Dp(560), unit.Dp(640)))
	gioWin = w

	var (
		loginEd        widget.Editor
		loginBtn       widget.Clickable
		relaysEd       widget.Editor
		fetchBtn       widget.Clickable
		tabNappsBtn    widget.Clickable
		tabDiscoBtn    widget.Clickable
		tabDevBtn      widget.Clickable
		themeBtn       widget.Clickable
		logoutBtn      widget.Clickable
		confirmYesBtn  widget.Clickable
		confirmNoBtn   widget.Clickable
		installedList  widget.List
		discoveryList  widget.List
		devList        widget.List
		devURLed       widget.Editor
		devPathEd      widget.Editor
		loadURLBtn     widget.Clickable
		browseBtn      widget.Clickable
		loadFolderBtn  widget.Clickable
		devOpenBtns    []widget.Clickable
		devUnloadBtns  []widget.Clickable
		devPublishBtns []widget.Clickable
		cardBtns       []widget.Clickable
		uninstBtns     []widget.Clickable
		actionBtns     []widget.Clickable
		updateBtns     []widget.Clickable
		checkUpdBtn    widget.Clickable
		approveBtn     widget.Clickable
		denyBtn        widget.Clickable
		optBtns        []widget.Clickable
	)
	loginEd.SingleLine = true
	relaysEd.SingleLine = false
	filterEd.SingleLine = true
	installedFilterEd.SingleLine = true
	devURLed.SingleLine = true
	devPathEd.SingleLine = true
	installedList.Axis = layout.Vertical
	discoveryList.Axis = layout.Vertical
	devList.Axis = layout.Vertical
	relaysEd.SetText(strings.Join(backend.Relays(), "\n"))

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

			st := backend.Snapshot()
			activePrompt := backend.CurrentPrompt()

			ui.mu.Lock()
			tab := ui.tab
			pendingCopies := ui.clipboard
			ui.clipboard = nil
			ui.mu.Unlock()

			installedSet := make(map[string]bool, len(st.Installed))
			for _, n := range st.Installed {
				installedSet[n.ID] = true
			}
			busy := make(map[string]bool, len(st.Busy))
			for _, id := range st.Busy {
				busy[id] = true
			}

			// copyText can only reach the clipboard from inside a frame
			for _, text := range pendingCopies {
				gtx.Execute(clipboard.WriteCmd{
					Type: "application/text",
					Data: io.NopCloser(strings.NewReader(text)),
				})
			}

			layout.UniformInset(unit.Dp(16)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				// a prompt generated by a napp is shown over that napp's own
				// window (it covers the webview until answered), not here.
				// Only launcher-originated prompts take over this window.
				if activePrompt != nil && activePrompt.Instance == "" {
					for len(optBtns) < len(activePrompt.Options) {
						optBtns = append(optBtns, widget.Clickable{})
					}
					if approveBtn.Clicked(gtx) {
						backend.AnswerPrompt(activePrompt.ID, true, 0)
					}
					if denyBtn.Clicked(gtx) {
						backend.AnswerPrompt(activePrompt.ID, false, 0)
					}
					for i := range activePrompt.Options {
						if optBtns[i].Clicked(gtx) {
							backend.AnswerPrompt(activePrompt.ID, true, i)
						}
					}
					return layoutPrompt(gtx, th, activePrompt, &approveBtn, &denyBtn, optBtns)
				}

				if ui.confirmLogout {
					if confirmYesBtn.Clicked(gtx) {
						setConfirmLogout(false)
						go backend.Logout()
					}
					if confirmNoBtn.Clicked(gtx) {
						setConfirmLogout(false)
					}
					return layoutConfirmLogout(gtx, th, &confirmYesBtn, &confirmNoBtn)
				}

				switch st.Phase {
				case backend.PhaseLogin:
					if loginBtn.Clicked(gtx) {
						in := strings.TrimSpace(loginEd.Text())
						if in != "" {
							go backend.Login(in)
						}
					}
					return layoutLogin(gtx, th, &loginEd, &loginBtn, st.LoginErr)
				case backend.PhaseMain:
					if tabNappsBtn.Clicked(gtx) {
						setTab(0)
					}
					if tabDiscoBtn.Clicked(gtx) {
						setTab(1)
					}
					if devEnabled && tabDevBtn.Clicked(gtx) {
						setTab(2)
					}
					if themeBtn.Clicked(gtx) {
						toggleTheme()
					}
					if logoutBtn.Clicked(gtx) {
						setConfirmLogout(true)
					}
					if fetchBtn.Clicked(gtx) {
						backend.SetRelays(parseRelays(relaysEd.Text()))
						go backend.Fetch()
					}
					for len(cardBtns) < len(st.Installed) {
						cardBtns = append(cardBtns, widget.Clickable{})
					}
					for len(uninstBtns) < len(st.Installed) {
						uninstBtns = append(uninstBtns, widget.Clickable{})
					}
					for len(actionBtns) < len(st.Discovery) {
						actionBtns = append(actionBtns, widget.Clickable{})
					}
					for len(updateBtns) < len(st.Discovery) {
						updateBtns = append(updateBtns, widget.Clickable{})
					}
					for len(devOpenBtns) < len(st.Dev) {
						devOpenBtns = append(devOpenBtns, widget.Clickable{})
					}
					for len(devUnloadBtns) < len(st.Dev) {
						devUnloadBtns = append(devUnloadBtns, widget.Clickable{})
					}
					for len(devPublishBtns) < len(st.Dev) {
						devPublishBtns = append(devPublishBtns, widget.Clickable{})
					}
					vis := discoveryFilter(st)
					instVis := installedFilter(st)
					if tab == 0 {
						// buttons on top of the card's own click area go
						// first: a click that hit a button must not also
						// count as opening the napp.
						acted := false
						for _, i := range instVis {
							if uninstBtns[i].Clicked(gtx) {
								go backend.Uninstall(st.Installed[i].ID)
								acted = true
							}
						}
						if !acted {
							for _, i := range instVis {
								if cardBtns[i].Clicked(gtx) {
									backend.Launch(st.Installed[i])
								}
							}
						}
					} else if tab == 1 {
						for _, i := range vis {
							if actionBtns[i].Clicked(gtx) {
								n := st.Discovery[i]
								if busy[n.ID] {
									continue
								}
								if installedSet[n.ID] {
									go backend.Uninstall(n.ID)
								} else {
									go backend.Install(n)
								}
							}
							if updateBtns[i].Clicked(gtx) {
								go backend.Install(st.Discovery[i])
							}
						}
						if checkUpdBtn.Clicked(gtx) && !st.UpdateCheckRunning {
							go backend.CheckForUpdates()
						}
					} else {
						// the dev tab (only reachable in dev builds): load a
						// napp from a folder or a dev-server url, open and
						// unload the ephemeral ones below
						if loadURLBtn.Clicked(gtx) {
							if u := strings.TrimSpace(devURLed.Text()); u != "" {
								go backend.DevLoadURL(u)
							}
						}
						if loadFolderBtn.Clicked(gtx) {
							if p := strings.TrimSpace(devPathEd.Text()); p != "" {
								go backend.DevLoadFolder(p)
							}
						}
						if browseBtn.Clicked(gtx) {
							go pickAndLoadFolder(&devPathEd)
						}
						acted := false
						for i := range st.Dev {
							if devUnloadBtns[i].Clicked(gtx) {
								backend.DevUnload(st.Dev[i].ID)
								acted = true
							}
							if devPublishBtns[i].Clicked(gtx) {
								openDevPublishWindow(st.Dev[i].ID)
								acted = true
							}
						}
						if !acted {
							for i := range st.Dev {
								if devOpenBtns[i].Clicked(gtx) {
									backend.LaunchDev(st.Dev[i].ID)
								}
							}
						}
					}
					var devBtn *widget.Clickable
					if devEnabled {
						devBtn = &tabDevBtn
					}
					return layoutMain(gtx, th, &tabNappsBtn, &tabDiscoBtn, devBtn, &themeBtn, &logoutBtn, tab,
						&installedList, &discoveryList, &devList, &relaysEd, &filterEd, &installedFilterEd,
						&devURLed, &devPathEd, &fetchBtn, &checkUpdBtn, &loadURLBtn, &browseBtn, &loadFolderBtn,
						cardBtns, uninstBtns, actionBtns, updateBtns, devOpenBtns, devUnloadBtns, devPublishBtns,
						vis, instVis, st, installedSet, busy)
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
