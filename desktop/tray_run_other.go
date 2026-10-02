//go:build !darwin

package main

import "runtime"

func runDesktop(background bool) {
	ready := make(chan trayRun)
	go func() {
		runtime.LockOSThread()
		tray := newTray()
		done := make(chan struct{})
		ready <- trayRun{remove: tray.Remove, done: done}
		if err := tray.Run(); err != nil {
			log.Error().Err(err).Msg("system tray stopped")
			quitDesktop()
		}
		close(done)
	}()
	runner := <-ready
	desktopLoop(background)
	runner.remove()
	<-runner.done
}

type trayRun struct {
	remove func()
	done   <-chan struct{}
}
