package main

import (
	"errors"
	"os"
	"os/exec"
	"runtime"
	"time"

	"verdana/backend"
)

// NAP-MEDIA shell-owned playback goes to the media player the user already
// has: mpv when it is installed (its JSON IPC reports state as it changes),
// VLC otherwise (its remote control interface is polled). The player's own
// window is the playback UI; the napplet gets the state and may steer it.
// The OS-specific halves (unix sockets, or none on Windows) live in
// media_<player>.go and media_windows.go.

// MediaPlay starts the first player found.
func (gioHost) MediaPlay(req backend.MediaRequest, onState func(backend.MediaState)) (backend.MediaPlayer, error) {
	if path, err := exec.LookPath("mpv"); err == nil {
		return startMpv(path, req, onState)
	}
	if path := findVLC(); path != "" {
		return startVLC(path, req, onState)
	}
	return nil, errors.New("no media player installed (mpv or vlc)")
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
