//go:build darwin

package main

// AppKit's tray loop has to own the process main thread. Gio windows run from
// the desktop goroutine and use that same NSApplication event loop.
func runDesktop(background bool) {
	tray := newTray()
	done := make(chan struct{})
	go func() {
		desktopLoop(background)
		tray.Remove()
		close(done)
	}()
	if err := tray.Run(); err != nil {
		log.Error().Err(err).Msg("system tray stopped")
		quitDesktop()
	}
	<-done
}
