package main

import (
	"errors"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"verdana/backend"
)

// NAP-MEDIA shell-owned playback goes to the media player the user already
// has: mpv when it is installed (its JSON IPC reports state as it changes),
// VLC otherwise (its remote control interface is polled). The player's own
// window is the playback UI; the napplet gets the state and may steer it.
// The OS-specific halves (unix sockets, or none on Windows) live in
// media_<player>.go and media_windows.go.

// MediaPlay plays in the player already open when it can take new media
// (mpv over its IPC socket), and otherwise closes it and starts the first
// player found: there is only ever one.
func (gioHost) MediaPlay(req backend.MediaRequest, onState func(backend.MediaState)) (backend.MediaPlayer, error) {
	playing.mu.Lock()
	defer playing.mu.Unlock()
	if playing.cur != nil {
		if mp := playing.cur.replace(req, onState); mp != nil {
			return mp, nil
		}
		playing.cur.retire()
		playing.cur = nil
	}
	var (
		sp  sharedPlayer
		mp  backend.MediaPlayer
		err error
	)
	if path, lerr := exec.LookPath("mpv"); lerr == nil {
		sp, mp, err = startMpv(path, req, onState)
	} else if path := findVLC(); path != "" {
		sp, mp, err = startVLC(path, req, onState)
	} else {
		err = errors.New("no media player installed (mpv or vlc)")
	}
	if err != nil {
		return nil, err
	}
	playing.cur = sp
	return mp, nil
}

// playing is the one player process, which may have exited since.
var playing struct {
	mu  sync.Mutex
	cur sharedPlayer
}

// sharedPlayer is a player process as MediaPlay hands it from one session
// to the next.
type sharedPlayer interface {
	// replace plays req in this player for a new session, retiring the
	// MediaPlayer it was playing for, or returns nil if it can't.
	replace(req backend.MediaRequest, onState func(backend.MediaState)) backend.MediaPlayer
	// retire closes the player without its session hearing of it.
	retire()
}

// stateGate passes a player's reports to its session until it is retired.
type stateGate struct {
	off     atomic.Bool
	onState func(backend.MediaState)
}

func (g *stateGate) emit(st backend.MediaState) {
	if !g.off.Load() {
		g.onState(st)
	}
}

func findVLC() string {
	if path, err := exec.LookPath("vlc"); err == nil {
		return path
	}
	var candidates []string
	switch runtime.GOOS {
	case "darwin":
		candidates = []string{"/Applications/VLC.app/Contents/MacOS/VLC"}
	case "windows":
		candidates = []string{
			os.Getenv("ProgramFiles") + `\VideoLAN\VLC\vlc.exe`,
			os.Getenv("ProgramFiles(x86)") + `\VideoLAN\VLC\vlc.exe`,
		}
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c
		}
	}
	return ""
}

// playerProc is a running player process: done closes once it has exited
// and everything it left behind (its socket dir) is gone.
type playerProc struct {
	cmd  *exec.Cmd
	done chan struct{}
}

// startPlayerProc runs the player and, once it exits, removes dir and
// reports one last "stopped".
func startPlayerProc(cmd *exec.Cmd, dir string, onState func(backend.MediaState)) (*playerProc, error) {
	if err := cmd.Start(); err != nil {
		if dir != "" {
			os.RemoveAll(dir)
		}
		return nil, err
	}
	p := &playerProc{cmd: cmd, done: make(chan struct{})}
	go func() {
		cmd.Wait()
		if dir != "" {
			os.RemoveAll(dir)
		}
		close(p.done)
		onState(backend.MediaState{Status: "stopped"})
	}()
	return p, nil
}

// killAfter makes sure the player is gone within grace of being asked to
// quit, without waiting for it here.
func (p *playerProc) killAfter(grace time.Duration) {
	go func() {
		select {
		case <-p.done:
		case <-time.After(grace):
			p.cmd.Process.Kill()
		}
	}()
}

func fptr(v float64) *float64 { return &v }
