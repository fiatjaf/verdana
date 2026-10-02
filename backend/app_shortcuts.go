package backend

import (
	"context"
	"sort"
	"sync"
	"time"
)

const (
	AppShortcutNamePlain  = "plain"
	AppShortcutNameHosted = "hosted"
)

var appShortcutSyncMu sync.Mutex

func AppShortcutsEnabled() bool {
	stateMu.Lock()
	defer stateMu.Unlock()
	return state.ExposeInstalledApps
}

func AppShortcutNaming() string {
	stateMu.Lock()
	defer stateMu.Unlock()
	if state.AppShortcutNameStyle == AppShortcutNameHosted {
		return AppShortcutNameHosted
	}
	return AppShortcutNamePlain
}

func SetAppShortcutSettings(enabled bool, naming string) {
	if naming != AppShortcutNameHosted {
		naming = AppShortcutNamePlain
	}
	stateMu.Lock()
	state.ExposeInstalledApps = enabled
	state.AppShortcutNameStyle = naming
	saveState()
	stateMu.Unlock()
	go syncAppShortcuts()
}

// syncAppShortcuts serializes complete reconciliation passes. Icon loading is
// best-effort: a missing or temporarily unavailable icon never hides an app.
func syncAppShortcuts() {
	appShortcutSyncMu.Lock()
	defer appShortcutSyncMu.Unlock()

	if !AppShortcutsEnabled() {
		if err := host.SyncAppShortcuts(nil); err != nil {
			log.Warn().Err(err).Msg("could not remove system app shortcuts")
		}
		return
	}

	naming := AppShortcutNaming()
	napps := installedNapps()
	sort.Slice(napps, func(i, j int) bool { return napps[i].ID < napps[j].ID })
	shortcuts := make([]AppShortcut, 0, len(napps))
	for _, napp := range napps {
		name := napp.Label()
		if naming == AppShortcutNameHosted {
			name += " — Verdana"
		}
		shortcut := AppShortcut{ID: napp.ID, Name: name, Description: napp.Description}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		shortcut.Icon, _ = napp.IconBlob(ctx)
		cancel()
		shortcuts = append(shortcuts, shortcut)
	}
	if err := host.SyncAppShortcuts(shortcuts); err != nil {
		log.Warn().Err(err).Msg("could not synchronize system app shortcuts")
	}
}
