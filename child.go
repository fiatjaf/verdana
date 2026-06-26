package main

import (
	"encoding/json"
	"errors"
	"os"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/abemedia/go-webview"
	_ "github.com/abemedia/go-webview/embedded"
)

var (
	nappDir         string
	childOutMu      sync.Mutex
	childEnc        *json.Encoder
	childPendingMu  sync.Mutex
	childPending    = make(map[int]chan wireMsg)
	childReqSerial  atomic.Int64
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
