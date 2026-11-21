package backend

import (
	"context"
	"errors"
	"strings"

	"fiatjaf.com/nostr"
)

// signRelayAuth signs a NIP-42 AUTH event as the logged-in user.
func signRelayAuth(ctx context.Context, evt *nostr.Event) error {
	k, _ := currentKeyer()
	if k == nil {
		return errors.New("not logged in")
	}
	return k.SignEvent(ctx, evt)
}

// applyReadAuth decides whether relays that answer "auth-required:" to a
// query or a subscription get an AUTH: only with someone logged in and the
// user having explicitly opted in. Read auth defaults OFF so untrusted napps
// cannot silently trigger NIP-42 kind:22242 signing against arbitrary relays.
func applyReadAuth() {
	stateMu.Lock()
	on := state.ReadAuth
	stateMu.Unlock()

	k, _ := currentKeyer()
	if k != nil && on {
		sys.Pool.AuthRequiredHandler = signRelayAuth
	} else {
		sys.Pool.AuthRequiredHandler = nil
	}
}

// ReadAuth is whether relays that ask for it get authenticated to when reading.
func ReadAuth() bool {
	stateMu.Lock()
	defer stateMu.Unlock()
	return state.ReadAuth
}

// SetReadAuth turns authenticating to relays when reading on or off.
// Publishing authenticates regardless.
func SetReadAuth(on bool) {
	stateMu.Lock()
	changed := state.ReadAuth != on
	state.ReadAuth = on
	if changed {
		saveState()
	}
	stateMu.Unlock()

	if changed {
		log.Info().Bool("on", on).Msg("read auth changed")
		applyReadAuth()
		notifyState()
	}
}

// publishToRelays is sys.Pool.PublishMany, but a relay that answers
// "auth-required:" always gets an AUTH from the logged-in user and the event
// again, whatever the read auth setting says. Forbidden local relay URLs are
// refused before dialing.
func publishToRelays(ctx context.Context, urls []string, evt nostr.Event) chan nostr.PublishResult {
	out := make(chan nostr.PublishResult, len(urls))
	go func() {
		defer close(out)
		allowed := make([]string, 0, len(urls))
		for _, u := range urls {
			if err := isAllowedRelayURL(ctx, u); err != nil {
				out <- nostr.PublishResult{RelayURL: u, Error: err}
				continue
			}
			allowed = append(allowed, u)
		}
		if len(allowed) == 0 {
			return
		}
		for res := range sys.Pool.PublishMany(ctx, allowed, evt) {
			k, _ := currentKeyer()
			if res.Error != nil && res.Relay != nil && k != nil &&
				strings.HasPrefix(res.Error.Error(), "msg: auth-required:") {
				if err := res.Relay.Auth(ctx, signRelayAuth); err != nil {
					res.Error = errors.New("failed to auth: " + err.Error())
				} else {
					res.Error = res.Relay.Publish(ctx, evt)
				}
			}
			out <- res
		}
	}()
	return out
}
