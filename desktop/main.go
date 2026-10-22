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

	// bundleEditing is the bundle being created (from the checked
	// windows) or edited (a stored one), parked over the Sessions tab the
	// same dialogs are; nil the rest of the time.
	bundleEditing *bundleEditState

	// bundleErr is why the last bundle save (or pin, or shortcut) didn't work,
	// shown on the Sessions tab (the editor keeps itself open on error).
	bundleErr string
}

// bundleEditState is the editor overlay for one bundle: the fields it is
// built from (one napp, its actions) and the name of the bundle when it is a
// stored one being edited ("": creating a new one).
type bundleEditState struct {
	name    string // the stored bundle being edited, "" for a new one
	nameEd  widget.Editor
	entries []bundleEditEntry
}

// bundleEditEntry is one napp of the bundle being edited, with the actions
// it gets when the bundle opens, each drawn as a pill with a remove button.
type bundleEditEntry struct {
	nappID     string
	label      string
	actions    []backend.ShortcutAction
	removeBtns []widget.Clickable
}

// bundleRowBtns are the buttons of one bundle row on the Sessions tab.
type bundleRowBtns struct {
	pin, shortcut, edit, del widget.Clickable
}

// checked is the bundle-creation checkboxes of the Sessions tab, keyed by
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

	// the napps tab shows what the discovery relays have, what the people
	// the user follows published or what the user published (nappsMode),
	// switched by these. Its dev sub-tab is tabDev.
	nappsMode       = modeDiscovery
	modeDiscoBtn    widget.Clickable
	modeContactsBtn widget.Clickable
	modeMineBtn     widget.Clickable
)

const (
	modeDiscovery = iota
	modeContacts
	modeMine
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

	verdanaDir := filepath.Join(dataDir, "Verdana")

	if startupToken != "" && forwardToInstance(verdanaDir, startupToken) {
		log.Info().Str("token", previewToken(startupToken)).
			Msg("forwarded a bundle invocation to the running launcher")
		return
	}

	// the listener goes up before the backend, so a shortcut clicked while
	// this launcher is still starting finds someone to forward to instead of
	// starting a second one; the tokens it accepts meanwhile wait for the
	// backend to be up (see runBundleToken).
	startInstanceListener(verdanaDir)

	closeStores, err := backend.Start(backend.Options{
		DataDir: verdanaDir,
		Host:    gioHost{},
		Log:     &log,
	})
	if err != nil {
		log.Fatal().Err(err).Msg("could not start the backend")
	}

	// the backend is up: the token this launcher was started with opens its
	// napps, and the ones forwarded in while it was starting stop waiting.
	close(launcherReady)
	if startupToken != "" {
		log.Info().Str("token", previewToken(startupToken)).Msg("bundle invocation at startup")
		go runBundleToken(startupToken)
	}

	// the palette the user last chose, and its CSS tokens for napps
	applyStoredTheme()

	// startup tab: home if anything is pinned or installed, else explore
	if snap := backend.Snapshot(); len(snap.Pinned) > 0 || len(snap.Installed) > 0 {
		ui.tab = tabHome
	} else {
		ui.tab = tabDiscovery
	}

	go func() {
		gioMain()
		backend.CloseAllWindows()
		killAllShells()
		closeStores()
		os.Exit(0)
	}()
	app.Main()
}

func setTab(t int) {
	ui.tab = t
	home.reset()
	if t != tabExtra {
		clearExtraTab()
	}
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

func setBundleErr(msg string) {
	ui.mu.Lock()
	ui.bundleErr = msg
	ui.mu.Unlock()
	if gioWin != nil {
		gioWin.Invalidate()
	}
}

// currentBundleEdit is the bundle editor parked over the Sessions tab, or
// nil. Background saves put their editor back through it, so the field is
// never touched off the Gio loop without the mutex.
func currentBundleEdit() *bundleEditState {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	return ui.bundleEditing
}

func setBundleEdit(edit *bundleEditState) {
	ui.mu.Lock()
	ui.bundleEditing = edit
	ui.mu.Unlock()
	if gioWin != nil {
		gioWin.Invalidate()
	}
}

// discoNapps is the list the napps tab is showing: the discovery relays',
// the contacts' or the user's own.
func discoNapps(st backend.State) []backend.Napp {
	switch currentNappsMode(st) {
	case modeContacts:
		return st.Contacts
	case modeMine:
		return st.Mine
	}
	return st.Discovery
}

// currentNappsMode is nappsMode, except that the mine mode is only there
// while the user has napps stored.
func currentNappsMode(st backend.State) int {
	if nappsMode == modeMine && len(st.Mine) == 0 {
		return modeDiscovery
	}
	return nappsMode
}

// discoveryFilter returns the indices of discoNapps that pass the filter
// editor's text: a case-insensitive substring on name, description, author
// pubkey and author name. Clicks and rendering both walk this same index
// list, so buttons stay glued to their napp no matter what the filter hides.
func discoveryFilter(st backend.State) []int {
	q := strings.ToLower(strings.TrimSpace(filterEd.Text()))
	return nappFilter(discoNapps(st), q)
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
		googleBtn           widget.Clickable
		relaysEd            widget.Editor
		fetchBtn            widget.Clickable
		tabHomeBtn          widget.Clickable
		tabDiscoBtn         widget.Clickable
		tabDevBtn           widget.Clickable
		tabWindowsBtn       widget.Clickable
		themeBtn            widget.Clickable
		readAuthBtn         widget.Clickable
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
		installedOpenBtns   []widget.Clickable
		installedAuthorBtns []widget.Clickable
		installedPinBtns    []widget.Clickable
		actionBtns          []widget.Clickable
		updateBtns          []widget.Clickable
		discoCardBtns       []widget.Clickable
		discoOpenBtns       []widget.Clickable
		discoAuthorBtns     []widget.Clickable
		checkUpdBtn         widget.Clickable
		updateAllBtn        widget.Clickable
		tabExtraBtn         widget.Clickable
		detailOpenBtn       widget.Clickable
		detailPrimaryBtn    widget.Clickable
		detailUpdateBtn     widget.Clickable
		detailAuthorBtn     widget.Clickable
		detailPinBtn        widget.Clickable
		profileList         widget.List
		profileCardBtns     []widget.Clickable
		profileOpenBtns     []widget.Clickable
		profileActionBtns   []widget.Clickable
		profileUpdateBtns   []widget.Clickable
		promptBtns          promptButtons

		createBundleBtn widget.Clickable
		saveBundleBtn   widget.Clickable
		cancelBundleBtn widget.Clickable
		bundleBtns      []bundleRowBtns
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
	profileList.Axis = layout.Vertical
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
			installedNow = installedSet
			updatable = make(map[string]bool)
			for _, n := range st.Installed {
				installedSet[n.ID] = true
				if n.UpdateAvailable != nil {
					updatable[n.ID] = true
				}
			}
			previewSet = make(map[string]bool, len(st.Previews))
			for _, n := range st.Previews {
				previewSet[n.ID] = true
				if n.UpdateAvailable != nil {
					updatable[n.ID] = true
				}
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

			trackPointer(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.UniformInset(unit.Dp(16)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					// a prompt generated by a napp is shown over that napp's own
					// window (it covers the webview until answered), not here.
					// Only launcher-originated prompts take over this window.
					if activePrompt != nil && activePrompt.Instance == "" {
						// allow and deny, each of them for this prompt only, for
						// this session or always
						promptBtns.reset(activePrompt)
						for _, c := range []struct {
							ok    bool
							scope backend.Scope
							btn   *widget.Clickable
						}{
							{true, backend.ScopeOnce, &promptBtns.allow},
							{true, backend.ScopeSession, &promptBtns.sessionAllow},
							{true, backend.ScopeAlways, &promptBtns.alwaysAllow},
							{false, backend.ScopeOnce, &promptBtns.deny},
							{false, backend.ScopeSession, &promptBtns.sessionDeny},
							{false, backend.ScopeAlways, &promptBtns.alwaysDeny},
						} {
							if c.btn.Clicked(gtx) {
								backend.AnswerPrompt(activePrompt.ID, backend.Answer{OK: c.ok, Scope: c.scope, Skip: promptBtns.skipped()})
							}
						}
						return layoutPrompt(gtx, th, activePrompt, &promptBtns)
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
						if googleBtn.Clicked(gtx) {
							go backend.LoginWithGoogle()
						}
						return layoutLogin(gtx, th, &loginEd, &loginBtn, &googleBtn, st.LoginErr, st.LoginStatus)
					case backend.PhaseMain:
						handleOpenBox(gtx)
						if tabHomeBtn.Clicked(gtx) {
							setTab(tabHome)
						}
						if tabWindowsBtn.Clicked(gtx) {
							setTab(tabWindows)
						}
						if tabDiscoBtn.Clicked(gtx) {
							setTab(tabDiscovery)
						}
						if devEnabled && tabDevBtn.Clicked(gtx) {
							setTab(tabDev)
						}
						if extraTabState != nil && tabExtraBtn.Clicked(gtx) {
							setTab(tabExtra)
						}
						if themeBtn.Clicked(gtx) {
							toggleTheme()
						}
						if readAuthBtn.Clicked(gtx) {
							backend.SetReadAuth(!backend.ReadAuth())
						}
						if detailBtns.selfProfile.Clicked(gtx) && st.Pubkey != "" {
							openProfileTab(st.Pubkey)
						}
						if logoutBtn.Clicked(gtx) {
							setConfirmLogout(true)
						}
						if fetchBtn.Clicked(gtx) {
							backend.SetRelays(parseRelays(relaysEd.Text()))
							go backend.Discover()
						}
						if modeDiscoBtn.Clicked(gtx) {
							nappsMode = modeDiscovery
							setTab(tabDiscovery)
						}
						if modeContactsBtn.Clicked(gtx) {
							nappsMode = modeContacts
							setTab(tabDiscovery)
							// what the background sync stored so far
							go backend.RefreshContacts()
						}
						if modeMineBtn.Clicked(gtx) {
							nappsMode = modeMine
							setTab(tabDiscovery)
							go backend.RefreshContacts()
						}
						disco := discoNapps(st)
						for len(cardBtns) < len(st.Installed) {
							cardBtns = append(cardBtns, widget.Clickable{})
						}
						for len(uninstBtns) < len(st.Installed) {
							uninstBtns = append(uninstBtns, widget.Clickable{})
						}
						for len(installedUpdateBtns) < len(st.Installed) {
							installedUpdateBtns = append(installedUpdateBtns, widget.Clickable{})
						}
						for len(installedOpenBtns) < len(st.Installed) {
							installedOpenBtns = append(installedOpenBtns, widget.Clickable{})
						}
						for len(installedAuthorBtns) < len(st.Installed) {
							installedAuthorBtns = append(installedAuthorBtns, widget.Clickable{})
						}
						for len(installedPinBtns) < len(st.Installed) {
							installedPinBtns = append(installedPinBtns, widget.Clickable{})
						}
						for len(actionBtns) < len(disco) {
							actionBtns = append(actionBtns, widget.Clickable{})
						}
						for len(updateBtns) < len(disco) {
							updateBtns = append(updateBtns, widget.Clickable{})
						}
						for len(discoCardBtns) < len(disco) {
							discoCardBtns = append(discoCardBtns, widget.Clickable{})
						}
						for len(discoOpenBtns) < len(disco) {
							discoOpenBtns = append(discoOpenBtns, widget.Clickable{})
						}
						for len(discoAuthorBtns) < len(disco) {
							discoAuthorBtns = append(discoAuthorBtns, widget.Clickable{})
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
						for len(bundleBtns) < len(st.Bundles) {
							bundleBtns = append(bundleBtns, bundleRowBtns{})
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
							if ui.bundleEditing != nil {
								if saveBundleBtn.Clicked(gtx) {
									name := strings.TrimSpace(ui.bundleEditing.nameEd.Text())
									oldName := ui.bundleEditing.name
									draft := bundleDraft(ui.bundleEditing)
									_, err := backend.ParseBundleDraft(draft)
									switch {
									case err != nil:
										setBundleErr(err.Error())
									case name == "":
										setBundleErr("give the bundle a name")
									default:
										// validation passed: the selection is consumed
										// here, on the Gio loop; the bundle itself is
										// saved in the background
										editing := ui.bundleEditing
										ui.bundleEditing = nil
										clearBundleChecks()
										go saveBundle(name, oldName, draft, editing)
									}
								}
								if cancelBundleBtn.Clicked(gtx) {
									ui.bundleEditing = nil
								}
							} else {
								if createBundleBtn.Clicked(gtx) {
									draft := backend.DraftFromWindows(pickedBundleWindows(st))
									if len(draft) > 0 {
										setBundleEdit(newBundleEditState("", draft))
										setBundleErr("")
									}
								}
								for i, b := range st.Bundles {
									name := b.Name
									btns := &bundleBtns[i]
									if btns.pin.Clicked(gtx) {
										if b.Pinned {
											go backend.UnpinBundle(name)
										} else {
											go backend.PinBundle(name)
										}
									}
									if btns.shortcut.Clicked(gtx) {
										if b.Shortcut {
											runInBackground(func() error { return backend.RemoveBundleShortcut(name) })
										} else {
											runInBackground(func() error { return backend.CreateBundleShortcut(name) })
										}
									}
									if btns.del.Clicked(gtx) {
										runInBackground(func() error { return backend.DeleteBundle(name) })
									}
									if btns.edit.Clicked(gtx) {
										setBundleEdit(editBundleEditState(b))
										setBundleErr("")
									}
								}
							}
						} else if tab == tabHome {
							// the installed list below the home grid (the grid
							// takes its own pointer events, see home.go)
							// buttons on top of the card's own click area go
							// first: a click that hit a button must not also
							// count as opening the napp page. The author row
							// goes first of all: it opens a profile, not a napp.
							acted := false
							for _, i := range instVis {
								if installedAuthorBtns[i].Clicked(gtx) {
									openProfileTab(st.Installed[i].Author.Hex())
									acted = true
								}
							}
							if !acted {
								for _, i := range instVis {
									if installedOpenBtns[i].Clicked(gtx) {
										backend.Launch(st.Installed[i])
										acted = true
									}
								}
							}
							if !acted {
								for _, i := range instVis {
									if installedPinBtns[i].Clicked(gtx) {
										id := st.Installed[i].ID
										if backend.IsPinned(id) {
											backend.Unpin(id)
										} else {
											backend.Pin(id)
										}
										acted = true
									}
								}
							}
							if !acted {
								for _, i := range instVis {
									if installedUpdateBtns[i].Clicked(gtx) {
										go backend.Update(st.Installed[i].ID)
										acted = true
									}
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
										openNappTab(st.Installed[i])
									}
								}
							}
							if checkUpdBtn.Clicked(gtx) && !st.UpdateCheckRunning {
								go backend.CheckForUpdates()
							}
							if updateAllBtn.Clicked(gtx) {
								go backend.UpdateAll()
							}
						} else if tab == tabDiscovery {
							acted := false
							for _, i := range vis {
								if discoAuthorBtns[i].Clicked(gtx) {
									openProfileTab(disco[i].Author.Hex())
									acted = true
								}
							}
							if !acted {
								for _, i := range vis {
									if discoOpenBtns[i].Clicked(gtx) {
										openOrPreview(disco[i], installedSet, busy, true)
										acted = true
									}
								}
							}
							if !acted {
								for _, i := range vis {
									if actionBtns[i].Clicked(gtx) {
										n := disco[i]
										if busy[n.ID] {
											continue
										}
										if installedSet[n.ID] {
											go backend.Uninstall(n.ID)
										} else {
											go backend.Install(n)
										}
										acted = true
									}
									if updateBtns[i].Clicked(gtx) {
										cardSecondClicked(disco[i], installedSet, busy)
										acted = true
									}
								}
							}
							if !acted {
								for _, i := range vis {
									if discoCardBtns[i].Clicked(gtx) {
										openNappTab(disco[i])
									}
								}
							}
						} else if tab == tabExtra && extraTabState != nil {
							if extraTabState.kind == "profile" {
								if cp := cachedProfile(extraTabState.pubkey); cp != nil && cp.ShortName != "" {
									extraTabState.title = truncate(cp.ShortName, 18)
								}
							}
							if extraTabState.kind == "napp" {
								n := detailNapp(extraTabState)
								if detailAuthorBtn.Clicked(gtx) && n.Author.Hex() != "" {
									openProfileTab(n.Author.Hex())
								} else if detailBtns.nappEvent.Clicked(gtx) {
									backend.ViewNappEvent(n)
								} else if detailBtns.nappRemove.Clicked(gtx) && previewSet[n.ID] && !installedSet[n.ID] && !busy[n.ID] {
									go backend.Uninstall(n.ID)
								} else if detailOpenBtn.Clicked(gtx) {
									openOrPreview(n, installedSet, busy, false)
								} else if detailPrimaryBtn.Clicked(gtx) {
									if busy[n.ID] {
									} else if installedSet[n.ID] {
										go backend.Uninstall(n.ID)
									} else if dn, ok := backend.DiscoveredNapp(n.ID); ok {
										go backend.Install(dn)
									} else {
										go backend.Install(n)
									}
								} else if detailUpdateBtn.Clicked(gtx) {
									go backend.Update(n.ID)
								} else if detailPinBtn.Clicked(gtx) && (installedSet[n.ID] || previewSet[n.ID]) {
									if backend.IsPinned(n.ID) {
										backend.Unpin(n.ID)
									} else {
										backend.Pin(n.ID)
									}
								}
							} else {
								if detailBtns.profileView.Clicked(gtx) {
									backend.ViewProfile(extraTabState.pubkey)
								}
								// profile tab: size buttons to its napps list
								pan, _, _ := cachedAuthorNapps(extraTabState.pubkey)
								for len(profileCardBtns) < len(pan) {
									profileCardBtns = append(profileCardBtns, widget.Clickable{})
									profileOpenBtns = append(profileOpenBtns, widget.Clickable{})
									profileActionBtns = append(profileActionBtns, widget.Clickable{})
									profileUpdateBtns = append(profileUpdateBtns, widget.Clickable{})
								}
								pacted := false
								for i, pn := range pan {
									if i >= len(profileOpenBtns) {
										break
									}
									if profileOpenBtns[i].Clicked(gtx) {
										openOrPreview(pn, installedSet, busy, true)
										pacted = true
									}
								}
								if !pacted {
									for i, pn := range pan {
										if i >= len(profileActionBtns) {
											break
										}
										if profileActionBtns[i].Clicked(gtx) {
											if busy[pn.ID] {
												continue
											}
											if installedSet[pn.ID] {
												go backend.Uninstall(pn.ID)
											} else {
												go backend.Install(pn)
											}
											pacted = true
										}
										if i < len(profileUpdateBtns) && profileUpdateBtns[i].Clicked(gtx) {
											cardSecondClicked(pn, installedSet, busy)
											pacted = true
										}
									}
								}
								if !pacted {
									for i, pn := range pan {
										if i >= len(profileCardBtns) {
											break
										}
										if profileCardBtns[i].Clicked(gtx) {
											openNappTab(pn)
										}
									}
								}
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
							&tabHomeBtn,
							&tabWindowsBtn,
							&tabDiscoBtn,
							devBtn,
							&themeBtn,
							&readAuthBtn,
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
							&updateAllBtn,
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

							&createBundleBtn,
							&saveBundleBtn,
							&cancelBundleBtn,
							bundleBtns,

							vis,
							instVis,
							st,
							installedSet,
							busy,

							extraTabState,
							&tabExtraBtn,
							installedOpenBtns,
							installedAuthorBtns,
							discoCardBtns,
							discoOpenBtns,
							discoAuthorBtns,
							&detailOpenBtn,
							&detailPrimaryBtn,
							&detailUpdateBtn,
							&detailAuthorBtn,
							&detailPinBtn,
							&profileList,
							profileCardBtns,
							profileOpenBtns,
							profileActionBtns,
							profileUpdateBtns,
							installedPinBtns,
						)
					default:
						return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return material.Body1(th, "Loading\u2026").Layout(gtx)
						})
					}
				})
			})
			drawTooltip(gtx, th)

			e.Frame(gtx.Ops)

		case app.DestroyEvent:
			return
		}
	}
}
