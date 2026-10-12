//go:build !linux || (!amd64 && !386)

package backend

import (
	"fmt"
	"iter"
	"path/filepath"
	"slices"

	"fiatjaf.com/nostr"
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
	return boltStore{db}, db.Close, nil
}

// boltStore closes a query's read transaction before yielding its events.
// bbolt deadlocks when a write has to grow the file while a read transaction
// is open, and callers do write from inside query loops (the sdk's list
// fetchers save refreshed lists mid-query). Queries are always limited, so
// collecting the events first is cheap.
type boltStore struct {
	*boltdb.BoltBackend
}

func (s boltStore) QueryEvents(filter nostr.Filter, maxLimit int) iter.Seq[nostr.Event] {
	return func(yield func(nostr.Event) bool) {
		for _, evt := range slices.Collect(s.BoltBackend.QueryEvents(filter, maxLimit)) {
			if !yield(evt) {
				return
			}
		}
	}
}
