package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"verdana/backend"

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

	// shortcutEditing is the bundle shortcut being created (nil checked
	// windows) or edited (a stored one), parked over the Windows tab the
	// same dialogs are; nil the rest of the time.
	shortcutEditing *shortcutEditState

	// shortcutErr is why the last shortcut create/edit attempt didn't work,
	// shown on the Windows tab (the editor keeps itself open on error).
	shortcutErr string
}

// shortcutEditState is the editor overlay for one bundle: the fields it is
// built from (one napp, its action textarea) and the name of the shortcut
// when it is a stored one being edited ("": creating a new one).
type shortcutEditState struct {
	name    string // the stored shortcut being edited, "" for a new one
	entries []shortcutEditEntry
}

type shortcutEditEntry struct {
	nappID string
	label  string
	ed     widget.Editor
}

// checked is the bundle-creation checkboxes of the Windows tab, keyed by
// window instance, living across frames so state survives redraws.
var bundleChecks = make(map[string]*widget.Bool)

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

	// startup arguments are bundle shortcut invocations: tokens like
	// "<napp-id> +<action> …" coming from a bundle's OS shortcut file. When
	// a launcher is already running they were forwarded there and we never
	// got this far; with none running, this process serves it. Tool and
	// toolkit flags are left for gio and friends to chew on.
	var tokenArgs []string
	for _, arg := range os.Args[1:] {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		tokenArgs = append(tokenArgs, arg)
	}
	startupToken := strings.Join(tokenArgs, " ")

	if startupToken != "" && forwardToInstance(dataDir+"/Verdana", startupToken) {
		log.Info().Str("token", previewToken(startupToken)).
			Msg("forwarded a bundle invocation to the running launcher")
		return
	}

	if startupToken != "" {
		log.Info().Str("token", previewToken(startupToken)).Msg("bundle invocation at startup")
		go backend.RunShortcutToken(startupToken)
	}
	startInstanceListener(filepath.Join(dataDir, "Verdana"))

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

	// startup tab: installed if any napps, else discovery
	if len(backend.Snapshot().Installed) > 0 {
		ui.tab = 1
	} else {
		ui.tab = 2
	}

	gioMain()

	backend.CloseAllWindows()
	killAllChildren()
}

func setTab(t int) {
	ui.tab = t
	if gioWin != nil {
		gioWin.Invalidate()
	}
}

func setConfirmLogout(v bool) {
	ui.confirmLogout = v
	if gioWin != nil {
		gioWin.Invalidate()
	}
}

func setShortcutErr(msg string) {
	ui.mu.Lock()
	ui.shortcutErr = msg
	ui.mu.Unlock()
	if gioWin != nil {
		gioWin.Invalidate()
	}
}

// currentShortcutEdit is the bundle editor parked over the Windows tab, or
// nil. Background saves put their editor back through it, so the field is
// never touched off the Gio loop without the mutex.
func currentShortcutEdit() *shortcutEditState {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	return ui.shortcutEditing
}

func setShortcutEdit(edit *shortcutEditState) {
	ui.mu.Lock()
	ui.shortcutEditing = edit
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
		loginEd             widget.Editor
		loginBtn            widget.Clickable
		relaysEd            widget.Editor
		fetchBtn            widget.Clickable
		tabNappsBtn         widget.Clickable
		tabDiscoBtn         widget.Clickable
		tabDevBtn           widget.Clickable
		tabWindowsBtn       widget.Clickable
		themeBtn            widget.Clickable
		logoutBtn           widget.Clickable
		confirmYesBtn       widget.Clickable
		confirmNoBtn        widget.Clickable
		installedList       widget.List
		discoveryList       widget.List
		devList             widget.List
		windowsList         widget.List
		devURLed            widget.Editor
		devPathEd           widget.Editor
		loadURLBtn          widget.Clickable
		browseBtn           widget.Clickable
		loadFolderBtn       widget.Clickable
		devOpenBtns         []widget.Clickable
		devUnloadBtns       []widget.Clickable
		devPublishBtns      []widget.Clickable
		closeBtns           []widget.Clickable
		reopenBtns          []widget.Clickable
		cardBtns            []widget.Clickable
		uninstBtns          []widget.Clickable
		installedUpdateBtns []widget.Clickable
		actionBtns          []widget.Clickable
		updateBtns          []widget.Clickable
		checkUpdBtn         widget.Clickable
		approveBtn          widget.Clickable
		denyBtn             widget.Clickable
		optBtns             []widget.Clickable

		bundleNameEd      widget.Editor
		createShortcutBtn widget.Clickable
		saveShortcutBtn   widget.Clickable
		cancelShortcutBtn widget.Clickable
		shortcutDelBtns   []widget.Clickable
		shortcutEditBtns  []widget.Clickable
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
	windowsList.Axis = layout.Vertical
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
						input := strings.TrimSpace(loginEd.Text())
						if input != "" {
							go backend.Login(input)
						}
					}
					return layoutLogin(gtx, th, &loginEd, &loginBtn, st.LoginErr)
				case backend.PhaseMain:
					if tabWindowsBtn.Clicked(gtx) {
						setTab(0)
					}
					if tabNappsBtn.Clicked(gtx) {
						setTab(1)
					}
					if tabDiscoBtn.Clicked(gtx) {
						setTab(2)
					}
					if devEnabled && tabDevBtn.Clicked(gtx) {
						setTab(3)
					}
					if themeBtn.Clicked(gtx) {
						toggleTheme()
					}
					if logoutBtn.Clicked(gtx) {
						setConfirmLogout(true)
					}
					if fetchBtn.Clicked(gtx) {
						backend.SetRelays(parseRelays(relaysEd.Text()))
						go backend.Discover()
					}
					for len(cardBtns) < len(st.Installed) {
						cardBtns = append(cardBtns, widget.Clickable{})
					}
					for len(uninstBtns) < len(st.Installed) {
						uninstBtns = append(uninstBtns, widget.Clickable{})
					}
					for len(installedUpdateBtns) < len(st.Installed) {
						installedUpdateBtns = append(installedUpdateBtns, widget.Clickable{})
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
					for len(closeBtns) < len(st.ManagedWindows) {
						closeBtns = append(closeBtns, widget.Clickable{})
						reopenBtns = append(reopenBtns, widget.Clickable{})
					}
					for len(shortcutDelBtns) < len(st.Shortcuts) {
						shortcutDelBtns = append(shortcutDelBtns, widget.Clickable{})
						shortcutEditBtns = append(shortcutEditBtns, widget.Clickable{})
					}
					// a checkbox per window row, living across frames
					for _, w := range st.ManagedWindows {
						if bundleChecks[w.Instance] == nil {
							bundleChecks[w.Instance] = new(widget.Bool)
						}
					}
					vis := discoveryFilter(st)
					instVis := installedFilter(st)
					if tab == 0 {
						for i, w := range st.ManagedWindows {
							if w.Open {
								if closeBtns[i].Clicked(gtx) {
									backend.CloseWindow(w.Instance)
								}
							} else if reopenBtns[i].Clicked(gtx) {
								backend.ReopenWindow(w.Instance)
							}
						}
						if ui.shortcutEditing != nil {
							if saveShortcutBtn.Clicked(gtx) {
								name := strings.TrimSpace(bundleNameEd.Text())
								oldName := ui.shortcutEditing.name
								spec, err := shortcutSpecJSON(ui.shortcutEditing)
								switch {
								case err != nil:
									setShortcutErr(err.Error())
								case name == "":
									setShortcutErr("give the bundle a name")
								default:
									// validation passed: the selection is consumed
									// here, on the Gio loop; the shortcut itself is
									// written in the background
									editing := ui.shortcutEditing
									ui.shortcutEditing = nil
									clearBundleChecks()
									go saveShortcut(name, oldName, spec, editing)
								}
							}
							if cancelShortcutBtn.Clicked(gtx) {
								ui.shortcutEditing = nil
							}
						} else {
							if createShortcutBtn.Clicked(gtx) {
								entries := pickedBundleWindows(st)
								if len(entries) > 0 {
									setShortcutEdit(newShortcutEditState(entries))
									setShortcutErr("")
								}
							}
							for i, sc := range st.Shortcuts {
								if shortcutDelBtns[i].Clicked(gtx) {
									go backend.DeleteShortcut(sc.Name)
								}
								if shortcutEditBtns[i].Clicked(gtx) {
									setShortcutEdit(editShortcutEditState(sc))
									bundleNameEd.SetText(sc.Name)
									setShortcutErr("")
								}
							}
						}
					} else if tab == 1 {
						// buttons on top of the card's own click area go
						// first: a click that hit a button must not also
						// count as opening the napp.
						acted := false
						for _, i := range instVis {
							if installedUpdateBtns[i].Clicked(gtx) {
								go backend.Update(st.Installed[i].ID)
								acted = true
							}
						}
						if !acted {
							for _, i := range instVis {
								if uninstBtns[i].Clicked(gtx) {
									go backend.Uninstall(st.Installed[i].ID)
									acted = true
								}
							}
						}
						if !acted {
							for _, i := range instVis {
								if cardBtns[i].Clicked(gtx) {
									backend.Launch(st.Installed[i])
								}
							}
						}
					} else if tab == 2 {
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
					return layoutMain(gtx,
						th,
						&tabWindowsBtn,
						&tabNappsBtn,
						&tabDiscoBtn,
						devBtn,
						&themeBtn,
						&logoutBtn,
						tab,

						&windowsList,
						&installedList,
						&discoveryList,
						&devList,
						&relaysEd,
						&filterEd,
						&installedFilterEd,

						&devURLed,
						&devPathEd,
						&fetchBtn,
						&checkUpdBtn,
						&loadURLBtn,
						&browseBtn,
						&loadFolderBtn,

						closeBtns,
						reopenBtns,
						cardBtns,
						uninstBtns,
						installedUpdateBtns,
						actionBtns,
						updateBtns,
						devOpenBtns,
						devUnloadBtns,
						devPublishBtns,

						&bundleNameEd,
						&createShortcutBtn,
						&saveShortcutBtn,
						&cancelShortcutBtn,
						shortcutDelBtns,
						shortcutEditBtns,

						vis,
						instVis,
						st,
						installedSet,
						busy,
					)
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
