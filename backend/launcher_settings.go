package backend

import (
	neturl "net/url"
	"slices"
	"strings"

	"fiatjaf.com/nostr"
)

// The launcher's own settings, as the settings window's Verdana page edits
// them: the discovery relays (state.Relays, see SetRelays) and the Blossom
// servers files are fetched from.

// defaultBlossomServers are where napp files are looked for first when the
// user never chose: the napp host most napps are published to, and a big
// public server.
var defaultBlossomServers = []string{
	"https://relay.nostrapps.com",
	"https://nostr.download",
}

// BlossomServers are the launcher's own Blossom servers, in order.
func BlossomServers() []string {
	stateMu.Lock()
	defer stateMu.Unlock()
	if state.BlossomServers == nil {
		return append([]string(nil), defaultBlossomServers...)
	}
	return append([]string(nil), state.BlossomServers...)
}

// SetBlossomServers stores the launcher's Blossom servers. Anything that
// isn't an http(s) url is dropped; a bare host is taken as https.
func SetBlossomServers(servers []string) {
	cleaned := make([]string, 0, len(servers))
	for _, raw := range servers {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if !strings.Contains(raw, "://") {
			raw = "https://" + raw
		}
		u, err := neturl.Parse(raw)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			continue
		}
		url, err := nostr.NormalizeHTTPURL(raw)
		if err != nil || url == "" {
			continue
		}
		if !slices.Contains(cleaned, url) {
			cleaned = append(cleaned, url)
		}
	}

	stateMu.Lock()
	state.BlossomServers = cleaned
	saveState()
	stateMu.Unlock()
	notifyState()
}
