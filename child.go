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
	"strings"
	"sync"
	"sync/atomic"

	"github.com/abemedia/go-webview"
	_ "github.com/abemedia/go-webview/embedded"
	"github.com/rs/zerolog/log"
)

var (
	nappDir        string
	childOutMu     sync.Mutex
	childEnc       *json.Encoder
	childPendingMu sync.Mutex
	childPending   = make(map[int]chan wireMsg)
	childReqSerial atomic.Int64
)

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

	log.Info().Str("napp", nappID).Str("name", napp.Name).Msg("child process started")
	childEnc = json.NewEncoder(os.Stdout)

	w := webview.New(false)
	w.SetTitle(napp.Name)
	w.SetSize(600, 450, webview.HintNone)
	_ = w.Bind("__bridge_rpc", childRPCBound)
	w.Init(bridgeJS) // must run before Navigate/SetHtml

	if url := startNappServer(nappDir); url != "" {
		log.Info().Str("napp", nappID).Str("url", url).Msg("serving napp files")
		w.Navigate(url)
	} else {
		log.Debug().Str("napp", nappID).Msg("no napp files, showing placeholder")
		w.SetHtml(nappHTML(napp))
	}

	go childReader(w)

	w.Run()
	w.Destroy()
	os.Exit(0)
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

// childRPCBound is the function bound into the webview. go-webview panics when
// a bound function returns a nil error (it blindly type-asserts the second
// return value to error), so we expose a single-return wrapper: on success it
// returns the raw result, on failure an error envelope the JS bridge unwraps.
func childRPCBound(method string, params string) any {
	result, err := childRPC(method, params)
	if err != nil {
		return map[string]any{"__bridge_error": err.Error()}
	}
	return result
}

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
