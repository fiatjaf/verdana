package backend

import "sync"

// The launcher has one theme at a time and every napp tracks it. The colors
// themselves belong to whoever draws the launcher (a Gio palette, a Compose
// color scheme); the backend carries the effective name and CSS tokens that
// go to napps, persists the separate system/light/dark preference, and makes
// sure every window hears about an effective change.

var (
	themeMu   sync.RWMutex
	themeName = "light"
	themeVars = "{}"
)

const (
	ThemeSystem = "system"
	ThemeLight  = "light"
	ThemeDark   = "dark"
)

// Theme is the current theme name and its CSS custom properties as JSON (the
// `--surface`/`--text`/… tokens behavior.md documents, without the dashes).
func Theme() (string, string) {
	themeMu.RLock()
	defer themeMu.RUnlock()
	return themeName, themeVars
}

// ThemeName is the current theme name on its own.
func ThemeName() string {
	name, _ := Theme()
	return name
}

// ThemeMode is the user's persisted preference. It is separate from Theme,
// which is always the effective light or dark palette currently being drawn.
func ThemeMode() string {
	stateMu.Lock()
	defer stateMu.Unlock()
	if state.Theme == ThemeLight || state.Theme == ThemeDark {
		return state.Theme
	}
	return ThemeSystem
}

// SetThemeMode persists the user's preference. The platform UI resolves
// system mode and calls SetTheme with the resulting palette.
func SetThemeMode(mode string) {
	if mode != ThemeLight && mode != ThemeDark {
		mode = ThemeSystem
	}
	stateMu.Lock()
	changed := state.Theme != mode
	state.Theme = mode
	if changed {
		saveState()
	}
	stateMu.Unlock()
	if changed {
		log.Info().Str("mode", mode).Msg("theme mode changed")
		notifyState()
	}
}

// SetTheme records the effective theme the launcher is drawing with and
// pushes it into every open napp. Preference persistence belongs to
// SetThemeMode so an OS appearance change cannot overwrite "system".
func SetTheme(name, varsJSON string) {
	if name != ThemeDark {
		name = ThemeLight
	}
	if varsJSON == "" {
		varsJSON = "{}"
	}

	themeMu.Lock()
	changed := themeName != name || themeVars != varsJSON
	themeName, themeVars = name, varsJSON
	themeMu.Unlock()
	if !changed {
		return
	}

	log.Info().Str("theme", name).Msg("theme changed")
	notifyState()
	go broadcastTheme()
}

// broadcastTheme pushes the current theme into every running napp window.
func broadcastTheme() {
	name, vars := Theme()
	open := allInstances()
	for _, ci := range open {
		ci.send(WireMsg{T: "theme", Method: name, Params: vars})
	}
	log.Debug().Str("theme", name).Int("napps", len(open)).Msg("pushed theme to napps")
	broadcastNappletTheme()
	broadcastSettingsTheme(name, vars)
}
