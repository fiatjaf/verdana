package main

import (
	"sync"

	"gioui.org/app"
	"gioui.org/io/system"
)

var desktopLifecycle = struct {
	sync.Mutex
	window *app.Window
	show   chan struct{}
	quit   chan struct{}
	once   sync.Once
}{
	show: make(chan struct{}, 1),
	quit: make(chan struct{}),
}

func setManagerWindow(w *app.Window) {
	desktopLifecycle.Lock()
	desktopLifecycle.window = w
	desktopLifecycle.Unlock()
}

func managerWindow() *app.Window {
	desktopLifecycle.Lock()
	defer desktopLifecycle.Unlock()
	return desktopLifecycle.window
}

// showManager raises the manager when it exists, or asks the desktop loop to
// create it. It is safe for tray and single-instance callbacks to call.
func showManager() {
	if w := managerWindow(); w != nil {
		w.Perform(system.ActionRaise)
		w.Invalidate()
		return
	}
	select {
	case desktopLifecycle.show <- struct{}{}:
	default:
	}
}

func quitDesktop() {
	desktopLifecycle.once.Do(func() { close(desktopLifecycle.quit) })
	if w := managerWindow(); w != nil {
		w.Perform(system.ActionClose)
	}
}

func desktopLoop(background bool) {
	if !background {
		showManager()
	}
	for {
		select {
		case <-desktopLifecycle.show:
			select {
			case <-desktopLifecycle.quit:
				return
			default:
			}
			gioMain()
		case <-desktopLifecycle.quit:
			return
		}
	}
}
