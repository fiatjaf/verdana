package backend

import "sync"

// The launcher has one theme at a time and every napp tracks it. The colors
// themselves belong to whoever draws the launcher (a Gio palette, a Compose
// color scheme); the backend only carries the name and the CSS tokens that go
// to napps, persists the choice, and makes sure every window hears about a
// change.

var (
	themeMu   sync.Mutex
	themeName = "light"
	themeVars = "{}"
)

// Theme is the current theme name and its CSS custom properties as JSON (the
// `--surface`/`--text`/… tokens behavior.md documents, without the dashes).
func Theme() (string, string) {
	themeMu.Lock()
	defer themeMu.Unlock()
	return themeName, themeVars
}

// ThemeName is the current theme name on its own.
func ThemeName() string {
	name, _ := Theme()
	return name
}

// SetTheme records the theme the launcher is drawing with, persists it and
// pushes it into every open napp. The GUI calls it once at startup (with the
// tokens for the theme it restored) and again on every switch.
func SetTheme(name, varsJSON string) {
	if name != "dark" {
		name = "light"
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

	stateMu.Lock()
	if state.Theme != name {
		state.Theme = name
		saveState()
	}
	stateMu.Unlock()

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
}
