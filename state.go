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
	} else {
		log.Debug().Err(err).Msg("no existing state file, using defaults")
	}
	if state.ClientKey == (nostr.SecretKey{}) {
		state.ClientKey = nostr.Generate()
		log.Debug().Msg("generated new client key")
	}
	if len(state.Relays) == 0 {
		state.Relays = append([]string(nil), defaultRelays...)
	}
	if state.InstalledNapps == nil {
		state.InstalledNapps = make(map[string]Napp)
	}
	saveState()
	log.Info().Int("napps", len(state.InstalledNapps)).Msg("state loaded")
}

func saveState() {
	data, err := json.MarshalIndent(&state, "", "  ")
	if err != nil {
		log.Error().Err(err).Msg("failed to marshal state")
		return
	}
	if err := os.WriteFile(statePath, data, 0600); err != nil {
		log.Error().Err(err).Msg("failed to write state file")
	}
}
