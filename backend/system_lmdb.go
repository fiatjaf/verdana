//go:build linux && (amd64 || 386)

package backend

import (
	"fmt"
	"path/filepath"

	"fiatjaf.com/nostr/eventstore"
	lmdb "fiatjaf.com/nostr/eventstore/lmdb"
)

// lmdb is only used where the fiatjaf/lmdb-go fork has a working cgo build
// (linux x86), everywhere else openEventStore falls back to boltdb. It is a
// directory rather than a file, so it gets the engine in its name too: an
// install that ran a boltdb build must not have boltdb's file in the way, and
// vice versa.
func openEventStore(dataDir string) (eventstore.Store, func(), error) {
	path := filepath.Join(dataDir, "eventstore.lmdb")
	log.Info().Str("path", path).Msg("init eventstore")
	db := &lmdb.LMDBBackend{
		Path: path,
	}
	if err := db.Init(); err != nil {
		return nil, nil, fmt.Errorf("eventstore: %w", err)
	}
	return db, db.Close, nil
}
