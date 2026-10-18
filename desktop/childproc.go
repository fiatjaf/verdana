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
	"sync/atomic"

	"verdana/backend"
)

// A napp window is its own process, whichever kind it is: a webview shell
// (./child) serving the napp's page, or a canvas shell (./wasmchild) running
// the napp's module. Both are spoken to over stdin/stdout with one JSON wire
// message per line, so this file is that pipe for either of them and the
// backend never learns the difference.

type shellTransport struct {
	instance string
	cmd      *exec.Cmd

	// retired is set on a picker closed to make room for the napp the user
	// picked in it: that napp has its instance id now, so the picker going
	// away must not close it.
	retired atomic.Bool

	mu  sync.Mutex
	enc *json.Encoder
}

var (
	shellsMu sync.Mutex
	shells   []*shellTransport
)

// startChild spawns the webview process for a web napp. It returns as soon
// as the process is up: the napp's own readiness is observed later, when it
// registers its actions.
func startChild(spec backend.WindowSpec) (backend.Transport, error) {
	return spawnShell(childExePath(), spec.Instance, nappEnv(spec))
}

// startWasmChild spawns the canvas process for a wasm napp. Same contract as
// startChild, same wire: the difference is all inside the shell.
func startWasmChild(spec backend.WindowSpec) (backend.Transport, error) {
	return spawnShell(wasmChildExePath(), spec.Instance, nappEnv(spec))
}

// startPicker spawns a canvas process with no napp in it yet, to show a
// picker: the canvas shell is the one that can become the napp picked in it,
// when that napp is a wasm one (see become).
func startPicker(spec backend.PickerSpec) (backend.Transport, error) {
	return spawnShell(wasmChildExePath(), spec.Instance, []string{
		"VERDANA_PICKER=1",
		"VERDANA_INSTANCE_ID=" + spec.Instance,
		"VERDANA_WINDOW_WIDTH=" + strconv.Itoa(pickerWidth),
		"VERDANA_WINDOW_HEIGHT=" + strconv.Itoa(pickerHeight),
		"VERDANA_THEME=" + spec.Theme,
		"VERDANA_THEME_VARS=" + spec.ThemeVars,
	})
}

// pickerWidth and pickerHeight size a picker window: room for a few rows
// of icons, and no more than a small window's worth of screen.
const (
	pickerWidth  = 560
	pickerHeight = 520
)

// become lands the napp the user picked in the picker that asked: the
// canvas shell takes the napp's environment and runs it in the window it
// has, which keeps its pipe, and so this transport, and its instance id.
func (ct *shellTransport) become(spec backend.WindowSpec) backend.Transport {
	env := make(map[string]string)
	for _, kv := range nappEnv(spec) {
		k, v, _ := strings.Cut(kv, "=")
		env[k] = v
	}
	raw, _ := json.Marshal(env)
	ct.Send(backend.WireMsg{T: "become", Params: string(raw)})
	log.Info().Str("napp", spec.NappID).Str("instance", spec.Instance).
		Msg("picker window becomes the napp")
	return ct
}

// retire closes a picker to make room for a napp it cannot become.
func (ct *shellTransport) retire() {
	ct.retired.Store(true)
	ct.Close()
}

// nappEnv is what a napp shell is told about the napp it runs.
func nappEnv(spec backend.WindowSpec) []string {
	env := []string{
		"VERDANA_NAPP_ID=" + spec.NappID,
		"VERDANA_NAPP_DIR=" + spec.Dir,
		"VERDANA_NAPP_URL=" + spec.URL,
		"VERDANA_NAPP_NAME=" + spec.Name,
		"VERDANA_NAPP_DESC=" + spec.Description,
		"VERDANA_NAPP_STORAGE_FILE=" + backend.StorageFile(spec.NappID),
		"VERDANA_INSTANCE_ID=" + spec.Instance,
		"VERDANA_WINDOW_WIDTH=" + strconv.Itoa(spec.Width),
		"VERDANA_WINDOW_HEIGHT=" + strconv.Itoa(spec.Height),
		"VERDANA_NAPP_REQUIRES=" + strings.Join(spec.Requires, ","),
		"VERDANA_THEME=" + spec.Theme,
		"VERDANA_THEME_VARS=" + spec.ThemeVars,
	}
	if spec.Mode == backend.ModeWasm {
		env = append(env, "VERDANA_NAPP_WASM="+spec.Wasm)
	}
	return env
}

// spawnShell starts exe with env on top of the launcher's own environment,
// and wires its stdout to the backend.
func spawnShell(exe, instance string, env []string) (*shellTransport, error) {
	cmd := exec.Command(exe)
	cmd.Env = append(os.Environ(), env...)
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

	ct := &shellTransport{instance: instance, cmd: cmd, enc: json.NewEncoder(stdin)}

	shellsMu.Lock()
	shells = append(shells, ct)
	shellsMu.Unlock()

	log.Info().Str("instance", instance).Str("exe", exe).Int("pid", cmd.Process.Pid).
		Msg("napp window started")

	go readChild(ct, stdout)
	return ct, nil
}

func (ct *shellTransport) Send(m backend.WireMsg) {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	if err := ct.enc.Encode(m); err != nil {
		log.Debug().Str("instance", ct.instance).Err(err).Msg("could not write to napp shell")
	}
}

func (ct *shellTransport) Close() {
	ct.Send(backend.WireMsg{T: "close"})
}

// readChild hands everything the shell says to the backend, until it exits.
func readChild(ct *shellTransport, stdout io.ReadCloser) {
	dec := json.NewDecoder(stdout)
	for {
		var m backend.WireMsg
		if err := dec.Decode(&m); err != nil {
			log.Debug().Str("instance", ct.instance).Err(err).Msg("napp shell stdout ended")
			break
		}
		backend.HandleMessage(ct.instance, m)
	}

	if !ct.retired.Load() {
		backend.WindowClosed(ct.instance)
	}
	ct.cmd.Wait()

	shellsMu.Lock()
	for i, c := range shells {
		if c == ct {
			shells = append(shells[:i], shells[i+1:]...)
			break
		}
	}
	shellsMu.Unlock()
}

// killAllShells is the last resort when the launcher itself is going away:
// the windows were asked to close first.
func killAllShells() {
	shellsMu.Lock()
	snapshot := append([]*shellTransport(nil), shells...)
	shellsMu.Unlock()
	for _, ct := range snapshot {
		if ct.cmd != nil && ct.cmd.Process != nil {
			ct.cmd.Process.Kill()
		}
	}
	log.Info().Int("count", len(snapshot)).Msg("killed all napp shells")
}

// shellExePath finds one of the two shells: the embedded copy when this is a
// release build, otherwise the one next to the launcher (or in ./child) that
// `just run` just built.
func shellExePath(name string) string {
	path, err := extractShell(name)
	if err == nil && path != "" {
		return path
	}
	exe, err := os.Executable()
	if err == nil {
		candidate := filepath.Join(filepath.Dir(exe), name, name)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	try := filepath.Join(name, name)
	if _, err := os.Stat(try); err == nil {
		return try
	}
	return try
}

func childExePath() string     { return shellExePath("child") }
func wasmChildExePath() string { return shellExePath("wasmchild") }
