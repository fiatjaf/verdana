package backend

import (
	"fmt"
	"path/filepath"

	"fiatjaf.com/nostr/sdk"
	bolt_kv "fiatjaf.com/nostr/sdk/kvstore/bbolt"
)

func initSystem(dataDir string) (func(), error) {
	db, closeEventStore, err := openEventStore(dataDir)
	if err != nil {
		return nil, err
	}

	log.Info().Str("path", filepath.Join(dataDir, "kvstore")).Msg("init kvstore")
	kv, err := bolt_kv.NewStore(filepath.Join(dataDir, "kvstore"))
	if err != nil {
		closeEventStore()
		return nil, fmt.Errorf("kvstore: %w", err)
	}

	sys = sdk.NewSystem()
	sys.KVStore = kv
	sys.Store = db
	sys.Publisher = storePublisher{}

	sys.Pool.QueryMiddleware = sys.TrackQueryAttempts
	sys.Pool.EventMiddleware = sys.TrackEventHintsAndRelays
	sys.Pool.DuplicateMiddleware = sys.TrackEventRelaysD

	log.Info().Msg("system initialized")
	return closeEventStore, nil
}

// Sys is the sdk system the backend runs on, for a GUI that needs to reach it
// directly (the launcher's own profile lookups, say).
func Sys() *sdk.System { return sys }
