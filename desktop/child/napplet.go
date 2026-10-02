package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"

	"github.com/abemedia/go-webview"

	nappbridge "verdana/backend/webview"
)

// A napplet window loads no napp files and gets no bridge.js. Its main frame
// is the launcher's host page, which puts the napplet in a sandboxed srcdoc
// iframe and carries NAP envelopes to the backend (nap.boot / nap.msg).
//
// The webview's bindings are the one thing the sandbox does not cover on its
// own: depending on the engine, a bound function (or the message handler
// behind it) can be reachable from child frames too. So in a napplet window
// both bindings demand a per-window secret that only the host page's
// top-frame init script knows. A frame that calls them without it is ignored.

var nappletToken string

func runNapplet(w webview.WebView) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		log.Fatal().Err(err).Msg("no randomness for the napplet window token")
	}
	nappletToken = hex.EncodeToString(raw[:])

	_ = w.Bind("__verdana_napplet_rpc", nappletRPC)
	_ = w.Bind("__verdana_napplet_answer", nappletAnswer)

	// Everything below runs in the top frame only. The token lives in this
	// closure and in the two wrappers it defines; the napplet's frame cannot
	// read another frame's script source, and an opaque-origin frame cannot
	// touch this window's globals.
	w.Init("(function(){if (window !== window.top) return;" +
		"var t = " + jsString(nappletToken) + ";" +
		"window.__verdanaNappletRPC = function(m, p){ return window.__verdana_napplet_rpc(t, m, p) };" +
		// the prompt overlay (promptLibScript) answers through this name
		"window.__verdana_prompt_answer = function(id, ok, index, scope){" +
		"return window.__verdana_napplet_answer(t, id, ok, index, scope) };" +
		"window.name = " + jsString(meta.Instance) + ";" +
		themeInitScript(meta.Theme, meta.ThemeVars) +
		"})();")
	w.Init(nappbridge.NappletHostJS())

	url := startNappletHostServer()
	if url == "" {
		log.Fatal().Msg("could not start the napplet host page")
	}
	w.Navigate(url)

	go reader(w)

	w.Run()
	w.Destroy()
	os.Exit(0)
}

func validToken(token string) bool {
	return nappletToken != "" &&
		subtle.ConstantTimeCompare([]byte(token), []byte(nappletToken)) == 1
}

func nappletRPC(token, method, params string) string {
	if !validToken(token) {
		log.Warn().Str("method", method).Msg("napplet window: rpc without the window token, ignored")
		wrapped, _ := json.Marshal(map[string]string{"__bridge_error": errors.New("forbidden").Error()})
		return string(wrapped)
	}
	return rpcBound(method, params)
}

func nappletAnswer(token string, id int, ok bool, index int, scope string) {
	if !validToken(token) {
		log.Warn().Int("prompt", id).Msg("napplet window: prompt answer without the window token, ignored")
		return
	}
	promptAnswer(id, ok, index, scope)
}

// startNappletHostServer serves the host page, and nothing else, on loopback.
// The napplet's own bytes never come from here: they arrive over nap.boot as
// a srcdoc, after the backend verified them against the event's hash.
func startNappletHostServer() string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Error().Err(err).Msg("failed to listen for the napplet host page")
		return ""
	}
	page := []byte(nappbridge.NappletHostHTML())
	handler := http.HandlerFunc(func(wr http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(wr, r)
			return
		}
		wr.Header().Set("Content-Type", "text/html; charset=utf-8")
		wr.Header().Set("Cache-Control", "no-store")
		// the srcdoc frame inherits this page's policy on top of its own, so
		// this must stay loose on script/style/img; it only pins navigation
		wr.Header().Set("Content-Security-Policy", "navigate-to 'self'")
		_, _ = wr.Write(page)
	})
	go http.Serve(ln, handler)
	return "http://" + ln.Addr().String() + "/"
}
