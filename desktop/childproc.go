package main

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"verdana/backend"
)

// On the desktop a napp window is its own process: a small webview shell
// (./child) that we talk to over its stdin/stdout with one JSON wire message
// per line. This file is that pipe — the backend never learns about it.

type childTransport struct {
	instance string
	cmd      *exec.Cmd
	// settings is a napp's settings window rather than a napp: instance is
	// then its backend.SettingsSpec.Window
	settings bool

	mu  sync.Mutex
	enc *json.Encoder
}

var (
	childrenMu sync.Mutex
	children   []*childTransport
)

// startChild spawns the webview process for a napp. It returns as soon as the
// process is up: the napp's own readiness is observed later, when it registers
// its actions.
func startChild(spec backend.WindowSpec) (backend.Transport, error) {
	cmd := exec.Command(childExePath())
	cmd.Env = append(os.Environ(),
		"VERDANA_NAPP_ID="+spec.NappID,
		"VERDANA_NAPP_DIR="+spec.Dir,
		"VERDANA_NAPP_URL="+spec.URL,
		"VERDANA_NAPP_NAME="+spec.Name,
		"VERDANA_NAPP_DESC="+spec.Description,
		"VERDANA_NAPP_STORAGE_FILE="+backend.StorageFile(spec.NappID),
		"VERDANA_INSTANCE_ID="+spec.Instance,
		"VERDANA_WINDOW_NUMBER="+strconv.Itoa(spec.Number),
		"VERDANA_WINDOW_WIDTH="+strconv.Itoa(spec.Width),
		"VERDANA_WINDOW_HEIGHT="+strconv.Itoa(spec.Height),
		"VERDANA_NAPP_REQUIRES="+strings.Join(spec.Requires, ","),
		"VERDANA_NAPP_FORMAT="+spec.Format,
		"VERDANA_THEME="+spec.Theme,
		"VERDANA_THEME_VARS="+spec.ThemeVars,
	)
	ct, err := spawnChild(cmd, spec.Instance, false)
	if err != nil {
		return nil, err
	}
	log.Info().Str("napp", spec.NappID).Str("instance", spec.Instance).
		Int("pid", cmd.Process.Pid).Msg("napp window started")
	return ct, nil
}

// startSettingsChild spawns the webview process for a napp's settings window:
// the same child, in its settings mode, serving the launcher's settings page.
func startSettingsChild(spec backend.SettingsSpec) (backend.Transport, error) {
	cmd := exec.Command(childExePath())
	cmd.Env = append(os.Environ(),
		"VERDANA_WINDOW_KIND=settings",
		"VERDANA_NAPP_ID="+spec.NappID,
		"VERDANA_NAPP_NAME="+spec.Name,
		"VERDANA_INSTANCE_ID="+spec.Window,
		"VERDANA_WINDOW_WIDTH=560",
		"VERDANA_WINDOW_HEIGHT=640",
		"VERDANA_THEME="+spec.Theme,
		"VERDANA_THEME_VARS="+spec.ThemeVars,
	)
	ct, err := spawnChild(cmd, spec.Window, true)
	if err != nil {
		return nil, err
	}
	log.Info().Str("napp", spec.NappID).Str("window", spec.Window).
		Int("pid", cmd.Process.Pid).Msg("settings window started")
	return ct, nil
}

func spawnChild(cmd *exec.Cmd, instance string, settings bool) (*childTransport, error) {
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

	ct := &childTransport{instance: instance, cmd: cmd, settings: settings, enc: json.NewEncoder(stdin)}

	childrenMu.Lock()
	children = append(children, ct)
	childrenMu.Unlock()

	go readChild(ct, stdout)
	return ct, nil
}

func (ct *childTransport) Send(m backend.WireMsg) {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	if err := ct.enc.Encode(m); err != nil {
		log.Debug().Str("instance", ct.instance).Err(err).Msg("could not write to child")
	}
}

func (ct *childTransport) Close() {
	ct.Send(backend.WireMsg{T: "close"})
}

// The child webview library has no cross-platform raise operation. Dispatch
// still reaches the existing window; compositors that prevent focus stealing
// are allowed to leave this hint unfulfilled.
func (ct *childTransport) Focus() {}

// readChild hands everything the child says to the backend, until it exits.
func readChild(ct *childTransport, stdout io.ReadCloser) {
	dec := json.NewDecoder(stdout)
	for {
		var m backend.WireMsg
		if err := dec.Decode(&m); err != nil {
			log.Debug().Str("instance", ct.instance).Err(err).Msg("child stdout ended")
			break
		}
		if ct.settings {
			backend.HandleSettingsMessage(ct.instance, m)
		} else {
			backend.HandleMessage(ct.instance, m)
		}
	}

	if ct.settings {
		backend.SettingsClosed(ct.instance)
	} else {
		backend.WindowClosed(ct.instance)
	}
	ct.cmd.Wait()

	childrenMu.Lock()
	for i, c := range children {
		if c == ct {
			children = append(children[:i], children[i+1:]...)
			break
		}
	}
	childrenMu.Unlock()
}

// killAllChildren is the last resort when the launcher itself is going away:
// the windows were asked to close first.
func killAllChildren() {
	childrenMu.Lock()
	snapshot := append([]*childTransport(nil), children...)
	childrenMu.Unlock()
	for _, ct := range snapshot {
		if ct.cmd != nil && ct.cmd.Process != nil {
			ct.cmd.Process.Kill()
		}
	}
	log.Info().Int("count", len(snapshot)).Msg("killed all child processes")
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
