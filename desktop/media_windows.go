package main

import (
	"errors"
	"os/exec"

	"verdana/backend"
)

// On Windows mpv's IPC is a named pipe and VLC's rc a TCP port; neither is
// wired up yet, so the player is only started and stopped: the session
// reports "playing" until the player exits, and ignores everything else.
// Neither can be handed new media, so a new session's closes the old player.

func startMpv(path string, req backend.MediaRequest, onState func(backend.MediaState)) (sharedPlayer, backend.MediaPlayer, error) {
	args := []string{"--force-window=yes", "--idle=no", "--ytdl=no"}
	if req.Title != "" {
		args = append(args, "--force-media-title="+req.Title)
	}
	if !req.Autoplay {
		args = append(args, "--pause")
	}
	return startLaunchOnly(exec.Command(path, append(args, "--", req.URL)...), onState)
}

func startVLC(path string, req backend.MediaRequest, onState func(backend.MediaState)) (sharedPlayer, backend.MediaPlayer, error) {
	args := []string{"--play-and-exit", "--no-one-instance"}
	if !req.Autoplay {
		args = append(args, "--start-paused")
	}
	return startLaunchOnly(exec.Command(path, append(args, "--", req.URL)...), onState)
}

func startLaunchOnly(cmd *exec.Cmd, onState func(backend.MediaState)) (sharedPlayer, backend.MediaPlayer, error) {
	p := &launchOnlyPlayer{gate: stateGate{onState: onState}}
	proc, err := startPlayerProc(cmd, "", p.gate.emit)
	if err != nil {
		return nil, nil, err
	}
	p.proc = proc
	go p.gate.emit(backend.MediaState{Status: "playing"})
	return p, p, nil
}

type launchOnlyPlayer struct {
	proc *playerProc
	gate stateGate
}

var errNoControl = errors.New("this player can't be controlled here")

func (*launchOnlyPlayer) Play() error             { return errNoControl }
func (*launchOnlyPlayer) Pause() error            { return errNoControl }
func (*launchOnlyPlayer) Seek(float64) error      { return errNoControl }
func (*launchOnlyPlayer) SetVolume(float64) error { return errNoControl }
func (*launchOnlyPlayer) SetTitle(string) error   { return nil }
func (p *launchOnlyPlayer) Stop() error           { return p.proc.cmd.Process.Kill() }

func (*launchOnlyPlayer) replace(backend.MediaRequest, func(backend.MediaState)) backend.MediaPlayer {
	return nil
}

func (p *launchOnlyPlayer) retire() {
	p.gate.off.Store(true)
	p.Stop()
}
