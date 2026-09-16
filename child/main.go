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
	Instance    string
	Requires    []string
	Theme       string
	ThemeVars   string
}

var (
	meta      nappMeta
	outMu     sync.Mutex
	outEnc    *json.Encoder
	pendingMu sync.Mutex
	pending   = make(map[int]chan wireMsg)
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
	w.SetTitle(meta.Name)
	w.SetSize(600, 450, webview.HintNone)
	_ = w.Bind("__bridge_rpc", rpcBound)
	_ = w.Bind("__verdana_prompt_answer", promptAnswer)

	// window.name is where bridge.js picks up window.napp.instance, and it
	// survives same-origin navigations — so a reload keeps the instance id.
	// window.__nappTheme is where bridge.js picks the launcher's theme up on
	// every (re)load, so a napp that reloads itself stays in sync.
	w.Init("window.name = " + jsString(meta.Instance) + ";" +
		"window.__nappDomains = " + jsStringSlice(meta.Requires) + ";" +
		themeInitScript(meta.Theme, meta.ThemeVars))
	// the very same bridge.js the Android app injects
	w.Init(nappbridge.JS())

	url := startNappServer(meta.Dir)
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
	pendingMu.Lock()
	pending[id] = ch
	pendingMu.Unlock()

	writeMsg(wireMsg{T: "rpc", ID: id, Method: method, Params: params})

	resp := <-ch
	pendingMu.Lock()
	delete(pending, id)
	pendingMu.Unlock()

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
			pendingMu.Lock()
			ch := pending[m.ID]
			pendingMu.Unlock()
			if ch != nil {
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

// promptView and promptOptionView mirror backend.Prompt as the overlay needs
// to see it.
type promptView struct {
	ID      int                `json:"id"`
	Title   string             `json:"title"`
	Detail  string             `json:"detail"`
	Code    string             `json:"code"`
	Options []promptOptionView `json:"options"`
}

type promptOptionView struct {
	Label    string `json:"label"`
	Detail   string `json:"detail"`
	NappID   string `json:"nappId"`
	Instance string `json:"instance"`
}

// promptAnswer is the bound call the overlay's buttons make. It sends the
// answer up to the launcher and takes the overlay down; if the launcher has
// another prompt queued for this window it will send it right back.
func promptAnswer(id int, ok bool, index int) {
	b, _ := json.Marshal(map[string]any{"ok": ok, "index": index})
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
	"var o = document.createElement('div');" +
	"o.id = '__verdana_prompt';" +
	"o.style.cssText = 'position:fixed;top:0;left:0;width:100vw;height:100vh;" +
	"z-index:2147483647;background:' + bg + ';color:' + fg" +
	"+ ';font:14px sans-serif;overflow:auto;padding:24px;box-sizing:border-box;" +
	"margin:0;border:0;display:flex;flex-direction:column;';" +
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
	"if (p.code) {" +
	"var c = document.createElement('pre');" +
	"c.style.cssText = 'white-space:pre-wrap;word-break:break-word;background:' + card " +
	"+ ';border:1px solid ' + border + ';border-radius:8px;padding:10px" +
	";max-height:160px;overflow:auto;font:12px monospace;margin:0 0 14px;';" +
	"c.textContent = p.code;" +
	"box.appendChild(c);" +
	"}" +
	"function btn(label, detail, ok, index) {" +
	"var b = document.createElement('button');" +
	"b.style.cssText = 'display:block;width:100%;padding:10px 14px;margin-bottom:8px" +
	";border:0;border-radius:8px;background:' + accent" +
	"+ ';color:' + accentText + ';font-size:14px;text-align:left;cursor:pointer;';" +
	"b.textContent = detail ? label + '  \\u2014  ' + detail : label;" +
	"b.onclick = function() { window.__verdana_prompt_answer(p.id, ok, index) };" +
	"return b;" +
	"};" +
	"var isPicker = p.options && p.options.length;" +
	"if (isPicker) {" +
	"p.options.forEach(function(opt, i) { box.appendChild(btn(opt.label, opt.detail, true, i)) });" +
	"} else {" +
	"box.appendChild(btn('Allow', '', true, 0));" +
	"}" +
	"var cancel = btn(isPicker ? 'Cancel' : 'Deny', '', false, 0);" +
	"cancel.style.background = card; cancel.style.color = fg;" +
	"box.appendChild(cancel);" +
	"o.appendChild(box);" +
	"document.documentElement.appendChild(o);" +
	"}," +
	"hide: function() {" +
	"var o = document.getElementById('__verdana_prompt');" +
	"if (o && o.parentNode) o.parentNode.removeChild(o);" +
	"}" +
	"};" +
	"})()"
