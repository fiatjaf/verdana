package main

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/abemedia/go-webview"
	_ "github.com/abemedia/go-webview/embedded"
	"github.com/puzpuzpuz/xsync/v3"
	"github.com/rs/zerolog"

	nappbridge "verdana/backend/webview"
)

type wireMsg struct {
	T      string          `json:"t"`
	ID     int             `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params string          `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
	Code   string          `json:"code,omitempty"`
	Idx    *int            `json:"idx,omitempty"`
}

type nappMeta struct {
	ID          string
	Name        string
	Description string
	Dir         string
	URL         string
	Instance    string
	Requires    []string
	Theme       string
	ThemeVars   string
}

var (
	meta      nappMeta
	outMu     sync.Mutex
	outEnc    *json.Encoder
	pending   = xsync.NewMapOf[int, chan wireMsg]()
	reqSerial atomic.Int64
	log       zerolog.Logger
)

func main() {
	log = zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr}).With().
		Int("_", os.Getpid()).
		Timestamp().
		Logger()

	meta = nappMeta{
		ID:          os.Getenv("VERDANA_NAPP_ID"),
		Dir:         os.Getenv("VERDANA_NAPP_DIR"),
		URL:         strings.TrimSpace(os.Getenv("VERDANA_NAPP_URL")),
		Name:        os.Getenv("VERDANA_NAPP_NAME"),
		Description: os.Getenv("VERDANA_NAPP_DESC"),
		Instance:    os.Getenv("VERDANA_INSTANCE_ID"),
		Theme:       os.Getenv("VERDANA_THEME"),
		ThemeVars:   os.Getenv("VERDANA_THEME_VARS"),
	}
	if req := strings.TrimSpace(os.Getenv("VERDANA_NAPP_REQUIRES")); req != "" {
		meta.Requires = strings.Split(req, ",")
	}
	if meta.Name == "" {
		meta.Name = meta.ID
	}
	if meta.Instance == "" {
		meta.Instance = meta.ID
	}

	log.Info().Str("napp", meta.ID).Str("instance", meta.Instance).Msg("napp process started")
	outEnc = json.NewEncoder(os.Stdout)

	runtime.LockOSThread()

	w := webview.New(os.Getenv("WEBVIEW_DEBUG") == "true")
	w.SetTitle(windowTitle(meta.Name, os.Getenv("VERDANA_INSTANCE_ID")))
	w.SetSize(windowWidth(), windowHeight(), webview.HintNone)
	_ = w.Bind("__bridge_rpc", rpcBound)
	_ = w.Bind("__verdana_prompt_answer", promptAnswer)

	// window.name is where bridge.js picks up window.napp.instance, and it
	// survives same-origin navigations — so a reload keeps the instance id.
	// window.__nappTheme is where bridge.js picks the launcher's theme up on
	// every (re)load, so a napp that reloads itself stays in sync.
	// window.__nappStorage seeds the localStorage shim: the file is the
	// per-nappId JSON store the backend persists (native localStorage would
	// be a fresh empty origin on every launch, random port each time).
	w.Init("window.name = " + jsString(meta.Instance) + ";" +
		"window.__nappDomains = " + jsStringSlice(meta.Requires) + ";" +
		storageInitScript(os.Getenv("VERDANA_NAPP_STORAGE_FILE")) +
		themeInitScript(meta.Theme, meta.ThemeVars))
	// the napp-ui kit, for the napps that ask for it with requires: ["ui"]
	if kit := nappbridge.UIKitScript(meta.Requires); kit != "" {
		w.Init(kit)
	}
	// the very same bridge.js the Android app injects
	w.Init(nappbridge.JS())

	// a dev napp navigates straight to its page (its dev-server url, or the
	// launcher's throwaway server): the bridge bindings below don't depend
	// on the page's origin, so nothing else changes.
	url := meta.URL
	if url == "" {
		url = startNappServer(meta.Dir)
	}
	w.Navigate(url)

	go reader(w)

	w.Run()
	w.Destroy()
	os.Exit(0)
}

func jsString(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(b)
}

// windowWidth/Height are the initial window size in pixels: the napp's
// initial_size via the launcher, falling back to a roomy default. Values are
// clamped like nostrapps sanitizes them (positive, capped at 2000, with a
// minimum that keeps the window usable).
func windowWidth() int { return clampWindowSize(envSize("VERDANA_WINDOW_WIDTH", 1024), 320, 2000) }

func windowHeight() int { return clampWindowSize(envSize("VERDANA_WINDOW_HEIGHT", 700), 240, 2000) }

func envSize(key string, fallback int) int {
	if raw := strings.TrimSpace(os.Getenv(key)); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			return v
		}
	}
	return fallback
}

func clampWindowSize(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

// windowTitle names the OS window after the napp and its instance, so the
// user can match it to the "window <instance>" entries in action prompts.
func windowTitle(name, instance string) string {
	if name == "" {
		name = "Napp"
	}
	if instance == "" {
		return name
	}
	return name + " - window " + instance
}

// storageInitScript seeds window.__nappStorage, which the bridge's
// localStorage shim runs synchronously from. The file is the backend's
// per-nappId JSON store; missing/corrupt means start empty.
func storageInitScript(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil || len(strings.TrimSpace(string(raw))) == 0 {
		return "window.__nappStorage = {};"
	}
	var probe map[string]string
	if err := json.Unmarshal(raw, &probe); err != nil {
		return "window.__nappStorage = {};"
	}
	return "window.__nappStorage = " + string(raw) + ";"
}

// themeInitScript sets window.__nappTheme, which bridge.js applies as soon as
// it runs. varsJSON comes from the launcher, so it is already valid JSON.
func themeInitScript(name, varsJSON string) string {
	if name == "" {
		name = "light"
	}
	if strings.TrimSpace(varsJSON) == "" {
		varsJSON = "{}"
	}
	return "window.__nappTheme = {name:" + jsString(name) + ",vars:" + varsJSON + "};"
}

func jsStringSlice(items []string) string {
	b, err := json.Marshal(items)
	if err != nil {
		return "[]"
	}
	return string(b)
}

func startNappServer(root string) string {
	if root == "" {
		return ""
	}
	if _, err := os.Stat(filepath.Join(root, "index.html")); err != nil {
		log.Debug().Str("root", root).Msg("no index.html found for napp")
		return ""
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Error().Err(err).Str("root", root).Msg("failed to listen for napp server")
		return ""
	}
	fs := http.FileServer(http.Dir(root))
	handler := http.HandlerFunc(func(wr http.ResponseWriter, r *http.Request) {
		// Keep top-level navigation inside this napp origin. External URLs must
		// be opened through window.napp.link(), which goes through the host.
		wr.Header().Set("Content-Security-Policy", "navigate-to 'self'")
		clean := filepath.Join(root, filepath.FromSlash(path.Clean("/"+r.URL.Path)))
		if st, statErr := os.Stat(clean); statErr != nil || st.IsDir() {
			if r.URL.Path != "/" && !strings.Contains(path.Base(r.URL.Path), ".") {
				http.ServeFile(wr, r, filepath.Join(root, "index.html"))
				return
			}
		}
		fs.ServeHTTP(wr, r)
	})
	go http.Serve(ln, handler)
	return "http://" + ln.Addr().String() + "/"
}

func rpcBound(method string, params string) string {
	result, err := rpc(method, params)
	if err != nil {
		wrapped, _ := json.Marshal(map[string]string{"__bridge_error": err.Error()})
		return string(wrapped)
	}
	if len(result) == 0 {
		return "null"
	}
	return string(result)
}

func rpc(method string, params string) (json.RawMessage, error) {
	id := int(reqSerial.Add(1))
	ch := make(chan wireMsg, 1)
	pending.Store(id, ch)

	writeMsg(wireMsg{T: "rpc", ID: id, Method: method, Params: params})

	resp := <-ch
	pending.Delete(id)

	if resp.Error != "" {
		return nil, errors.New(resp.Error)
	}
	return resp.Result, nil
}

func reader(w webview.WebView) {
	dec := json.NewDecoder(os.Stdin)
	for {
		var m wireMsg
		if err := dec.Decode(&m); err != nil {
			break
		}
		switch m.T {
		case "prompt":
			// the prompt this napp asked for covers its own screen until
			// answered (empty params take a stale overlay down)
			if strings.TrimSpace(m.Params) == "" {
				w.Dispatch(func() { w.Eval(promptHideCode()) })
				continue
			}
			var pv promptView
			if err := json.Unmarshal([]byte(m.Params), &pv); err != nil {
				log.Warn().Err(err).Msg("unreadable prompt from launcher")
				continue
			}
			code := promptOverlayCode(pv)
			w.Dispatch(func() { w.Eval(code) })
		case "resp":
			if ch, ok := pending.Load(m.ID); ok {
				ch <- m
			}
		case "eval":
			code := m.Code
			w.Dispatch(func() { w.Eval(code) })
		case "action":
			// the launcher routed an action here: hand it to the napp's
			// registered handler (idx) and/or its popstate listener
			idx := "null"
			if m.Idx != nil {
				idx = strconv.Itoa(*m.Idx)
			}
			payload := m.Params
			if payload == "" {
				payload = "null"
			}
			code := "window.__bridge_dispatch_action(" +
				strconv.Itoa(m.ID) + "," + jsString(m.Method) + "," + jsString(payload) + "," + idx + ")"
			w.Dispatch(func() { w.Eval(code) })
		case "theme":
			// the launcher switched theme: apply it now, and make it the value
			// future page loads in this window start from
			init := themeInitScript(m.Method, m.Params)
			vars := m.Params
			if vars == "" {
				vars = "{}"
			}
			code := init +
				"if (window.__bridge_theme_change) window.__bridge_theme_change(" +
				jsString(m.Method) + "," + jsString(vars) + ");"
			w.Dispatch(func() {
				w.Init(init)
				w.Eval(code)
			})
		case "close":
			w.Dispatch(func() { w.Terminate() })
		}
	}
	w.Terminate()
}

func writeMsg(m wireMsg) {
	outMu.Lock()
	defer outMu.Unlock()
	outEnc.Encode(m)
}

// ─── prompt overlay ──────────────────────────────────────────────
//
// A prompt fired by this napp is shown over its own screen: an opaque
// overlay that hides the webview until the user answers it.

// promptView mirrors backend.Prompt as the overlay needs to see it. A
// picker never comes here: it is a window of its own.
type promptView struct {
	ID       int      `json:"id"`
	Title    string   `json:"title"`
	Detail   string   `json:"detail"`
	Warning  string   `json:"warning"`
	Items    []string `json:"items"`
	Code     string   `json:"code"`
	Summary  string   `json:"summary"`
	Image    string   `json:"image"`
	Remember bool     `json:"remember"`
}

// promptAnswer is the bound call the overlay's buttons make. It sends the
// answer up to the launcher and takes the overlay down; if the launcher has
// another prompt queued for this window it will send it right back. scope is
// how long the answer holds: "once", "session" or "always" (see
// backend.Scope) — the launcher files the wider ones away and stops asking.
// skip is the indexes of the items (relays) the user unticked.
func promptAnswer(id int, ok bool, scope string, skip []int) {
	b, _ := json.Marshal(map[string]any{"ok": ok, "scope": scope, "skip": skip})
	writeMsg(wireMsg{T: "promptAnswer", ID: id, Params: string(b)})
}

func promptOverlayCode(pv promptView) string {
	data, err := json.Marshal(pv)
	if err != nil {
		return ""
	}
	return promptLibScript + ";window.__verdana_prompt_lib.show(" + string(data) + ");"
}

func promptHideCode() string {
	return ";if (window.__verdana_prompt_lib) window.__verdana_prompt_lib.hide();"
}

// promptLibScript defines the overlay runtime once per page. It draws an
// opaque full-viewport cover — the napp underneath stays hidden until the
// user answers.
const promptLibScript = "(function(){" +
	"if (window.__verdana_prompt_lib) return;" +
	"function tok(k, fallback) {" +
	"try { var v = (window.__nappTheme && window.__nappTheme.vars) || {};" +
	"return v[k] || fallback } catch(e) { return fallback }" +
	"};" +
	"window.__verdana_prompt_lib = {" +
	"show: function(p) {" +
	"var old = document.getElementById('__verdana_prompt');" +
	"if (old && old.parentNode) old.parentNode.removeChild(old);" +
	"var dark = window.__nappTheme && window.__nappTheme.name === 'dark';" +
	"var bg = tok('surface', dark ? '#17181b' : '#ffffff');" +
	"var card = tok('surface-alt', dark ? '#23252b' : '#f2f2f2');" +
	"var fg = tok('text', dark ? '#e8e8ea' : '#000000');" +
	"var muted = tok('text-faint', dark ? '#7d818a' : '#999999');" +
	"var accent = tok('accent', dark ? '#5c6bc0' : '#3f51b5');" +
	"var accentText = tok('accent-text', '#ffffff');" +
	"var border = tok('border', dark ? '#3a3d45' : '#cccccc');" +
	"var danger = tok('danger', dark ? '#ff6b6b' : '#cc2222');" +
	"var o = document.createElement('div');" +
	"o.id = '__verdana_prompt';" +
	"o.style.cssText = 'position:fixed;top:0;left:0;width:100vw;height:100vh;" +
	"z-index:2147483647;background:' + bg + ';color:' + fg" +
	"+ ';font:14px sans-serif;overflow:auto;padding:24px;box-sizing:border-box;" +
	"margin:0;border:0;display:flex;flex-direction:column;';" +
	// an image is shown as big as it fits, with what to do about it to
	// the side (under it, on a narrow window)
	"if (p.image) {" +
	"var narrow = window.innerWidth < 600;" +
	"o.style.flexDirection = narrow ? 'column' : 'row';" +
	"o.style.gap = '20px'; o.style.overflow = 'hidden';" +
	"var im = document.createElement('img');" +
	"im.src = p.image;" +
	"im.style.cssText = 'flex:1;min-width:0;min-height:0;width:100%;height:100%;object-fit:contain;';" +
	"im.onerror = function() { im.alt = 'the image could not be loaded'; im.style.color = muted };" +
	"o.appendChild(im);" +
	"var side = document.createElement('div');" +
	"side.style.cssText = 'flex:none;display:flex;flex-direction:column;gap:8px;justify-content:center;'" +
	"+ (narrow ? '' : 'width:240px;');" +
	"var t = document.createElement('div');" +
	"t.textContent = p.title;" +
	"t.style.cssText = 'font-size:15px;font-weight:700;';" +
	"side.appendChild(t);" +
	"var u = document.createElement('div');" +
	"u.textContent = p.code || p.image;" +
	"u.style.cssText = 'color:' + muted + ';font:12px monospace;word-break:break-all;margin-bottom:6px;';" +
	"side.appendChild(u);" +
	"function act(label, ok, loud) {" +
	"var b = document.createElement('button');" +
	"b.textContent = label;" +
	"b.style.cssText = 'padding:10px 14px;border:0;border-radius:8px;font-size:14px;cursor:pointer;" +
	"background:' + (loud ? accent : card) + ';color:' + (loud ? accentText : fg) + ';';" +
	"b.onclick = function() { window.__verdana_prompt_answer(p.id, ok, 'once', []) };" +
	"side.appendChild(b);" +
	"};" +
	"act('Open in browser', true, true);" +
	"act('Close', false, false);" +
	"o.appendChild(side);" +
	"document.documentElement.appendChild(o);" +
	"return;" +
	"}" +
	"var box = document.createElement('div');" +
	"box.style.cssText = 'margin:auto 0;width:100%;max-width:520px;';" +
	"var h = document.createElement('div');" +
	"h.textContent = p.title;" +
	"h.style.cssText = 'font-size:17px;font-weight:700;margin:0 0 10px;';" +
	"box.appendChild(h);" +
	"if (p.detail) {" +
	"var d = document.createElement('div');" +
	"d.textContent = p.detail;" +
	"d.style.cssText = 'color:' + muted + ';margin-bottom:14px;line-height:1.45;';" +
	"box.appendChild(d);" +
	"}" +
	// the items come with a box each, ticked to start with: the answer
	// says which ones the user unticked
	"var boxes = [];" +
	"if (p.items && p.items.length) {" +
	"var list = document.createElement('div');" +
	"list.style.cssText = 'margin:-6px 0 14px;border:1px solid ' + border + ';border-radius:8px;overflow:hidden;';" +
	"p.items.forEach(function(it, i) {" +
	"var r = document.createElement('label');" +
	"r.style.cssText = 'display:flex;align-items:center;gap:8px;cursor:pointer;padding:6px 10px" +
	";font:12px monospace;word-break:break-all;'" +
	"+ (i ? 'border-top:1px solid ' + border + ';' : '');" +
	"var cb = document.createElement('input');" +
	"cb.type = 'checkbox'; cb.checked = true;" +
	"cb.style.cssText = 'margin:0;accent-color:' + accent + ';';" +
	"boxes.push(cb);" +
	"r.appendChild(cb);" +
	"r.appendChild(document.createTextNode(it));" +
	"list.appendChild(r);" +
	"});" +
	"box.appendChild(list);" +
	"}" +
	"function skipped() {" +
	"var out = [];" +
	"boxes.forEach(function(cb, i) { if (!cb.checked) out.push(i) });" +
	"return out;" +
	"};" +
	"if (p.warning) {" +
	"var w = document.createElement('div');" +
	"w.textContent = p.warning;" +
	"w.style.cssText = 'color:' + danger + ';border:1px solid ' + danger + ';border-radius:8px" +
	";padding:8px 10px;margin:0 0 14px;font-weight:700;line-height:1.4;';" +
	"box.appendChild(w);" +
	"}" +
	"if (p.code) {" +
	"var c = document.createElement('pre');" +
	"c.style.cssText = 'white-space:pre-wrap;word-break:break-word;background:' + card " +
	"+ ';border:1px solid ' + border + ';border-radius:8px;padding:10px" +
	";max-height:' + (p.summary ? '320px' : '160px') + ';overflow:auto;font:12px monospace;margin:0 0 14px;';" +
	"c.textContent = p.code;" +
	// with a summary the payload starts folded under it
	"if (p.summary) {" +
	"var f = document.createElement('details');" +
	"f.style.cssText = 'margin:0 0 14px;';" +
	"var s = document.createElement('summary');" +
	"s.textContent = p.summary;" +
	"s.style.cssText = 'cursor:pointer;color:' + muted + ';font:12px monospace;padding:4px 0;';" +
	"c.style.marginTop = '6px'; c.style.marginBottom = '0';" +
	"f.appendChild(s); f.appendChild(c);" +
	"box.appendChild(f);" +
	"} else {" +
	"box.appendChild(c);" +
	"}" +
	"}" +
	"function btn(label, tone, ok, scope) {" +
	"var bgc = card, fgc = fg;" +
	"if (tone === 'accent') { bgc = accent; fgc = accentText }" +
	"var b = document.createElement('button');" +
	"b.style.cssText = 'display:block;width:100%;max-width:360px;margin:0 auto 8px;padding:10px 14px" +
	";border:0;border-radius:8px;background:' + bgc + ';color:' + fgc + ';font-size:14px;text-align:left" +
	";cursor:pointer;';" +
	"b.textContent = label;" +
	"b.title = tip(label, ok, scope);" +
	"b.onclick = function() { window.__verdana_prompt_answer(p.id, ok, scope, skipped()) };" +
	"return b;" +
	"};" +
	// tip is what an answer button says it does, on hover.
	"function tip(label, ok, scope) {" +
	"if (scope === 'session') return (ok ? 'Allow' : 'Refuse') + ' it without asking again until the launcher quits';" +
	"if (scope === 'always') return (ok ? 'Allow' : 'Refuse') + ' it from now on, without ever asking again';" +
	"return (ok ? 'Allow' : 'Refuse') + ' it this time only';" +
	"};" +
	// row puts buttons side by side, for the scopes of an approval prompt.
	"function row() {" +
	"var r = document.createElement('div');" +
	"r.style.cssText = 'display:flex;gap:8px;margin:0 0 8px;';" +
	"for (var i = 0; i < arguments.length; i++) {" +
	"var b = arguments[i];" +
	"b.style.maxWidth = 'none'; b.style.margin = '0'; b.style.flex = '1'; b.style.textAlign = 'center';" +
	"r.appendChild(b);" +
	"}" +
	"return r;" +
	"};" +
	"if (p.remember) {" +
	"box.appendChild(row(btn('Allow', 'accent', true, 'once'), btn('Deny', 'chip', false, 'once')));" +
	"box.appendChild(row(btn('Allow this session', 'chip', true, 'session')," +
	"btn('Deny this session', 'chip', false, 'session')));" +
	"box.appendChild(row(btn('Always allow', 'chip', true, 'always'), btn('Always deny', 'chip', false, 'always')));" +
	"} else {" +
	"box.appendChild(btn('Allow', 'accent', true, 'once'));" +
	"box.appendChild(btn('Deny', 'chip', false, 'once'));" +
	"}" +
	"o.appendChild(box);" +
	"document.documentElement.appendChild(o);" +
	"}," +
	"hide: function() {" +
	"var o = document.getElementById('__verdana_prompt');" +
	"if (o && o.parentNode) o.parentNode.removeChild(o);" +
	"}" +
	"};" +
	"})()"
