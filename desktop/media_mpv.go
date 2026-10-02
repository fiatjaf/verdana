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
// back as events whenever they change, and commands go the same way. One mpv
// plays for one session at a time; a new session's media is loaded into the
// same window (loadfile replace), and from then on its events go to that
// session, the one before it hearing nothing more.
type mpvPlayer struct {
	proc *playerProc

	mu    sync.Mutex
	conn  net.Conn
	state mpvState
	// owner is the session mpv plays for, nil once retired
	owner *mpvSession
}

// mpvSession is one session's MediaPlayer: it steers mpv only while it is
// the owner.
type mpvSession struct {
	p       *mpvPlayer
	onState func(backend.MediaState)
}

func startMpv(path string, req backend.MediaRequest, onState func(backend.MediaState)) (sharedPlayer, backend.MediaPlayer, error) {
	dir, err := os.MkdirTemp("", "verdana-mpv-")
	if err != nil {
		return nil, nil, err
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

	p := &mpvPlayer{}
	s := &mpvSession{p: p, onState: onState}
	p.owner = s
	proc, err := startPlayerProc(exec.Command(path, args...), dir, p.emit)
	if err != nil {
		return nil, nil, err
	}
	p.proc = proc
	go p.connect(sock)
	log.Info().Str("player", "mpv").Msg("started media player")
	return p, s, nil
}

// emit hands a state to the session mpv plays for.
func (p *mpvPlayer) emit(st backend.MediaState) {
	p.mu.Lock()
	owner := p.owner
	p.mu.Unlock()
	if owner != nil {
		owner.onState(st)
	}
}

func (p *mpvPlayer) replace(req backend.MediaRequest, onState func(backend.MediaState)) backend.MediaPlayer {
	select {
	case <-p.proc.done:
		return nil
	default:
	}
	s := &mpvSession{p: p, onState: onState}
	p.mu.Lock()
	if p.conn == nil {
		// still starting: it gets closed and a new one started
		p.mu.Unlock()
		return nil
	}
	p.owner = s
	// pause and volume carry over to the next file; the rest is the old
	// file's until mpv reports on the new one
	p.state.pos, p.state.dur, p.state.cache, p.state.eof = nil, nil, false, false
	st := p.state.media()
	p.mu.Unlock()

	if p.send("set_property", "pause", !req.Autoplay) != nil ||
		p.send("set_property", "force-media-title", req.Title) != nil ||
		p.send("loadfile", req.URL, "replace") != nil {
		return nil
	}
	go s.onState(st)
	log.Info().Str("player", "mpv").Msg("replaced the media playing")
	return s
}

func (p *mpvPlayer) retire() {
	p.mu.Lock()
	p.owner = nil
	p.mu.Unlock()
	p.quit()
}

func (p *mpvPlayer) quit() {
	err := p.send("quit")
	p.proc.killAfter(2 * time.Second)
	if err != nil {
		p.proc.cmd.Process.Kill()
	}
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
		owner := p.owner
		p.mu.Unlock()
		if changed && owner != nil {
			owner.onState(st)
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

// send passes a command on while this session owns mpv, and drops it once
// another session's media replaced this one's.
func (s *mpvSession) send(cmd ...any) error {
	s.p.mu.Lock()
	owns := s.p.owner == s
	s.p.mu.Unlock()
	if !owns {
		return nil
	}
	return s.p.send(cmd...)
}

func (s *mpvSession) Play() error  { return s.send("set_property", "pause", false) }
func (s *mpvSession) Pause() error { return s.send("set_property", "pause", true) }
func (s *mpvSession) Seek(sec float64) error {
	return s.send("seek", sec, "absolute")
}
func (s *mpvSession) SetVolume(v float64) error {
	return s.send("set_property", "volume", v*100)
}
func (s *mpvSession) SetTitle(title string) error {
	return s.send("set_property", "force-media-title", title)
}

func (s *mpvSession) Stop() error {
	s.p.mu.Lock()
	owns := s.p.owner == s
	s.p.mu.Unlock()
	if owns {
		s.p.quit()
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
