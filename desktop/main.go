package main

import (
	"io"
	"os"
	"path/filepath"
	"slices"
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

// This is the desktop launcher: a resident backend and tray, plus a Gio
// manager window that may be closed and recreated. Everything the manager
// shows comes from backend.Snapshot(), everything it does is a backend call,
// and every napp window is a child process (see childproc.go).

// gioState is what belongs to this window alone — the backend owns the rest.
type gioState struct {
	mu  sync.Mutex
	tab int

	// discoveryArchetype is set by an intent dispatch off the UI thread and
	// consumed by the next frame.
	discoveryArchetype string

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
	ui  gioState
	log zerolog.Logger

	// filterEd and installedFilterEd are the discovery and installed tabs'
	// filter boxes (one window, so one of each is enough).
	filterEd          widget.Editor
	installedFilterEd widget.Editor

	// discoKind is which apps the discovery tab lists (one of the
	// discoKind* constants), switched by discoKindBtns.
	discoKind     int
	discoKindBtns [3]widget.Clickable
	// discoCols and installedCols are how many napps a row of the
	// discovery and installed tabs held last frame (see nappGrid).
	discoCols, installedCols int
)

const (
	discoKindAll = iota
	discoKindNapps
	discoKindNapplets
)

// discoKindLabels name the discovery tab's kind buttons, in discoKind order.
var discoKindLabels = [3]string{"All", "Napps", "Napplets"}

// matchesKind says whether a napp belongs under a discovery kind tab.
func matchesKind(n backend.Napp, kind int) bool {
	switch kind {
	case discoKindNapps:
		return !n.IsNapplet()
	case discoKindNapplets:
		return n.IsNapplet()
	}
	return true
}

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
	background, startupToken := startupArgs(os.Args[1:])

	verdanaDir := filepath.Join(dataDir, "Verdana")

	forwarded := instanceCommand{Command: commandOpenManager}
	if startupToken != "" {
		forwarded.Command = commandRunShortcut
		forwarded.Token = startupToken
	} else if background {
		forwarded.Command = commandEnsureRunning
	}
	if forwardToInstance(verdanaDir, forwarded) {
		log.Info().Str("command", forwarded.Command).Msg("forwarded invocation to the running launcher")
		return
	}

	// the listener goes up before the backend, so a shortcut clicked while
	// this launcher is still starting finds someone to forward to instead of
	// starting a second one; the tokens it accepts meanwhile wait for the
	// backend to be up (see runBundleToken).
	stopInstanceListener := startInstanceListener(verdanaDir)
	defer stopInstanceListener()

	closeStores, err := backend.Start(backend.Options{
		DataDir: verdanaDir,
		Host:    gioHost{},
		Log:     &log,
	})
	if err != nil {
		log.Fatal().Err(err).Msg("could not start the backend")
	}
	defer closeStores()

	// the backend is up: the token this launcher was started with opens its
	// napps, and the ones forwarded in while it was starting stop waiting.
	close(launcherReady)
	if startupToken != "" {
		log.Info().Str("token", previewToken(startupToken)).Msg("bundle invocation at startup")
		go runBundleToken(startupToken)
	}

	// Resolve the user's system/light/dark preference and keep system mode in
	// sync with OS appearance changes for the lifetime of the launcher.
	stopTheme := startThemeController()
	defer stopTheme()

	// startup tab: installed if any napps, else discovery
	if len(backend.Snapshot().Installed) > 0 {
		ui.tab = 1
	} else {
		ui.tab = 2
	}

	runDesktop(background)

	backend.CloseAllWindows()
	backend.CloseAllSettings()
	killAllChildren()
}

func startupArgs(args []string) (background bool, token string) {
	var tokenArgs []string
	for _, arg := range args {
		if arg == "--background" {
			background = true
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		tokenArgs = append(tokenArgs, arg)
	}
	return background, strings.Join(tokenArgs, " ")
}

func setTab(t int) {
	ui.tab = t
	if t != tabExtra {
		clearExtraTab()
	}
	if w := managerWindow(); w != nil {
		w.Invalidate()
	}
}

func setConfirmLogout(v bool) {
	ui.confirmLogout = v
	if w := managerWindow(); w != nil {
		w.Invalidate()
	}
}

func setShortcutErr(msg string) {
	ui.mu.Lock()
	ui.shortcutErr = msg
	ui.mu.Unlock()
	if w := managerWindow(); w != nil {
		w.Invalidate()
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
	if w := managerWindow(); w != nil {
		w.Invalidate()
	}
}

// discoveryFilter returns the indices of st.Discovery that pass the filter
// editor's text: a case-insensitive substring on name, description, author
// pubkey and author name. Clicks and rendering both walk this same index
// list, so buttons stay glued to their napp no matter what the filter hides.
//
// A napp address (an naddr, a nostr: link) typed there is looked up on
// relays too; once found, it is listed and it alone passes the filter.
//
// The kind tabs narrow it further to napps or napplets, except for an
// address: that names one app, whatever its kind.
func discoveryFilter(st backend.State, archetype string) []int {
	q := strings.ToLower(strings.TrimSpace(filterEd.Text()))
	if role, ok := strings.CutPrefix(q, "archetype:"); ok {
		archetype = strings.TrimSpace(role)
		q = ""
		backend.LookupAddress("")
	} else {
		backend.LookupAddress(filterEd.Text())
	}
	vis := nappFilter(st.Discovery, q)
	if archetype != "" {
		vis = slices.DeleteFunc(vis, func(i int) bool {
			return !st.Discovery[i].HandlesArchetype(archetype)
		})
	}
	if discoKind == discoKindAll || backend.IsNappAddress(q) {
		return vis
	}
	out := vis[:0]
	for _, i := range vis {
		if matchesKind(st.Discovery[i], discoKind) {
			out = append(out, i)
		}
	}
	return out
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
	setManagerWindow(w)
	defer setManagerWindow(nil)

	var (
		fetchBtn              widget.Clickable
		tabNappsBtn           widget.Clickable
		tabDiscoBtn           widget.Clickable
		tabDevBtn             widget.Clickable
		tabWindowsBtn         widget.Clickable
		themeBtn              widget.Clickable
		settingsBtn           widget.Clickable
		logoutBtn             widget.Clickable
		confirmYesBtn         widget.Clickable
		confirmNoBtn          widget.Clickable
		installedList         widget.List
		discoveryList         widget.List
		devList               widget.List
		windowsList           widget.List
		devURLed              widget.Editor
		devPathEd             widget.Editor
		loadURLBtn            widget.Clickable
		browseBtn             widget.Clickable
		loadFolderBtn         widget.Clickable
		devOpenBtns           []widget.Clickable
		devUnloadBtns         []widget.Clickable
		devPublishBtns        []widget.Clickable
		closeBtns             []widget.Clickable
		reopenBtns            []widget.Clickable
		cardBtns              []widget.Clickable
		uninstBtns            []widget.Clickable
		installedUpdateBtns   []widget.Clickable
		installedOpenBtns     []widget.Clickable
		installedAuthorBtns   []widget.Clickable
		installedSettingsBtns []widget.Clickable
		actionBtns            []widget.Clickable
		updateBtns            []widget.Clickable
		discoCardBtns         []widget.Clickable
		discoOpenBtns         []widget.Clickable
		discoAuthorBtns       []widget.Clickable
		checkUpdBtn           widget.Clickable
		tabExtraBtn           widget.Clickable
		detailOpenBtn         widget.Clickable
		detailPrimaryBtn      widget.Clickable
		detailUpdateBtn       widget.Clickable
		detailAuthorBtn       widget.Clickable
		detailCopyAddrBtn     widget.Clickable
		detailSettingsBtn     widget.Clickable
		profileList           widget.List
		profileCardBtns       []widget.Clickable
		profileOpenBtns       []widget.Clickable
		profileActionBtns     []widget.Clickable
		profileUpdateBtns     []widget.Clickable
		promptBtns            promptButtons
		optBtns               []widget.Clickable

		bundleNameEd      widget.Editor
		createShortcutBtn widget.Clickable
		saveShortcutBtn   widget.Clickable
		cancelShortcutBtn widget.Clickable
		shortcutDelBtns   []widget.Clickable
		shortcutEditBtns  []widget.Clickable
	)
	loginScr := newLoginScreen()
	filterEd.SingleLine = true
	installedFilterEd.SingleLine = true
	devURLed.SingleLine = true
	devPathEd.SingleLine = true
	installedList.Axis = layout.Vertical
	discoveryList.Axis = layout.Vertical
	devList.Axis = layout.Vertical
	windowsList.Axis = layout.Vertical
	profileList.Axis = layout.Vertical

	var ops op.Ops
	for {
		switch e := w.Event(); e := e.(type) {
		case app.FrameEvent:
			gtx := app.NewContext(&ops, e)
			if backend.ThemeMode() != appliedThemeMode() {
				applyThemeMode()
			}

			// the palette is re-read every frame, so a theme switch (which can
			// come from any goroutine) never touches th concurrently
			pal := currentTheme()
			pal.apply(th)
			paint.Fill(gtx.Ops, pal.bg)

			st := backend.Snapshot()
			activePrompt := backend.CurrentPrompt()

			ui.mu.Lock()
			tab := ui.tab
			discoveryArchetype := ui.discoveryArchetype
			ui.discoveryArchetype = ""
			pendingCopies := ui.clipboard
			ui.clipboard = nil
			ui.mu.Unlock()
			if discoveryArchetype != "" {
				filterEd.SetText("archetype:" + discoveryArchetype)
				discoKind = discoKindNapplets
			}

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
					// allow and deny, each of them for this prompt only, for
					// this session or always
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
							backend.AnswerPrompt(activePrompt.ID, backend.Answer{OK: c.ok, Scope: c.scope})
						}
					}
					for i := range activePrompt.Options {
						if optBtns[i].Clicked(gtx) {
							backend.AnswerPrompt(activePrompt.ID, backend.Answer{OK: true, Index: i, Scope: backend.ScopeOnce})
						}
					}
					return layoutPrompt(gtx, th, activePrompt, &promptBtns, optBtns)
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
					return loginScr.layout(gtx, th, st)
				case backend.PhaseMain:
					if tabWindowsBtn.Clicked(gtx) {
						setTab(tabWindows)
					}
					if tabNappsBtn.Clicked(gtx) {
						setTab(tabInstalled)
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
					if settingsBtn.Clicked(gtx) {
						go func() {
							if err := backend.OpenLauncherSettings(); err != nil {
								log.Warn().Err(err).Msg("could not open settings")
							}
						}()
					}
					if logoutBtn.Clicked(gtx) {
						setConfirmLogout(true)
					}
					if fetchBtn.Clicked(gtx) {
						go backend.Discover()
					}
					for k := range discoKindBtns {
						if discoKindBtns[k].Clicked(gtx) {
							discoKind = k
						}
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
					for len(installedOpenBtns) < len(st.Installed) {
						installedOpenBtns = append(installedOpenBtns, widget.Clickable{})
					}
					for len(installedAuthorBtns) < len(st.Installed) {
						installedAuthorBtns = append(installedAuthorBtns, widget.Clickable{})
					}
					for len(installedSettingsBtns) < len(st.Installed) {
						installedSettingsBtns = append(installedSettingsBtns, widget.Clickable{})
					}
					for len(actionBtns) < len(st.Discovery) {
						actionBtns = append(actionBtns, widget.Clickable{})
					}
					for len(updateBtns) < len(st.Discovery) {
						updateBtns = append(updateBtns, widget.Clickable{})
					}
					for len(discoCardBtns) < len(st.Discovery) {
						discoCardBtns = append(discoCardBtns, widget.Clickable{})
					}
					for len(discoOpenBtns) < len(st.Discovery) {
						discoOpenBtns = append(discoOpenBtns, widget.Clickable{})
					}
					for len(discoAuthorBtns) < len(st.Discovery) {
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
					vis := discoveryFilter(st, discoveryArchetype)
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
					} else if tab == tabInstalled {
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
								if st.Installed[i].IsNapplet() && installedSettingsBtns[i].Clicked(gtx) {
									if err := backend.OpenSettings(st.Installed[i].ID); err != nil {
										log.Warn().Err(err).Str("napp", st.Installed[i].ID).Msg("could not open settings")
									}
									acted = true
								}
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
					} else if tab == tabDiscovery {
						acted := false
						for _, i := range vis {
							if discoAuthorBtns[i].Clicked(gtx) {
								openProfileTab(st.Discovery[i].Author.Hex())
								acted = true
							}
						}
						if !acted {
							for _, i := range vis {
								if (installedSet[st.Discovery[i].ID] || st.Discovery[i].IsNapplet()) && discoOpenBtns[i].Clicked(gtx) {
									if n, ok := backend.InstalledNapp(st.Discovery[i].ID); ok {
										backend.Launch(n)
									} else {
										backend.TryNapplet(st.Discovery[i])
									}
									acted = true
								}
							}
						}
						if !acted {
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
									acted = true
								}
								if updateBtns[i].Clicked(gtx) {
									go backend.Install(st.Discovery[i])
									acted = true
								}
							}
						}
						if !acted {
							for _, i := range vis {
								if discoCardBtns[i].Clicked(gtx) {
									openNappTab(st.Discovery[i])
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
							} else if detailOpenBtn.Clicked(gtx) && (installedSet[n.ID] || n.IsNapplet()) {
								if in, ok := backend.InstalledNapp(n.ID); ok {
									backend.Launch(in)
								} else {
									backend.TryNapplet(n)
								}
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
							} else if detailCopyAddrBtn.Clicked(gtx) && n.Naddr() != "" {
								gioHost{}.CopyText(n.Naddr())
							} else if detailSettingsBtn.Clicked(gtx) && installedSet[n.ID] {
								id := n.ID
								go func() {
									if err := backend.OpenSettings(id); err != nil {
										log.Warn().Err(err).Str("napp", id).Msg("could not open settings")
									}
								}()
							}
						} else {
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
								if (installedSet[pn.ID] || pn.IsNapplet()) && profileOpenBtns[i].Clicked(gtx) {
									if in, ok := backend.InstalledNapp(pn.ID); ok {
										backend.Launch(in)
									} else {
										backend.TryNapplet(pn)
									}
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
										go backend.Install(pn)
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
						&tabWindowsBtn,
						&tabNappsBtn,
						&tabDiscoBtn,
						devBtn,
						&themeBtn,
						&settingsBtn,
						&logoutBtn,
						tab,

						&windowsList,
						&installedList,
						&discoveryList,
						&devList,
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

						extraTabState,
						&tabExtraBtn,
						installedOpenBtns,
						installedAuthorBtns,
						installedSettingsBtns,
						discoCardBtns,
						discoOpenBtns,
						discoAuthorBtns,
						&detailOpenBtn,
						&detailPrimaryBtn,
						&detailUpdateBtn,
						&detailAuthorBtn,
						&detailCopyAddrBtn,
						&detailSettingsBtn,
						&profileList,
						profileCardBtns,
						profileOpenBtns,
						profileActionBtns,
						profileUpdateBtns,
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
