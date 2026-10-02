//go:build !windows

package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"verdana/backend"
)

// mpvPlayer drives mpv over its JSON IPC socket: properties it observes come
// back as events whenever they change, and commands go the same way.
type mpvPlayer struct {
	proc    *playerProc
	onState func(backend.MediaState)

	mu    sync.Mutex
	conn  net.Conn
	state mpvState
}

func startMpv(path string, req backend.MediaRequest, onState func(backend.MediaState)) (backend.MediaPlayer, error) {
	dir, err := os.MkdirTemp("", "verdana-mpv-")
	if err != nil {
		return nil, err
	}
	sock := filepath.Join(dir, "ipc")
	args := []string{
		"--no-terminal", "--force-window=yes", "--idle=no", "--keep-open=no",
		// the url was checked, the pages yt-dlp would go on to fetch were not
		"--ytdl=no",
		"--input-ipc-server=" + sock,
	}
	if req.Title != "" {
		// force-media-title is not property-expanded, unlike --title
		args = append(args, "--force-media-title="+req.Title)
	}
	if !req.Autoplay {
		args = append(args, "--pause")
	}
	args = append(args, "--", req.URL)

	p := &mpvPlayer{onState: onState}
	proc, err := startPlayerProc(exec.Command(path, args...), dir, onState)
	if err != nil {
		return nil, err
	}
	p.proc = proc
	go p.connect(sock)
	log.Info().Str("player", "mpv").Msg("started media player")
	return p, nil
}

// connect waits for mpv's socket to appear, then observes what NAP-MEDIA
// reports and reads events until mpv goes away.
func (p *mpvPlayer) connect(sock string) {
	var conn net.Conn
	deadline := time.Now().Add(5 * time.Second)
	for {
		c, err := net.Dial("unix", sock)
		if err == nil {
			conn = c
			break
		}
		if time.Now().After(deadline) {
			log.Warn().Err(err).Msg("mpv IPC socket never came up")
			return
		}
		select {
		case <-p.proc.done:
			return
		case <-time.After(50 * time.Millisecond):
		}
	}
	go func() {
		<-p.proc.done
		conn.Close()
	}()

	p.mu.Lock()
	p.conn = conn
	p.mu.Unlock()
	for i, name := range mpvObserved {
		p.send("observe_property", i+1, name)
	}

	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		p.mu.Lock()
		changed := p.state.apply(sc.Bytes())
		st := p.state.media()
		p.mu.Unlock()
		if changed {
			p.onState(st)
		}
	}
}

func (p *mpvPlayer) send(cmd ...any) error {
	raw, err := json.Marshal(map[string]any{"command": cmd})
	if err != nil {
		return err
	}
	p.mu.Lock()
	conn := p.conn
	p.mu.Unlock()
	if conn == nil {
		return errors.New("mpv is not ready")
	}
	conn.SetWriteDeadline(time.Now().Add(time.Second))
	_, err = conn.Write(append(raw, '\n'))
	return err
}

func (p *mpvPlayer) Play() error  { return p.send("set_property", "pause", false) }
func (p *mpvPlayer) Pause() error { return p.send("set_property", "pause", true) }
func (p *mpvPlayer) Seek(sec float64) error {
	return p.send("seek", sec, "absolute")
}
func (p *mpvPlayer) SetVolume(v float64) error {
	return p.send("set_property", "volume", v*100)
}
func (p *mpvPlayer) SetTitle(title string) error {
	return p.send("set_property", "force-media-title", title)
}

func (p *mpvPlayer) Stop() error {
	err := p.send("quit")
	p.proc.killAfter(2 * time.Second)
	if err != nil {
		p.proc.cmd.Process.Kill()
	}
	return nil
}

// mpvObserved are the properties mpvState follows; observe ids are their
// index + 1.
var mpvObserved = []string{"pause", "time-pos", "duration", "volume", "paused-for-cache", "eof-reached"}

// mpvState is what mpv's property-change events have said so far.
type mpvState struct {
	pause, cache, eof bool
	pos, dur, vol     *float64
}

// apply takes one line from mpv's socket and says whether it changed
// anything NAP-MEDIA reports.
func (s *mpvState) apply(line []byte) bool {
	var ev struct {
		Event string          `json:"event"`
		Name  string          `json:"name"`
		Data  json.RawMessage `json:"data"`
	}
	if json.Unmarshal(line, &ev) != nil || ev.Event != "property-change" {
		return false
	}
	var b bool
	var f *float64
	switch ev.Name {
	case "pause", "paused-for-cache", "eof-reached":
		json.Unmarshal(ev.Data, &b)
	case "time-pos", "duration", "volume":
		// null when mpv doesn't know (yet)
		json.Unmarshal(ev.Data, &f)
	}
	switch ev.Name {
	case "pause":
		s.pause = b
	case "paused-for-cache":
		s.cache = b
	case "eof-reached":
		s.eof = b
	case "time-pos":
		s.pos = f
	case "duration":
		s.dur = f
	case "volume":
		if f != nil {
			f = fptr(min(*f/100, 1))
		}
		s.vol = f
	default:
		return false
	}
	return true
}

func (s *mpvState) media() backend.MediaState {
	st := backend.MediaState{Position: s.pos, Duration: s.dur, Volume: s.vol}
	switch {
	case s.eof:
		st.Status = "stopped"
	case s.pause:
		st.Status = "paused"
	case s.cache || s.pos == nil:
		st.Status = "buffering"
	default:
		st.Status = "playing"
	}
	return st
}
