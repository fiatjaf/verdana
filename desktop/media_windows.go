package main

import (
	"errors"
	"os/exec"

	"verdana/backend"
)

// On Windows mpv's IPC is a named pipe and VLC's rc a TCP port; neither is
// wired up yet, so the player is only started and stopped: the session
// reports "playing" until the player exits, and ignores everything else.

func startMpv(path string, req backend.MediaRequest, onState func(backend.MediaState)) (backend.MediaPlayer, error) {
	args := []string{"--force-window=yes", "--idle=no", "--ytdl=no"}
	if req.Title != "" {
		args = append(args, "--force-media-title="+req.Title)
	}
	if !req.Autoplay {
		args = append(args, "--pause")
	}
	return startLaunchOnly(exec.Command(path, append(args, "--", req.URL)...), onState)
}

func startVLC(path string, req backend.MediaRequest, onState func(backend.MediaState)) (backend.MediaPlayer, error) {
	args := []string{"--play-and-exit", "--no-one-instance"}
	if !req.Autoplay {
		args = append(args, "--start-paused")
	}
	return startLaunchOnly(exec.Command(path, append(args, "--", req.URL)...), onState)
}

func startLaunchOnly(cmd *exec.Cmd, onState func(backend.MediaState)) (backend.MediaPlayer, error) {
	proc, err := startPlayerProc(cmd, "", onState)
	if err != nil {
		return nil, err
	}
	go onState(backend.MediaState{Status: "playing"})
	return launchOnlyPlayer{proc}, nil
}

type launchOnlyPlayer struct{ proc *playerProc }

var errNoControl = errors.New("this player can't be controlled here")

func (launchOnlyPlayer) Play() error             { return errNoControl }
func (launchOnlyPlayer) Pause() error            { return errNoControl }
func (launchOnlyPlayer) Seek(float64) error      { return errNoControl }
func (launchOnlyPlayer) SetVolume(float64) error { return errNoControl }
func (launchOnlyPlayer) SetTitle(string) error   { return nil }
func (p launchOnlyPlayer) Stop() error           { return p.proc.cmd.Process.Kill() }
