//go:build linux && (amd64 || 386)

package backend

import (
	"fmt"

	"fiatjaf.com/nostr/eventstore"
	lmdb "fiatjaf.com/nostr/eventstore/lmdb"
)

// lmdb is only used where the fiatjaf/lmdb-go fork has a working cgo build
// (linux x86), everywhere else openEventStore falls back to boltdb.
func openEventStore(path string) (eventstore.Store, func(), error) {
	db := &lmdb.LMDBBackend{
		Path: path,
	}
	if err := db.Init(); err != nil {
		return nil, nil, fmt.Errorf("eventstore: %w", err)
	}
	return db, db.Close, nil
}
