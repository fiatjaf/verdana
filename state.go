package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"fiatjaf.com/nostr"
)

var defaultRelays = []string{
	"relay.nostrapps.com",
	"relay.nostrapps.com/public",
}

var defaultBlossomServers = []string{
	"https://blossom.primal.net",
	"https://cdn.satellite.earth",
}

var (
	state     AppState
	statePath string
	stateMu   sync.Mutex
)

func loadState() {
	statePath = filepath.Join(verdanaDir, "state.json")
	data, err := os.ReadFile(statePath)
	if err == nil {
		json.Unmarshal(data, &state)
	}
	if state.ClientKey == (nostr.SecretKey{}) {
		state.ClientKey = nostr.Generate()
	}
	if len(state.Relays) == 0 {
		state.Relays = append([]string(nil), defaultRelays...)
	}
	if state.InstalledNapps == nil {
		state.InstalledNapps = make(map[string]Napp)
	}
	saveState()
}

func saveState() {
	data, err := json.MarshalIndent(&state, "", "  ")
	if err != nil {
		return
	}
	os.WriteFile(statePath, data, 0600)
}
