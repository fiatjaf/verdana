package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func webviewServer() {
	for req := range openReqCh {
		go func(req openReq) {
			ci, err := startChild(req)
			if req.reply != nil {
				req.reply <- launchResult{ci: ci, err: err}
			}
			if err != nil {
				log.Error().Err(err).Str("napp", req.napp.ID).Msg("could not start napp")
				return
			}
			// blocks until the child's stdout closes (i.e. the window is gone)
			readChild(ci)
		}(req)
	}
}

// startChild spawns the webview process for a napp and registers it as an
// open instance. It returns as soon as the process is up: the napp's own
// readiness is observed later, when it registers its actions.
func startChild(req openReq) (*childInfo, error) {
	instance := nextInstanceID(req.napp)

	// the napp starts already themed: the child injects these before the
	// page loads, so there is no flash of the wrong colors
	themeName, themeVars := themeWire()

	childExe := childExePath()
	cmd := exec.Command(childExe)
	cmd.Env = append(os.Environ(),
		"VERDANA_NAPP_ID="+req.napp.ID,
		"VERDANA_NAPP_DIR="+req.dir,
		"VERDANA_NAPP_NAME="+req.napp.Name,
		"VERDANA_NAPP_DESC="+req.napp.Description,
		"VERDANA_INSTANCE_ID="+instance,
		"VERDANA_NAPP_REQUIRES="+strings.Join(req.napp.Requires, ","),
		"VERDANA_THEME="+themeName,
		"VERDANA_THEME_VARS="+themeVars,
	)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	ci := &childInfo{
		instance:   instance,
		napp:       req.napp,
		cmd:        cmd,
		enc:        json.NewEncoder(stdin),
		subs:       make(map[int]context.CancelFunc),
		actions:    make(map[string]int),
		changed:    make(chan struct{}),
		dispatches: make(map[int]chan wireMsg),
		gone:       make(chan struct{}),
		stdout:     stdout,
	}
	registerInstance(ci)

	log.Info().Str("napp", req.napp.ID).Str("instance", instance).
		Int("pid", cmd.Process.Pid).Msg("napp window started")
	return ci, nil
}

// readChild consumes the child's messages until it exits.
func readChild(ci *childInfo) {
	dec := json.NewDecoder(ci.stdout)
	for {
		var m wireMsg
		if err := dec.Decode(&m); err != nil {
			log.Debug().Str("instance", ci.instance).Err(err).Msg("child stdout ended")
			break
		}
		if m.T == "rpc" {
			m := m
			go handleChildRPC(ci, m)
		}
	}

	cleanupChild(ci)
	ci.cmd.Wait()
	log.Info().Str("instance", ci.instance).Str("napp", ci.napp.ID).Msg("napp window closed")
}

func handleChildRPC(ci *childInfo, m wireMsg) {
	result, err := bridgeRPC(ci)(m.Method, m.Params)
	resp := wireMsg{T: "resp", ID: m.ID}
	if err != nil {
		log.Warn().Str("method", m.Method).Err(err).Msg("child rpc error")
		resp.Error = err.Error()
	} else if raw, mErr := json.Marshal(result); mErr != nil {
		log.Error().Str("method", m.Method).Err(mErr).Msg("child rpc marshal error")
		resp.Error = mErr.Error()
	} else {
		resp.Result = raw
	}
	ci.send(resp)
}

func (ci *childInfo) send(m wireMsg) {
	ci.encMu.Lock()
	defer ci.encMu.Unlock()
	if err := ci.enc.Encode(m); err != nil {
		log.Debug().Str("instance", ci.instance).Err(err).Msg("could not write to child")
	}
}

func (ci *childInfo) eval(code string) {
	ci.send(wireMsg{T: "eval", Code: code})
}

func childExePath() string {
	path, err := extractChild()
	if err == nil && path != "" {
		return path
	}
	exe, err := os.Executable()
	if err == nil {
		dir := filepath.Dir(exe)
		candidate := filepath.Join(dir, "child", "child")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	try := "./child/child"
	if _, err := os.Stat(try); err == nil {
		return try
	}
	return "child/child"
}

func cleanupChild(ci *childInfo) {
	ci.subMu.Lock()
	for _, cancel := range ci.subs {
		cancel()
	}
	ci.subs = make(map[int]context.CancelFunc)
	ci.subMu.Unlock()

	// unblock anything still waiting on this window
	select {
	case <-ci.gone:
	default:
		close(ci.gone)
	}

	mu.Lock()
	for i, c := range children {
		if c == ci {
			children = append(children[:i], children[i+1:]...)
			break
		}
	}
	mu.Unlock()
}
