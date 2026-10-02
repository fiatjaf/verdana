package backend

import (
	"os"
	"path/filepath"
	"testing"
)

func TestThemeModePersistsSeparatelyFromEffectiveTheme(t *testing.T) {
	stateMu.Lock()
	oldState, oldPath := state, statePath
	statePath = filepath.Join(t.TempDir(), "state.json")
	state.Theme = ThemeSystem
	stateMu.Unlock()
	themeMu.Lock()
	oldName, oldVars := themeName, themeVars
	themeName = ThemeLight
	themeMu.Unlock()
	t.Cleanup(func() {
		stateMu.Lock()
		state, statePath = oldState, oldPath
		stateMu.Unlock()
		themeMu.Lock()
		themeName, themeVars = oldName, oldVars
		themeMu.Unlock()
	})

	SetThemeMode(ThemeDark)
	if got := ThemeMode(); got != ThemeDark {
		t.Fatalf("ThemeMode() = %q, want dark", got)
	}
	if got := ThemeName(); got != ThemeLight {
		t.Fatalf("effective theme changed to %q with preference, want light", got)
	}

	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) == "" {
		t.Fatal("theme mode was not persisted")
	}

	SetThemeMode("unsupported")
	if got := ThemeMode(); got != ThemeSystem {
		t.Fatalf("invalid mode resolved to %q, want system", got)
	}
}
