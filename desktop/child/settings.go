package main

import (
	"crypto/rand"
	"encoding/hex"
	"net"
	"net/http"
	"os"

	"github.com/abemedia/go-webview"

	nappbridge "verdana/backend/webview"
)

// A settings window is the launcher's own page, for one napp: no napp code
// runs in it. It still gets a per-window token like a napplet window, so
// the one binding it has answers only the page's top frame.

func runSettings(w webview.WebView) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		log.Fatal().Err(err).Msg("no randomness for the settings window token")
	}
	nappletToken = hex.EncodeToString(raw[:])

	_ = w.Bind("__verdana_settings_rpc", nappletRPC)
	w.Init("(function(){if (window !== window.top) return;" +
		"var t = " + jsString(nappletToken) + ";" +
		"window.__verdanaSettingsRPC = function(m, p){ return window.__verdana_settings_rpc(t, m, p) };" +
		themeInitScript(meta.Theme, meta.ThemeVars) +
		"})();")
	w.Init(nappbridge.SettingsJS())

	url := startSettingsServer()
	if url == "" {
		log.Fatal().Msg("could not start the settings page")
	}
	w.Navigate(url)

	go reader(w)

	w.Run()
	w.Destroy()
	os.Exit(0)
}

// startSettingsServer serves the settings page, and nothing else, on loopback.
func startSettingsServer() string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Error().Err(err).Msg("failed to listen for the settings page")
		return ""
	}
	page := []byte(nappbridge.SettingsHTML())
	handler := http.HandlerFunc(func(wr http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(wr, r)
			return
		}
		wr.Header().Set("Content-Type", "text/html; charset=utf-8")
		wr.Header().Set("Cache-Control", "no-store")
		// everything the page needs is inline or injected: no network,
		// no frames, no navigation away
		wr.Header().Set("Content-Security-Policy",
			"default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; "+
				"img-src data:; frame-src 'none'; base-uri 'none'; form-action 'none'")
		_, _ = wr.Write(page)
	})
	go http.Serve(ln, handler)
	return "http://" + ln.Addr().String() + "/"
}
