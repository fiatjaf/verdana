package main

import (
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore/lmdb"
	"fiatjaf.com/nostr/sdk"
	bolt_kv "fiatjaf.com/nostr/sdk/kvstore/bbolt"
	"gioui.org/app"
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
}

var napps = []Napp{
	{ID: "hello-world", Name: "Hello World", Description: "Simple hello world demo"},
	{ID: "counter", Name: "Counter", Description: "Increment and decrement counter"},
	{ID: "chat", Name: "Chat", Description: "Anonymous chat room"},
	{ID: "draw", Name: "Drawing Board", Description: "Collaborative drawing canvas"},
	{ID: "notes", Name: "Notes", Description: "Shared sticky notes"},
}

var sys *sdk.System

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

func gioMain() {
	var nappList widget.List
	nappList.Axis = layout.Vertical

	installBtns := make([]widget.Clickable, len(napps))

	th := material.NewTheme()
	th.Shaper = text.NewShaper(text.WithCollection(gofont.Collection()))

	w := new(app.Window)
	w.Option(app.Title("Verdana"), app.Size(unit.Dp(520), unit.Dp(500)))

	var ops op.Ops
	for {
		switch e := w.Event(); e := e.(type) {
		case app.FrameEvent:
			gtx := app.NewContext(&ops, e)

			for i := range installBtns {
				if installBtns[i].Clicked(gtx) {
					napp := napps[i]
					appDir := filepath.Join(verdanaDir, "napps", napp.ID)
					os.MkdirAll(appDir, 0755)
					select {
					case openReqCh <- openReq{napp: napp, dir: appDir}:
					default:
					}
				}
			}

			layout.Inset{
				Top: unit.Dp(16), Bottom: unit.Dp(16),
				Left: unit.Dp(16), Right: unit.Dp(16),
			}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return material.List(th, &nappList).Layout(gtx, len(napps), func(gtx layout.Context, i int) layout.Dimensions {
					return renderCard(gtx, th, &installBtns[i], napps[i])
				})
			})

			e.Frame(gtx.Ops)

		case app.DestroyEvent:
			return
		}
	}
}

func renderCard(gtx layout.Context, th *material.Theme, btn *widget.Clickable, napp Napp) layout.Dimensions {
	return layout.Inset{Bottom: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		sz := gtx.Constraints.Max
		defer clip.RRect{
			Rect: image.Rectangle{Max: sz},
			NW:   8, NE: 8, SW: 8, SE: 8,
		}.Push(gtx.Ops).Pop()
		paint.Fill(gtx.Ops, color.NRGBA{R: 0xf0, G: 0xf0, B: 0xf0, A: 0xff})

		return layout.Inset{
			Top: unit.Dp(12), Bottom: unit.Dp(12),
			Left: unit.Dp(16), Right: unit.Dp(16),
		}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							label := material.H6(th, napp.Name)
							label.Font.Weight = font.Bold
							return label.Layout(gtx)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							pointer.CursorPointer.Add(gtx.Ops)
							b := material.Button(th, btn, "Install")
							b.TextSize = unit.Sp(12)
							b.Inset = layout.UniformInset(unit.Dp(6))
							return b.Layout(gtx)
						}),
					)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						label := material.Body2(th, napp.Description)
						label.Color = color.NRGBA{R: 0x66, G: 0x66, B: 0x66, A: 0xff}
						return label.Layout(gtx)
					})
				}),
			)
		})
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

	var napp Napp
	for _, n := range napps {
		if n.ID == nappID {
			napp = n
			break
		}
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
	return "", nil
}

func signEvent(params string) (any, error) {
	return nil, nil
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
