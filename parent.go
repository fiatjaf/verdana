package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
)

func webviewServer() {
	for req := range openReqCh {
		go launchChild(req)
	}
}

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
			break
		}
		if m.T == "rpc" {
			m := m
			go handleChildRPC(ci, m)
		}
	}

	cleanupChild(ci)
	cmd.Wait()
}

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

func (ci *childInfo) eval(code string) {
	ci.send(wireMsg{T: "eval", Code: code})
}

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
