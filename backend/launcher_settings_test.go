package backend

import (
	"encoding/json"
	"slices"
	"testing"
)

func resetLauncherState(t *testing.T) {
	t.Helper()
	stateMu.Lock()
	saved := state
	state = AppState{Relays: []string{"wss://relay.one"}}
	statePath = t.TempDir() + "/state.json"
	stateMu.Unlock()
	t.Cleanup(func() {
		stateMu.Lock()
		state = saved
		stateMu.Unlock()
	})
}

func TestBlossomServersDefaultAndSet(t *testing.T) {
	resetLauncherState(t)
	if got := BlossomServers(); !slices.Equal(got, defaultBlossomServers) {
		t.Fatalf("defaults: %v", got)
	}
	SetBlossomServers([]string{" blossom.example.com ", "https://b2.example/", "ftp://nope", "", "https://blossom.example.com"})
	want := []string{"https://blossom.example.com", "https://b2.example"}
	if got := BlossomServers(); !slices.Equal(got, want) {
		t.Fatalf("set: got %v want %v", got, want)
	}
	// an empty list is the user's choice, not "the defaults"
	SetBlossomServers(nil)
	if got := BlossomServers(); len(got) != 0 {
		t.Fatalf("emptied: %v", got)
	}
}

func TestNappBlossomServersStartWithOurs(t *testing.T) {
	resetLauncherState(t)
	SetBlossomServers([]string{"https://mine.example"})
	n := Napp{Servers: []string{"https://theirs.example", "https://mine.example"}}
	got := n.BlossomServers(t.Context())
	if !slices.Equal(got, []string{"https://mine.example", "https://theirs.example"}) {
		t.Fatalf("order: %v", got)
	}
}

func TestLauncherSettingsWindow(t *testing.T) {
	h := setupConfigTest(t)
	resetLauncherState(t)

	if err := OpenLauncherSettings(); err != nil {
		t.Fatal(err)
	}
	if h.count() != 1 || h.opened[0].Name != "Verdana" || h.opened[0].NappID != "" {
		t.Fatalf("opened: %+v", h.opened)
	}
	win := h.opened[0].Window
	srec := h.wins[win]

	HandleSettingsMessage(win, WireMsg{T: "rpc", ID: 1, Method: "settings.load"})
	var load settingsLoad
	if err := json.Unmarshal(srec.resp(t, 1).Result, &load); err != nil {
		t.Fatal(err)
	}
	if load.Napp || !slices.Equal(load.Launcher.Relays, []string{"wss://relay.one"}) ||
		!slices.Equal(load.Launcher.BlossomServers, defaultBlossomServers) || load.Launcher.ThemeMode != ThemeSystem ||
		!load.Launcher.AutostartSupported || load.Launcher.Autostart {
		t.Fatalf("load: %+v", load)
	}

	HandleSettingsMessage(win, WireMsg{T: "rpc", ID: 2, Method: "settings.saveLauncher",
		Params: `{"themeMode":"dark","relays":["relay.two"],"blossomServers":["https://b.example"]}`})
	if r := srec.resp(t, 2); r.Error != "" {
		t.Fatal(r.Error)
	}
	if !slices.Equal(Relays(), []string{"wss://relay.two"}) || !slices.Equal(BlossomServers(), []string{"https://b.example"}) {
		t.Fatalf("saved: %v %v", Relays(), BlossomServers())
	}
	if got := ThemeMode(); got != ThemeDark {
		t.Fatalf("saved theme mode: %q", got)
	}
	HandleSettingsMessage(win, WireMsg{T: "rpc", ID: 5, Method: "settings.saveLauncher", Params: `{"autostart":true}`})
	if r := srec.resp(t, 5); r.Error != "" {
		t.Fatal(r.Error)
	}
	if !h.autostart {
		t.Fatal("autostart was not enabled")
	}

	// a napp's window carries the launcher's page too
	ci, rec := openNapplet(t, "cfg-tabs")
	ready(t, ci, rec, 1)
	if _, err := napRPC(ci, "nap.openSettings", ""); err != nil {
		t.Fatal(err)
	}
	napWin := h.opened[1].Window
	HandleSettingsMessage(napWin, WireMsg{T: "rpc", ID: 3, Method: "settings.load"})
	load = settingsLoad{}
	_ = json.Unmarshal(h.wins[napWin].resp(t, 3).Result, &load)
	if !load.Napp || !slices.Equal(load.Launcher.Relays, []string{"wss://relay.two"}) {
		t.Fatalf("napp window load: %+v", load)
	}

	// nothing to forget in the launcher's own window
	HandleSettingsMessage(win, WireMsg{T: "rpc", ID: 4, Method: "settings.forgetPermission", Params: `{"permission":""}`})
	if r := srec.resp(t, 4); r.Error == "" {
		t.Fatal("forgot permissions for no napp")
	}
}

func TestBlossomServersEmptySurvivesRestart(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   []string
		want []string
	}{
		{"never set", nil, defaultBlossomServers},
		{"emptied", []string{}, []string{}},
	} {
		raw, err := json.Marshal(AppState{BlossomServers: tc.in})
		if err != nil {
			t.Fatal(err)
		}
		var back AppState
		if err := json.Unmarshal(raw, &back); err != nil {
			t.Fatal(err)
		}
		resetLauncherState(t)
		stateMu.Lock()
		state.BlossomServers = back.BlossomServers
		stateMu.Unlock()
		if got := BlossomServers(); !slices.Equal(got, tc.want) {
			t.Fatalf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}
