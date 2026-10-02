//go:build !windows

package main

import (
	"bufio"
	"errors"
	"fmt"
	"math"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"verdana/backend"
)

// vlcPlayer drives VLC over its old remote control interface (oldrc: VLC
// 3's "rc" is a Lua console that ignores --rc-unix). VLC announces play,
// pause and volume changes on its own; position and length are polled.
//
// VLC refuses most commands while paused ("Type 'pause' to continue"), and
// its pause toggles, so a seek or volume change made while paused is lost.
//
// VLC can't be handed new media reliably over oldrc, so a new session's
// media closes this VLC and starts another.
type vlcPlayer struct {
	proc *playerProc
	gate stateGate

	mu    sync.Mutex
	conn  net.Conn
	state vlcState
}

func startVLC(path string, req backend.MediaRequest, onState func(backend.MediaState)) (sharedPlayer, backend.MediaPlayer, error) {
	dir, err := os.MkdirTemp("", "verdana-vlc-")
	if err != nil {
		return nil, nil, err
	}
	sock := filepath.Join(dir, "rc")
	args := []string{
		"--extraintf", "oldrc", "--rc-unix", sock, "--rc-fake-tty",
		"--play-and-exit", "--no-one-instance", "--no-playlist-enqueue",
	}
	if !req.Autoplay {
		args = append(args, "--start-paused")
	}
	args = append(args, "--", req.URL)

	p := &vlcPlayer{gate: stateGate{onState: onState}}
	proc, err := startPlayerProc(exec.Command(path, args...), dir, p.gate.emit)
	if err != nil {
		return nil, nil, err
	}
	p.proc = proc
	go p.connect(sock)
	log.Info().Str("player", "vlc").Msg("started media player")
	return p, p, nil
}

func (p *vlcPlayer) replace(backend.MediaRequest, func(backend.MediaState)) backend.MediaPlayer {
	return nil
}

func (p *vlcPlayer) retire() {
	p.gate.off.Store(true)
	p.Stop()
}

func (p *vlcPlayer) connect(sock string) {
	var conn net.Conn
	deadline := time.Now().Add(10 * time.Second)
	for {
		c, err := net.Dial("unix", sock)
		if err == nil {
			conn = c
			break
		}
		if time.Now().After(deadline) {
			log.Warn().Err(err).Msg("VLC rc socket never came up")
			return
		}
		select {
		case <-p.proc.done:
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
	p.mu.Lock()
	p.conn = conn
	p.mu.Unlock()

	go func() {
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		defer conn.Close()
		for {
			p.poll()
			select {
			case <-p.proc.done:
				return
			case <-tick.C:
			}
		}
	}()

	sc := bufio.NewScanner(conn)
	for sc.Scan() {
		p.mu.Lock()
		changed := p.state.apply(sc.Text())
		st := p.state.media()
		p.mu.Unlock()
		if changed {
			p.gate.emit(st)
		}
	}
}

// poll asks for the two numbers VLC doesn't announce. The answers are bare
// numbers, told apart only by the order they were asked in.
func (p *vlcPlayer) poll() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, q := range []string{"get_time", "get_length"} {
		if p.writeLocked(q) == nil {
			p.state.pending = append(p.state.pending, q)
		}
	}
}

func (p *vlcPlayer) writeLocked(cmd string) error {
	if p.conn == nil {
		return errors.New("VLC is not ready")
	}
	p.conn.SetWriteDeadline(time.Now().Add(time.Second))
	_, err := p.conn.Write([]byte(cmd + "\n"))
	return err
}

func (p *vlcPlayer) send(cmd string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.writeLocked(cmd)
}

// togglePauseUnless sends VLC's pause toggle unless it already is in the
// wanted state.
func (p *vlcPlayer) togglePauseUnless(paused bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state.paused == paused {
		return nil
	}
	return p.writeLocked("pause")
}

func (p *vlcPlayer) Play() error  { return p.togglePauseUnless(false) }
func (p *vlcPlayer) Pause() error { return p.togglePauseUnless(true) }
func (p *vlcPlayer) Seek(sec float64) error {
	return p.send(fmt.Sprintf("seek %d", int(math.Round(sec))))
}
func (p *vlcPlayer) SetVolume(v float64) error {
	return p.send(fmt.Sprintf("volume %d", int(math.Round(v*256))))
}
func (p *vlcPlayer) SetTitle(string) error { return nil }

func (p *vlcPlayer) Stop() error {
	err := p.send("quit")
	p.proc.killAfter(2 * time.Second)
	if err != nil {
		p.proc.cmd.Process.Kill()
	}
	return nil
}

// vlcStatusChange is VLC announcing a state: "( play state: 3 )" playing,
// "( pause state: 4 )" paused, "( pause state: 3 )" resumed, "( play state:
// 2 ): Play" resuming, "( play state: 4 ): End" and "( stop state: 5 )"
// done. The number alone doesn't say it: 4 is paused or ended by its name.
var vlcStatusChange = regexp.MustCompile(`\( (\w+) state: (\d+) \)`)

var vlcVolumeChange = regexp.MustCompile(`\( audio volume: (\d+) \)`)

// vlcState is what VLC's rc output has said so far.
type vlcState struct {
	started, paused, stopped bool
	pos, dur, vol            *float64
	// pending are the polls whose answers haven't come yet, oldest first
	pending []string
}

// apply takes one line VLC wrote and says whether it changed anything
// NAP-MEDIA reports.
func (s *vlcState) apply(line string) bool {
	line = strings.TrimSpace(line)
	for strings.HasPrefix(line, ">") {
		line = strings.TrimSpace(strings.TrimPrefix(line, ">"))
	}
	if m := vlcStatusChange.FindStringSubmatch(line); m != nil {
		switch {
		case m[1] == "stop", m[1] == "play" && m[2] == "4":
			s.stopped = true
		case m[1] == "pause" && m[2] == "4":
			s.started, s.paused, s.stopped = true, true, false
		case m[2] == "2", m[2] == "3":
			s.started, s.paused, s.stopped = true, false, false
		default:
			return false
		}
		return true
	}
	if m := vlcVolumeChange.FindStringSubmatch(line); m != nil {
		n, _ := strconv.Atoi(m[1])
		s.vol = fptr(min(float64(n)/256, 1))
		return true
	}
	n, err := strconv.Atoi(line)
	if err != nil || len(s.pending) == 0 {
		return false
	}
	q := s.pending[0]
	s.pending = s.pending[1:]
	v := fptr(float64(n))
	switch q {
	case "get_time":
		if floatSame(s.pos, v) {
			return false
		}
		s.pos = v
	case "get_length":
		if n <= 0 {
			// a live stream, or not known yet
			v = nil
		}
		if floatSame(s.dur, v) {
			return false
		}
		s.dur = v
	}
	return true
}

func (s *vlcState) media() backend.MediaState {
	st := backend.MediaState{Position: s.pos, Duration: s.dur, Volume: s.vol}
	switch {
	case s.stopped:
		st.Status = "stopped"
	case !s.started:
		st.Status = "buffering"
	case s.paused:
		st.Status = "paused"
	default:
		st.Status = "playing"
	}
	return st
}

func floatSame(a, b *float64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
