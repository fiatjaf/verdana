package backend

import (
	"fmt"
	"path/filepath"

	lmdb "fiatjaf.com/nostr/eventstore/lmdb"
	"fiatjaf.com/nostr/sdk"
	bolt_kv "fiatjaf.com/nostr/sdk/kvstore/bbolt"
)

func initSystem(dataDir string) (func(), error) {
	log.Info().Str("path", filepath.Join(dataDir, "eventstore")).Msg("init eventstore")
	db := &lmdb.LMDBBackend{
		Path: filepath.Join(dataDir, "eventstore"),
	}
	if err := db.Init(); err != nil {
		return nil, fmt.Errorf("eventstore: %w", err)
	}

	log.Info().Str("path", filepath.Join(dataDir, "kvstore")).Msg("init kvstore")
	kv, err := bolt_kv.NewStore(filepath.Join(dataDir, "kvstore"))
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("kvstore: %w", err)
	}

	sys = sdk.NewSystem()
	sys.KVStore = kv
	sys.Store = db

	sys.Pool.QueryMiddleware = sys.TrackQueryAttempts
	sys.Pool.EventMiddleware = sys.TrackEventHintsAndRelays
	sys.Pool.DuplicateMiddleware = sys.TrackEventRelaysD

	log.Info().Msg("system initialized")
	return db.Close, nil
}

// Sys is the sdk system the backend runs on, for a GUI that needs to reach it
// directly (the launcher's own profile lookups, say).
func Sys() *sdk.System { return sys }
