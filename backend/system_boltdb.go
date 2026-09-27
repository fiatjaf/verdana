//go:build !linux || (!amd64 && !386)

package backend

import (
	"fmt"

	"fiatjaf.com/nostr/eventstore"
	boltdb "fiatjaf.com/nostr/eventstore/boltdb"
)

func openEventStore(path string) (eventstore.Store, func(), error) {
	db := &boltdb.BoltBackend{
		Path: path,
	}
	if err := db.Init(); err != nil {
		return nil, nil, fmt.Errorf("eventstore: %w", err)
	}
	return db, db.Close, nil
}
