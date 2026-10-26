package backend

import (
	"context"
	"errors"
	"time"

	"fiatjaf.com/nostr/sdk"
)

// bootstrapConfig is the napp configuration a new user is offered on their
// first login: a set of napps, bundles and settings to start from.
const bootstrapConfig = "naddr1qqykymm0w3ehgunpwqq3jamnwvaz7tms09exzmtfvshxv6tpw34xze3wvdhk6q3q80cvv07tjdrrgpa0j7j7tmnyl2yr6yr7l8j4s3evf6u64th6gkwsxpqqqzyn6at8tmf"

// needsWelcome says whether a login is the first one: the launcher was never
// welcomed and has nothing set up yet (a launcher from before the welcome
// screen existed, with napps already, isn't asked).
func needsWelcome() bool {
	stateMu.Lock()
	defer stateMu.Unlock()
	return !state.Welcomed && len(state.InstalledNapps) == 0 && len(state.Configurations) == 0
}

// Welcome answers the welcome screen: with bootstrap the bootstrap
// configuration is fetched and applied, without asking again; either way the
// launcher goes on to its main screen and never welcomes anyone again. It
// returns right away.
func Welcome(bootstrap bool) {
	stateMu.Lock()
	state.Welcomed = true
	saveState()
	stateMu.Unlock()
	setPhase(PhaseMain)
	log.Info().Bool("bootstrap", bootstrap).Msg("welcomed")

	if bootstrap {
		go func() {
			if err := applyBootstrap(); err != nil {
				log.Warn().Err(err).Msg("could not apply the bootstrap configuration")
				SetFetchErr("bootstrap configuration: " + err.Error())
			}
		}()
	}
}

// applyBootstrap fetches the bootstrap configuration and applies it.
// Blocking.
func applyBootstrap() error {
	if sys == nil {
		return errors.New("not ready yet")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	evt, _, err := sys.FetchSpecificEventFromInput(ctx, bootstrapConfig,
		sdk.FetchSpecificEventParameters{SaveToLocalStore: true})
	if err != nil || evt == nil {
		return errors.New("couldn't find it")
	}
	if evt.Kind != KindConfig {
		return errors.New("it is not a napp configuration")
	}
	applyConfiguration(configFromEvent(*evt))
	return nil
}
