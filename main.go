package main

import (
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore/lmdb"
	"fiatjaf.com/nostr/keyer"
	"fiatjaf.com/nostr/sdk"
	bolt_kv "fiatjaf.com/nostr/sdk/kvstore/bbolt"
	"gioui.org/app"
	"gioui.org/f32"
	"gioui.org/font"
	"gioui.org/font/gofont"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
	"github.com/abemedia/go-webview"
	_ "github.com/abemedia/go-webview/embedded"
)

type Napp struct {
	ID          string
	Name        string
	Description string
	Icon        string
	Author      string
	Actions     []string
}

var sys *sdk.System

// ---- persistent app state ----

var defaultRelays = []string{
	"relay.nostrapps.com",
	"relay.nostrapps.com/public",
}

// AppState is the persisted launcher configuration. It is stored as JSON in
// state.json inside the Verdana data dir. nostr.SecretKey marshals to/from hex
// automatically.
type AppState struct {
	ClientKey nostr.SecretKey `json:"client_key"`
	Login     string          `json:"login"` // nsec or bunker:// URL
	Relays    []string        `json:"relays"`
}

var (
	state     AppState
	statePath string
)

func loadState() {
	statePath = filepath.Join(verdanaDir, "state.json")
	data, err := os.ReadFile(statePath)
	if err == nil {
		json.Unmarshal(data, &state)
	}
	if state.ClientKey == (nostr.SecretKey{}) {
		state.ClientKey = nostr.Generate()
	}
	if len(state.Relays) == 0 {
		state.Relays = append([]string(nil), defaultRelays...)
	}
	saveState()
}

func saveState() {
	data, err := json.MarshalIndent(&state, "", "  ")
	if err != nil {
		return
	}
	os.WriteFile(statePath, data, 0600)
}

// ---- live UI state (read in the frame handler, published from goroutines) ----

type uiState struct {
	mu       sync.Mutex
	phase    string // "loading" | "login" | "main"
	loginErr string
	profName string
	profPic  string
	fetchErr string
	fetching bool
	napps    []Napp // published by replacement, never mutated in place
}

var ui = uiState{phase: "loading"}

var (
	userKeyer  nostr.Keyer
	userPubkey nostr.PubKey
)

type openReq struct {
	napp Napp
	dir  string
}

var openReqCh = make(chan openReq, 16)

// wireMsg is the newline/whitespace-delimited JSON protocol exchanged between
// the parent launcher process and each child napp-window process.
//
//	child -> parent : {"t":"rpc",  "id":N, "method":..., "params":...}
//	parent -> child : {"t":"resp", "id":N, "result":<json>, "error":...}
//	parent -> child : {"t":"eval", "code":"..."}
type wireMsg struct {
	T      string          `json:"t"`
	ID     int             `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params string          `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
	Code   string          `json:"code,omitempty"`
}

// childInfo is the parent's handle to one running child napp-window process.
type childInfo struct {
	cmd   *exec.Cmd     // the child process, so it can be killed on parent exit
	enc   *json.Encoder // writes to the child's stdin
	encMu sync.Mutex
	subs  map[int]context.CancelFunc
	subMu sync.Mutex
}

var (
	mu         sync.Mutex
	children   []*childInfo
	verdanaDir string // parent: root Verdana data dir
	nappDir    string // child: this napp's data dir (from VERDANA_NAPP_DIR)
)

func initSystem(dataDir string) func() {
	db := &lmdb.LMDBBackend{
		Path: filepath.Join(dataDir, "eventstore"),
	}
	if err := db.Init(); err != nil {
		panic("failed to init eventstore: " + err.Error())
	}

	kv, err := bolt_kv.NewStore(filepath.Join(dataDir, "kvstore"))
	if err != nil {
		panic("failed to init kvstore: " + err.Error())
	}

	sys = sdk.NewSystem()
	sys.KVStore = kv
	sys.Store = db

	sys.Pool.QueryMiddleware = sys.TrackQueryAttempts
	sys.Pool.EventMiddleware = sys.TrackEventHintsAndRelays
	sys.Pool.DuplicateMiddleware = sys.TrackEventRelaysD

	return db.Close
}

func main() {
	// Child mode: this process hosts exactly one napp webview window. Closing
	// that window (which terminates the shared GTK loop) only exits this child,
	// leaving the parent launcher and any other napp windows untouched.
	if nappID := os.Getenv("VERDANA_NAPP_ID"); nappID != "" {
		childMain(nappID)
		return
	}

	dataDir, err := app.DataDir()
	if err != nil {
		panic("no data dir: " + err.Error())
	}
	verdanaDir = filepath.Join(dataDir, "Verdana")
	os.MkdirAll(verdanaDir, 0755)
	closer := initSystem(verdanaDir)
	defer closer()

	loadState()

	go gioMain()
	go webviewServer()
	app.Main()

	// app.Main returns once the Gio launcher window is destroyed; make sure no
	// napp child windows are left orphaned.
	killAllChildren()
}

// killAllChildren terminates every running napp-window child process. Called
// when the parent launcher exits so no windows are left orphaned.
func killAllChildren() {
	mu.Lock()
	snapshot := append([]*childInfo(nil), children...)
	mu.Unlock()
	for _, ci := range snapshot {
		if ci.cmd != nil && ci.cmd.Process != nil {
			ci.cmd.Process.Kill()
		}
	}
}

// gioWin is the launcher window; goroutines call gioWin.Invalidate() after
// publishing new UI state.
var gioWin *app.Window

func gioMain() {
	th := material.NewTheme()
	th.Shaper = text.NewShaper(text.WithCollection(gofont.Collection()))

	w := new(app.Window)
	w.Option(app.Title("Verdana"), app.Size(unit.Dp(560), unit.Dp(640)))
	gioWin = w

	// Widgets are only ever touched from this (UI) goroutine.
	var (
		loginEd  widget.Editor
		loginBtn widget.Clickable
		relaysEd widget.Editor
		fetchBtn widget.Clickable
		mainList widget.List
		runBtns  []widget.Clickable
	)
	loginEd.SingleLine = true
	relaysEd.SingleLine = false
	mainList.Axis = layout.Vertical
	relaysEd.SetText(strings.Join(state.Relays, "\n"))

	// Kick off auto-login if we already have credentials stored.
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
			loginErr := ui.loginErr
			profName := ui.profName
			profPic := ui.profPic
			fetchErr := ui.fetchErr
			fetching := ui.fetching
			nappsSnap := ui.napps
			ui.mu.Unlock()

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
					if fetchBtn.Clicked(gtx) {
						state.Relays = parseRelays(relaysEd.Text())
						saveState()
						go doFetch(state.Relays)
					}
					for len(runBtns) < len(nappsSnap) {
						runBtns = append(runBtns, widget.Clickable{})
					}
					for i := range nappsSnap {
						if runBtns[i].Clicked(gtx) {
							launchNapp(nappsSnap[i])
						}
					}
					return layoutMain(gtx, th, &mainList, &relaysEd, &fetchBtn, runBtns,
						profName, profPic, fetchErr, fetching, nappsSnap)
				default: // loading
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

func setPhase(p string) {
	ui.mu.Lock()
	ui.phase = p
	ui.mu.Unlock()
	if gioWin != nil {
		gioWin.Invalidate()
	}
}

// parseRelays splits the relay textarea into one address per line, trimming
// whitespace, dropping empties, and prepending wss:// when no scheme is given.
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

func launchNapp(napp Napp) {
	id := napp.ID
	if id == "" {
		id = "unknown"
	}
	appDir := filepath.Join(verdanaDir, "napps", id)
	os.MkdirAll(appDir, 0755)
	select {
	case openReqCh <- openReq{napp: napp, dir: appDir}:
	default:
	}
}

// ---- login & data goroutines ----

// doLogin builds a Keyer from an nsec or bunker:// input, fetches our own
// profile metadata, persists the login, and moves the UI to the main phase.
func doLogin(input string) {
	setPhase("loading")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	k, err := keyer.New(ctx, sys.Pool, input, &keyer.SignerOptions{
		BunkerClientSecretKey: state.ClientKey,
		BunkerAuthHandler:     func(url string) { /* TODO: surface bunker auth url */ },
	})
	if err != nil {
		ui.mu.Lock()
		ui.loginErr = err.Error()
		ui.phase = "login"
		ui.mu.Unlock()
		gioWin.Invalidate()
		return
	}

	pk, err := k.GetPublicKey(ctx)
	if err != nil {
		ui.mu.Lock()
		ui.loginErr = err.Error()
		ui.phase = "login"
		ui.mu.Unlock()
		gioWin.Invalidate()
		return
	}

	userKeyer = k
	userPubkey = pk

	if state.Login != input {
		state.Login = input
		saveState()
	}

	pm := sys.FetchProfileMetadata(ctx, pk)
	name := pm.Name
	if name == "" {
		name = pm.DisplayName
	}
	if name == "" {
		name = pk.Hex()
	}

	ui.mu.Lock()
	ui.loginErr = ""
	ui.profName = name
	ui.profPic = pm.Picture
	ui.phase = "main"
	ui.mu.Unlock()
	gioWin.Invalidate()

	// Auto-fetch napps from the stored relays right away.
	go doFetch(state.Relays)
}

// doFetch queries the given relays for kind 35128 napp-definition events and
// publishes them (deduped by event id) to the UI as they arrive.
func doFetch(relays []string) {
	urls := relays
	if len(urls) == 0 {
		urls = parseRelays(strings.Join(defaultRelays, "\n"))
	}

	ui.mu.Lock()
	ui.fetching = true
	ui.fetchErr = ""
	ui.napps = nil
	ui.mu.Unlock()
	gioWin.Invalidate()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	seen := make(map[nostr.ID]bool)
	var collected []Napp

	ch := sys.Pool.FetchMany(ctx, urls,
		nostr.Filter{Kinds: []nostr.Kind{35128}},
		nostr.SubscriptionOptions{},
	)
	for re := range ch {
		if seen[re.ID] {
			continue
		}
		seen[re.ID] = true
		collected = append(collected, nappFromEvent(re.Event))

		ui.mu.Lock()
		ui.napps = append([]Napp(nil), collected...)
		ui.mu.Unlock()
		gioWin.Invalidate()
	}

	ui.mu.Lock()
	ui.fetching = false
	ui.mu.Unlock()
	gioWin.Invalidate()
}

func tagValue(tags nostr.Tags, key string) string {
	if t := tags.Find(key); len(t) > 1 {
		return t[1]
	}
	return ""
}

func nappFromEvent(evt nostr.Event) Napp {
	n := Napp{
		ID:          evt.Tags.GetD(),
		Name:        tagValue(evt.Tags, "title"),
		Description: tagValue(evt.Tags, "description"),
		Icon:        tagValue(evt.Tags, "icon"),
		Author:      evt.PubKey.Hex(),
	}
	if n.Name == "" {
		n.Name = n.ID
	}
	for t := range evt.Tags.FindAll("action") {
		if len(t) > 1 {
			n.Actions = append(n.Actions, t[1])
		}
	}
	return n
}

// ---- async image loader ----

type imgEntry struct {
	once   sync.Once
	op     paint.ImageOp
	ready  atomic.Bool
	failed atomic.Bool
}

var imgCache sync.Map // url -> *imgEntry

// getImage returns the decoded image op for a URL, kicking off a background
// fetch on first request and invalidating the window once it is ready.
func getImage(url string) (paint.ImageOp, bool) {
	if url == "" {
		return paint.ImageOp{}, false
	}
	v, _ := imgCache.LoadOrStore(url, &imgEntry{})
	e := v.(*imgEntry)
	e.once.Do(func() {
		go func() {
			client := http.Client{Timeout: 15 * time.Second}
			resp, err := client.Get(url)
			if err != nil {
				e.failed.Store(true)
				return
			}
			defer resp.Body.Close()
			img, _, err := image.Decode(resp.Body)
			if err != nil {
				e.failed.Store(true)
				return
			}
			e.op = paint.NewImageOp(img)
			e.ready.Store(true)
			if gioWin != nil {
				gioWin.Invalidate()
			}
		}()
	})
	if e.ready.Load() {
		return e.op, true
	}
	return paint.ImageOp{}, false
}

// ---- layout helpers ----

func layoutLogin(gtx layout.Context, th *material.Theme, ed *widget.Editor, btn *widget.Clickable, loginErr string) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			t := material.H5(th, "Log in to Verdana")
			t.Font.Weight = font.Bold
			return t.Layout(gtx)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			l := material.Body2(th, "Paste your nsec or a bunker:// URL")
			l.Color = color.NRGBA{R: 0x66, G: 0x66, B: 0x66, A: 0xff}
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
				l.Color = color.NRGBA{R: 0xcc, G: 0x22, B: 0x22, A: 0xff}
				return l.Layout(gtx)
			})
		}),
	)
}

func layoutMain(gtx layout.Context, th *material.Theme, list *widget.List, relaysEd *widget.Editor, fetchBtn *widget.Clickable, runBtns []widget.Clickable, profName, profPic, fetchErr string, fetching bool, napps []Napp) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layoutProfile(gtx, th, profName, profPic)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(16)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			l := material.Body2(th, "Relays (one per line)")
			l.Color = color.NRGBA{R: 0x66, G: 0x66, B: 0x66, A: 0xff}
			return l.Layout(gtx)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return editorBox(gtx, th, relaysEd, "relay.example.com")
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					pointer.CursorPointer.Add(gtx.Ops)
					label := "Fetch napps"
					if fetching {
						label = "Fetching\u2026"
					}
					return material.Button(th, fetchBtn, label).Layout(gtx)
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if fetchErr == "" {
						return layout.Dimensions{}
					}
					l := material.Body2(th, fetchErr)
					l.Color = color.NRGBA{R: 0xcc, G: 0x22, B: 0x22, A: 0xff}
					return l.Layout(gtx)
				}),
			)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(12)}.Layout),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			if len(napps) == 0 {
				msg := "No napps yet. Click \"Fetch napps\"."
				if fetching {
					msg = "Searching relays\u2026"
				}
				l := material.Body2(th, msg)
				l.Color = color.NRGBA{R: 0x99, G: 0x99, B: 0x99, A: 0xff}
				return l.Layout(gtx)
			}
			return material.List(th, list).Layout(gtx, len(napps), func(gtx layout.Context, i int) layout.Dimensions {
				var btn *widget.Clickable
				if i < len(runBtns) {
					btn = &runBtns[i]
				}
				return renderNappCard(gtx, th, btn, napps[i])
			})
		}),
	)
}

func layoutProfile(gtx layout.Context, th *material.Theme, name, pic string) layout.Dimensions {
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
	)
}

// avatar draws a square clipped image for the given URL, or a grey placeholder
// while it loads / on failure. The image is scaled to cover the square box.
func avatar(gtx layout.Context, url string, size int) layout.Dimensions {
	px := gtx.Dp(unit.Dp(size))
	sq := image.Point{X: px, Y: px}
	defer clip.RRect{Rect: image.Rectangle{Max: sq}, NW: 6, NE: 6, SW: 6, SE: 6}.Push(gtx.Ops).Pop()
	if imgOp, ok := getImage(url); ok {
		isz := imgOp.Size()
		if isz.X > 0 && isz.Y > 0 {
			// scale to cover the square box
			scale := float32(px) / float32(isz.X)
			if s := float32(px) / float32(isz.Y); s > scale {
				scale = s
			}
			defer op.Affine(f32.Affine2D{}.Scale(f32.Pt(0, 0), f32.Pt(scale, scale))).Push(gtx.Ops).Pop()
		}
		imgOp.Add(gtx.Ops)
		paint.PaintOp{}.Add(gtx.Ops)
	} else {
		paint.Fill(gtx.Ops, color.NRGBA{R: 0xdd, G: 0xdd, B: 0xdd, A: 0xff})
	}
	return layout.Dimensions{Size: sq}
}

func editorBox(gtx layout.Context, th *material.Theme, ed *widget.Editor, hint string) layout.Dimensions {
	border := widget.Border{
		Color:        color.NRGBA{R: 0xcc, G: 0xcc, B: 0xcc, A: 0xff},
		CornerRadius: unit.Dp(6),
		Width:        unit.Dp(1),
	}
	return border.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.UniformInset(unit.Dp(8)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return material.Editor(th, ed, hint).Layout(gtx)
		})
	})
}

func renderNappCard(gtx layout.Context, th *material.Theme, btn *widget.Clickable, napp Napp) layout.Dimensions {
	return layout.Inset{Bottom: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		sz := gtx.Constraints.Max
		macro := op.Record(gtx.Ops)
		dims := layout.Inset{
			Top: unit.Dp(12), Bottom: unit.Dp(12),
			Left: unit.Dp(12), Right: unit.Dp(12),
		}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if napp.Icon == "" {
						return layout.Dimensions{}
					}
					return layout.Inset{Right: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return avatar(gtx, napp.Icon, 40)
					})
				}),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							label := material.Body1(th, napp.Name)
							label.Font.Weight = font.Bold
							return label.Layout(gtx)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							if napp.Description == "" {
								return layout.Dimensions{}
							}
							return layout.Inset{Top: unit.Dp(2)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								l := material.Body2(th, napp.Description)
								l.Color = color.NRGBA{R: 0x66, G: 0x66, B: 0x66, A: 0xff}
								return l.Layout(gtx)
							})
						}),
					)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if btn == nil {
						return layout.Dimensions{}
					}
					return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						pointer.CursorPointer.Add(gtx.Ops)
						b := material.Button(th, btn, "Run")
						b.TextSize = unit.Sp(13)
						b.Inset = layout.UniformInset(unit.Dp(8))
						return b.Layout(gtx)
					})
				}),
			)
		})
		call := macro.Stop()

		// background behind the recorded content
		bg := clip.RRect{
			Rect: image.Rectangle{Max: image.Point{X: sz.X, Y: dims.Size.Y}},
			NW:   8, NE: 8, SW: 8, SE: 8,
		}
		defer bg.Push(gtx.Ops).Pop()
		paint.Fill(gtx.Ops, color.NRGBA{R: 0xf2, G: 0xf2, B: 0xf2, A: 0xff})
		call.Add(gtx.Ops)
		return dims
	})
}

// webviewServer (parent process) launches one independent child process per
// open request. It never touches a webview directly, so it does not need to
// lock an OS thread.
func webviewServer() {
	for req := range openReqCh {
		go launchChild(req)
	}
}

// launchChild spawns a child process that hosts a single napp window, wires up
// its stdin/stdout pipes, and pumps RPC requests through the existing bridge
// handlers until the child's window is closed (stdout EOF).
func launchChild(req openReq) {
	exe, err := os.Executable()
	if err != nil {
		exe = os.Args[0]
	}
	cmd := exec.Command(exe)
	cmd.Env = append(os.Environ(),
		"VERDANA_NAPP_ID="+req.napp.ID,
		"VERDANA_NAPP_DIR="+req.dir,
		"VERDANA_NAPP_NAME="+req.napp.Name,
		"VERDANA_NAPP_DESC="+req.napp.Description,
	)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return
	}

	ci := &childInfo{
		cmd:  cmd,
		enc:  json.NewEncoder(stdin),
		subs: make(map[int]context.CancelFunc),
	}
	mu.Lock()
	children = append(children, ci)
	mu.Unlock()

	dec := json.NewDecoder(stdout)
	for {
		var m wireMsg
		if err := dec.Decode(&m); err != nil {
			break // EOF: the child window was closed / process exited
		}
		if m.T == "rpc" {
			m := m
			go handleChildRPC(ci, m)
		}
	}

	cleanupChild(ci)
	cmd.Wait()
}

// handleChildRPC answers a single RPC request from a child using the shared
// bridge handlers, then writes the response back over the child's stdin pipe.
func handleChildRPC(ci *childInfo, m wireMsg) {
	result, err := bridgeRPC(ci)(m.Method, m.Params)
	resp := wireMsg{T: "resp", ID: m.ID}
	if err != nil {
		resp.Error = err.Error()
	} else if raw, mErr := json.Marshal(result); mErr != nil {
		resp.Error = mErr.Error()
	} else {
		resp.Result = raw
	}
	ci.send(resp)
}

func (ci *childInfo) send(m wireMsg) {
	ci.encMu.Lock()
	defer ci.encMu.Unlock()
	ci.enc.Encode(m)
}

// eval pushes JavaScript to be executed inside the child's webview.
func (ci *childInfo) eval(code string) {
	ci.send(wireMsg{T: "eval", Code: code})
}

// cleanupChild cancels only this child's feed subscriptions and removes it from
// the registry. Other children and the parent launcher are unaffected.
func cleanupChild(ci *childInfo) {
	ci.subMu.Lock()
	for _, cancel := range ci.subs {
		cancel()
	}
	ci.subs = make(map[int]context.CancelFunc)
	ci.subMu.Unlock()

	mu.Lock()
	for i, c := range children {
		if c == ci {
			children = append(children[:i], children[i+1:]...)
			break
		}
	}
	mu.Unlock()
}

// ---- child process side ----

var (
	childOutMu     sync.Mutex
	childEnc       *json.Encoder // writes to parent over stdout
	childPendingMu sync.Mutex
	childPending   = make(map[int]chan wireMsg)
	childReqSerial atomic.Int64
)

// childMain hosts a single napp webview window and forwards every bridge RPC to
// the parent process over stdout, resolving the JS promises from the parent's
// responses read on stdin. When the window is closed, Run returns and the
// process exits, terminating only this window. The napp's data directory is
// provided via VERDANA_NAPP_DIR.
func childMain(nappID string) {
	nappDir = os.Getenv("VERDANA_NAPP_DIR")

	runtime.LockOSThread()

	napp := Napp{
		ID:          nappID,
		Name:        os.Getenv("VERDANA_NAPP_NAME"),
		Description: os.Getenv("VERDANA_NAPP_DESC"),
	}
	if napp.Name == "" {
		napp.Name = nappID
	}

	childEnc = json.NewEncoder(os.Stdout)

	w := webview.New(false)
	w.SetTitle(napp.Name)
	w.SetSize(600, 450, webview.HintNone)
	_ = w.Bind("__bridge_rpc", childRPC)
	w.Init(bridgeJS)
	w.SetHtml(nappHTML(napp))

	go childReader(w)

	w.Run()
	w.Destroy()
	os.Exit(0)
}

// childRPC is the bound __bridge_rpc callback. It forwards the call to the
// parent and blocks the calling goroutine until the matching response arrives.
// The independent childReader goroutine guarantees responses are always
// delivered, so this cannot deadlock. The returned json.RawMessage is
// re-marshaled verbatim by the binding, so the JS promise resolves identically
// to the parent computing the value in-process.
func childRPC(method string, params string) (any, error) {
	id := int(childReqSerial.Add(1))
	ch := make(chan wireMsg, 1)
	childPendingMu.Lock()
	childPending[id] = ch
	childPendingMu.Unlock()

	childWriteMsg(wireMsg{T: "rpc", ID: id, Method: method, Params: params})

	resp := <-ch
	childPendingMu.Lock()
	delete(childPending, id)
	childPendingMu.Unlock()

	if resp.Error != "" {
		return nil, errors.New(resp.Error)
	}
	return resp.Result, nil
}

// childReader routes parent responses to the waiting RPC goroutines and runs
// eval pushes on the webview's UI thread via Dispatch. On stdin EOF (parent
// gone) it terminates the window.
func childReader(w webview.WebView) {
	dec := json.NewDecoder(os.Stdin)
	for {
		var m wireMsg
		if err := dec.Decode(&m); err != nil {
			break
		}
		switch m.T {
		case "resp":
			childPendingMu.Lock()
			ch := childPending[m.ID]
			childPendingMu.Unlock()
			if ch != nil {
				ch <- m
			}
		case "eval":
			code := m.Code
			w.Dispatch(func() { w.Eval(code) })
		}
	}
	w.Terminate()
}

func childWriteMsg(m wireMsg) {
	childOutMu.Lock()
	defer childOutMu.Unlock()
	childEnc.Encode(m)
}

func bridgeRPC(ci *childInfo) func(string, string) (any, error) {
	return func(method string, params string) (any, error) {
		switch method {
		case "getPublicKey":
			return getPublicKey()
		case "signEvent":
			return signEvent(params)
		case "nip04.encrypt", "nip04.decrypt":
			return nip04crypt(method, params)
		case "nip44.encrypt", "nip44.decrypt":
			return nip44crypt(method, params)
		case "nostrdb.add":
			return nostrdbAdd(params)
		case "nostrdb.query":
			return nostrdbQuery(params)
		case "nostrdb.count":
			return nostrdbCount(params)
		case "nostrdb.event":
			return nostrdbEvent(params)
		case "nostrdb.replaceable":
			return nostrdbReplaceable(params)
		case "napp.action":
			return nappAction(params)
		case "napp.feeds.profile", "napp.feeds.following", "napp.feeds.inbox":
			return feedSubscribe(ci, method, params)
		case "napp.feeds.cancel":
			return feedCancel(ci, params)
		case "napp.loadBlossomServers":
			return emptyList(), nil
		case "napp.loadBookmarks":
			return emptyList(), nil
		case "napp.loadEmojis":
			return emptyList(), nil
		case "napp.loadFavoriteRelays":
			return emptyList(), nil
		case "napp.loadFavoriteScrolls":
			return emptyList(), nil
		case "napp.loadFollowsList":
			return loadFollowsList(params)
		case "napp.loadMuteList":
			return loadMuteList(params)
		case "napp.loadPins":
			return emptyList(), nil
		case "napp.loadRelayList":
			return emptyList(), nil
		case "napp.loadWikiAuthors":
			return emptyList(), nil
		case "napp.loadWikiRelays":
			return emptyList(), nil
		case "napp.loadEmojiSets":
			return emptySets(), nil
		case "napp.loadFollowPacks":
			return emptySets(), nil
		case "napp.loadFollowSets":
			return emptySets(), nil
		case "napp.loadRelaySets":
			return emptySets(), nil
		case "napp.loadRelayInfo":
			return loadRelayInfo(params)
		case "napp.loadNostrUser":
			return loadNostrUser(params)
		case "napp.loadEvent":
			return loadEvent(params)
		case "napp.publish":
			return publish(params)
		default:
			return nil, nil
		}
	}
}

func getPublicKey() (string, error) {
	if userKeyer == nil {
		return "", errors.New("not logged in")
	}
	if userPubkey != (nostr.PubKey{}) {
		return userPubkey.Hex(), nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pk, err := userKeyer.GetPublicKey(ctx)
	if err != nil {
		return "", err
	}
	return pk.Hex(), nil
}

func signEvent(params string) (any, error) {
	if userKeyer == nil {
		return nil, errors.New("not logged in")
	}
	var evt nostr.Event
	if err := json.Unmarshal([]byte(params), &evt); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := userKeyer.SignEvent(ctx, &evt); err != nil {
		return nil, err
	}
	return evt, nil
}

func nip04crypt(method string, params string) (string, error) {
	return "", nil
}

func nip44crypt(method string, params string) (string, error) {
	return "", nil
}

func nostrdbAdd(params string) (bool, error) {
	return true, nil
}

func nostrdbQuery(params string) (any, error) {
	return []any{}, nil
}

func nostrdbCount(params string) (int, error) {
	return 0, nil
}

func nostrdbEvent(params string) (any, error) {
	return nil, nil
}

func nostrdbReplaceable(params string) (any, error) {
	return nil, nil
}

func nappAction(params string) (any, error) {
	return nil, nil
}

func feedSubscribe(ci *childInfo, method string, params string) (any, error) {
	var p struct {
		Pubkey     string           `json:"pubkey"`
		Source     string           `json:"source"`
		Kinds      []nostr.Kind     `json:"kinds"`
		CallbackId int              `json:"callbackId"`
		Since      *nostr.Timestamp `json:"since"`
		Until      *nostr.Timestamp `json:"until"`
		Limit      int              `json:"limit"`
	}
	json.Unmarshal([]byte(params), &p)

	ctx, cancel := context.WithCancel(context.Background())
	ci.subMu.Lock()
	ci.subs[p.CallbackId] = cancel
	ci.subMu.Unlock()

	go feedPump(ctx, ci, method, p)

	return nil, nil
}

func feedPump(ctx context.Context, ci *childInfo, method string, p struct {
	Pubkey     string           `json:"pubkey"`
	Source     string           `json:"source"`
	Kinds      []nostr.Kind     `json:"kinds"`
	CallbackId int              `json:"callbackId"`
	Since      *nostr.Timestamp `json:"since"`
	Until      *nostr.Timestamp `json:"until"`
	Limit      int              `json:"limit"`
},
) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(30 * time.Second):
			ci.eval("")
		}
	}
}

func feedCancel(ci *childInfo, params string) (any, error) {
	var p struct {
		CallbackId int `json:"callbackId"`
	}
	json.Unmarshal([]byte(params), &p)
	ci.subMu.Lock()
	if cancel, ok := ci.subs[p.CallbackId]; ok {
		cancel()
		delete(ci.subs, p.CallbackId)
	}
	ci.subMu.Unlock()
	return nil, nil
}

func emptyList() any {
	return []any{}
}

func emptySets() any {
	return map[string]any{}
}

func loadFollowsList(params string) (any, error) {
	var p struct {
		Pubkey string `json:"pubkey"`
	}
	json.Unmarshal([]byte(params), &p)
	if sys == nil || p.Pubkey == "" {
		return emptyList(), nil
	}
	pk, err := nostr.PubKeyFromHex(p.Pubkey)
	if err != nil {
		return emptyList(), nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	list := sys.FetchFollowList(ctx, pk)
	return list, nil
}

func loadMuteList(params string) (any, error) {
	var p struct {
		Pubkey string `json:"pubkey"`
	}
	json.Unmarshal([]byte(params), &p)
	if sys == nil || p.Pubkey == "" {
		return emptyList(), nil
	}
	pk, err := nostr.PubKeyFromHex(p.Pubkey)
	if err != nil {
		return emptyList(), nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	list := sys.FetchMuteList(ctx, pk)
	return list, nil
}

func loadRelayInfo(params string) (any, error) {
	return nil, nil
}

func loadNostrUser(params string) (any, error) {
	return nil, nil
}

func loadEvent(params string) (any, error) {
	return nil, nil
}

func publish(params string) (any, error) {
	return nil, nil
}

const bridgeJS = `;(() => {
const _rpc = window.__bridge_rpc;
const pending = new Map();
const feedCallbacks = new Map();
const actionHandlers = [];
let feedSerial = 0;

function rpc(method, params) {
  const id = 'rpc' + (feedSerial++);
  return new Promise((resolve, reject) => {
    pending.set(id, {resolve, reject});
    _rpc(method, params !== undefined ? JSON.stringify(params) : null).then(result => {
      const p = pending.get(id);
      if (!p) return;
      pending.delete(id);
      p.resolve(JSON.parse(result));
    }).catch(err => {
      const p = pending.get(id);
      if (!p) return;
      pending.delete(id);
      p.reject(err);
    });
  });
}

window.__bridge_feed_callback = function(callbackId, eventsJSON, synced) {
  const cb = feedCallbacks.get(callbackId);
  if (cb) cb(JSON.parse(eventsJSON), synced);
};

window.__bridge_dispatch_action = function(name, payloadJSON, idx) {
  if (typeof idx === 'number') {
    const fn = actionHandlers[idx]?.[1];
    if (fn) {
      Promise.resolve().then(() => fn(name, JSON.parse(payloadJSON))).catch(() => {});
    }
  }
  const state = { action: { name, payload: payloadJSON ? JSON.parse(payloadJSON) : null } };
  history.pushState(state, '', location.href);
  window.dispatchEvent(new PopStateEvent('popstate', { state }));
};

window.__bridge_theme_change = function(theme, varsJSON) {
  document.documentElement.dataset.theme = theme;
  if (varsJSON) {
    const vars = JSON.parse(varsJSON);
    for (const key in vars) {
      document.documentElement.style.setProperty('--' + key, vars[key]);
    }
  }
};

window.nostr = {
  getPublicKey: () => rpc('getPublicKey'),
  signEvent: evt => rpc('signEvent', evt),
  nip04: {
    encrypt: (pubkey, plaintext) => rpc('nip04.encrypt', {pubkey, plaintext}),
    decrypt: (pubkey, ciphertext) => rpc('nip04.decrypt', {pubkey, ciphertext})
  },
  nip44: {
    encrypt: (pubkey, plaintext) => rpc('nip44.encrypt', {pubkey, plaintext}),
    decrypt: (pubkey, ciphertext) => rpc('nip44.decrypt', {pubkey, ciphertext})
  }
};

window.nostrdb = {
  add: event => rpc('nostrdb.add', {event}),
  query: filters => rpc('nostrdb.query', {filters}),
  count: filters => rpc('nostrdb.count', {filters}),
  event: id => rpc('nostrdb.event', {id}),
  replaceable: (kind, author, identifier) => rpc('nostrdb.replaceable', {kind, author, identifier}),
  supports: async () => []
};

function feedRpc(method, params, callback) {
  if (!callback) throw new Error('no callback specified');
  const callbackId = feedSerial++;
  params.callbackId = callbackId;
  feedCallbacks.set(callbackId, callback);
  rpc(method, params);
  return {
    close() {
      feedCallbacks.delete(callbackId);
      rpc('napp.feeds.cancel', {callbackId}).catch(() => {});
    }
  };
}

let __pointer = {x: 0, y: 0};
window.addEventListener('pointermove', e => { __pointer = {x: e.clientX, y: e.clientY}; }, {passive: true});

window.napp = {
  instance: window.name,
  action: (name, payload, options) => rpc('napp.action', {name, payload, options, pointer: __pointer}),
  registerAction(pattern, fn) {
    if (typeof pattern !== 'string' || !pattern) throw new Error('pattern required');
    let idx;
    if (typeof fn === 'function') {
      idx = actionHandlers.length;
      actionHandlers.push([pattern, fn]);
    }
  },
  feeds: {
    profile: (pubkey, kinds, callback, opts) => feedRpc('napp.feeds.profile', {pubkey, kinds, ...opts}, callback),
    following: (source, kinds, callback, opts) => feedRpc('napp.feeds.following', {source, kinds, ...opts}, callback),
    inbox: (pubkey, kinds, callback, opts) => feedRpc('napp.feeds.inbox', {pubkey, kinds, ...opts}, callback)
  },
  utils: {
    loadBlossomServers: (pubkey, hints, refresh, def) => rpc('napp.loadBlossomServers', {pubkey, hints, refreshStyle: refresh, defaultItems: def}),
    loadBookmarks: (pubkey, hints, refresh, def) => rpc('napp.loadBookmarks', {pubkey, hints, refreshStyle: refresh, defaultItems: def}),
    loadEmojis: (pubkey, hints, refresh, def) => rpc('napp.loadEmojis', {pubkey, hints, refreshStyle: refresh, defaultItems: def}),
    loadFavoriteRelays: (pubkey, hints, refresh, def) => rpc('napp.loadFavoriteRelays', {pubkey, hints, refreshStyle: refresh, defaultItems: def}),
    loadFavoriteScrolls: (pubkey, hints, refresh, def) => rpc('napp.loadFavoriteScrolls', {pubkey, hints, refreshStyle: refresh, defaultItems: def}),
    loadFollowsList: (pubkey, hints, refresh, def) => rpc('napp.loadFollowsList', {pubkey, hints, refreshStyle: refresh, defaultItems: def}),
    loadMuteList: (pubkey, hints, refresh, def) => rpc('napp.loadMuteList', {pubkey, hints, refreshStyle: refresh, defaultItems: def}),
    loadPins: (pubkey, hints, refresh, def) => rpc('napp.loadPins', {pubkey, hints, refreshStyle: refresh, defaultItems: def}),
    loadRelayList: (pubkey, hints, refresh, def) => rpc('napp.loadRelayList', {pubkey, hints, refreshStyle: refresh, defaultItems: def}),
    loadWikiAuthors: (pubkey, hints, refresh, def) => rpc('napp.loadWikiAuthors', {pubkey, hints, refreshStyle: refresh, defaultItems: def}),
    loadWikiRelays: (pubkey, hints, refresh, def) => rpc('napp.loadWikiRelays', {pubkey, hints, refreshStyle: refresh, defaultItems: def}),
    loadEmojiSets: (pubkey, hints, force) => rpc('napp.loadEmojiSets', {pubkey, hints, forceUpdate: force}),
    loadFollowPacks: (pubkey, hints, force) => rpc('napp.loadFollowPacks', {pubkey, hints, forceUpdate: force}),
    loadFollowSets: (pubkey, hints, force) => rpc('napp.loadFollowSets', {pubkey, hints, forceUpdate: force}),
    loadRelaySets: (pubkey, hints, force) => rpc('napp.loadRelaySets', {pubkey, hints, forceUpdate: force}),
    loadRelayInfo: (url, refresh) => rpc('napp.loadRelayInfo', {url, refreshStyle: refresh}),
    loadNostrUser: req => rpc('napp.loadNostrUser', req),
    loadEvent: (code, relays, author) => rpc('napp.loadEvent', {code, relays, author}),
    publish: (event, relays) => rpc('napp.publish', {event, relays})
  }
};
})();`

func nappHTML(napp Napp) string {
	return `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<style>
body { font-family: sans-serif; padding: 2em; background: #fafafa; }
h1 { color: #333; }
p { color: #666; line-height: 1.5; }
</style>
</head>
<body>
<h1>` + napp.Name + `</h1>
<p>` + napp.Description + `</p>
<p>App ID: ` + napp.ID + `</p>
</body>
</html>`
}
