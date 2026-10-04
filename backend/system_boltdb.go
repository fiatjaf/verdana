//go:build !linux || (!amd64 && !386)

package backend

import (
	"fmt"
	"path/filepath"

	"fiatjaf.com/nostr/eventstore"
	boltdb "fiatjaf.com/nostr/eventstore/boltdb"
)

// boltdb is a single file, and the name carries the engine: an install that
// ran a build using another engine (see system_lmdb.go) must not have that
// engine's directory sitting where this wants its file, or every launch
// fails with "is a directory" and there is nothing the UI can do about it.
// Naming it per engine also means such an install just starts on the new
// engine, empty, instead of needing its app data cleared.
func openEventStore(dataDir string) (eventstore.Store, func(), error) {
	path := filepath.Join(dataDir, "eventstore.bolt")
	log.Info().Str("path", path).Msg("init eventstore")
	db := &boltdb.BoltBackend{
		Path: path,
	}
	if err := db.Init(); err != nil {
		return nil, nil, fmt.Errorf("eventstore: %w", err)
	}
	return db, db.Close, nil
}
