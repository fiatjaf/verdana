package main

import (
	"path/filepath"

	lmdb "fiatjaf.com/nostr/eventstore/lmdb"
	"fiatjaf.com/nostr/sdk"
	bolt_kv "fiatjaf.com/nostr/sdk/kvstore/bbolt"
)

func initSystem(dataDir string) func() {
	db := &lmdb.LMDBBackend{
		Path: filepath.Join(dataDir, "eventstore"),
	}
	if err := db.Init(); err != nil {
		panic("failed to init eventstore: " + err.Error())
	}

	kv, err := bolt_kv.NewStore(filepath.Join(dataDir, "kvstore"))
	if err != nil {
		panic("failed to init kvstore: " + err.Error())
	}

	sys = sdk.NewSystem()
	sys.KVStore = kv
	sys.Store = db

	sys.Pool.QueryMiddleware = sys.TrackQueryAttempts
	sys.Pool.EventMiddleware = sys.TrackEventHintsAndRelays
	sys.Pool.DuplicateMiddleware = sys.TrackEventRelaysD

	return db.Close
}
