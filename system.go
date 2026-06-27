package main

import (
	"path/filepath"

	lmdb "fiatjaf.com/nostr/eventstore/lmdb"
	"fiatjaf.com/nostr/sdk"
	bolt_kv "fiatjaf.com/nostr/sdk/kvstore/bbolt"
	"github.com/rs/zerolog/log"
)

func initSystem(dataDir string) func() {
	log.Info().Str("path", filepath.Join(dataDir, "eventstore")).Msg("init eventstore")
	db := &lmdb.LMDBBackend{
		Path: filepath.Join(dataDir, "eventstore"),
	}
	if err := db.Init(); err != nil {
		log.Fatal().Err(err).Msg("failed to init eventstore")
	}

	log.Info().Str("path", filepath.Join(dataDir, "kvstore")).Msg("init kvstore")
	kv, err := bolt_kv.NewStore(filepath.Join(dataDir, "kvstore"))
	if err != nil {
		log.Fatal().Err(err).Msg("failed to init kvstore")
	}

	sys = sdk.NewSystem()
	sys.KVStore = kv
	sys.Store = db

	sys.Pool.QueryMiddleware = sys.TrackQueryAttempts
	sys.Pool.EventMiddleware = sys.TrackEventHintsAndRelays
	sys.Pool.DuplicateMiddleware = sys.TrackEventRelaysD

	log.Info().Msg("system initialized")
	return db.Close
}
