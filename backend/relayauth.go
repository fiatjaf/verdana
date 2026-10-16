package backend

import (
	"context"
	"errors"
	"strings"

	"fiatjaf.com/nostr"
)

// signRelayAuth signs a NIP-42 AUTH event as the logged-in user.
func signRelayAuth(ctx context.Context, evt *nostr.Event) error {
	if userKeyer == nil {
		return errors.New("not logged in")
	}
	return userKeyer.SignEvent(ctx, evt)
}

// applyReadAuth decides whether relays that answer "auth-required:" to a
// query or a subscription get an AUTH: only with someone logged in and the
// user not having turned it off. The pool's handler is left nil otherwise,
// rather than one that refuses, because a relay connection only ever tries
// AUTH once: a refused one would keep a later publish from authenticating.
func applyReadAuth() {
	stateMu.Lock()
	off := state.NoReadAuth
	stateMu.Unlock()

	if userKeyer != nil && !off {
		sys.Pool.AuthRequiredHandler = signRelayAuth
	} else {
		sys.Pool.AuthRequiredHandler = nil
	}
}

// ReadAuth is whether relays that ask for it get authenticated to when reading.
func ReadAuth() bool {
	stateMu.Lock()
	defer stateMu.Unlock()
	return !state.NoReadAuth
}

// SetReadAuth turns authenticating to relays when reading on or off.
// Publishing authenticates regardless.
func SetReadAuth(on bool) {
	stateMu.Lock()
	changed := state.NoReadAuth == on
	state.NoReadAuth = !on
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
// again, whatever the read auth setting says.
func publishToRelays(ctx context.Context, urls []string, evt nostr.Event) chan nostr.PublishResult {
	out := make(chan nostr.PublishResult, len(urls))
	go func() {
		defer close(out)
		for res := range sys.Pool.PublishMany(ctx, urls, evt) {
			if res.Error != nil && res.Relay != nil && userKeyer != nil &&
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
