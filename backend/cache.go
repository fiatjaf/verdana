package backend

import "github.com/dgraph-io/ristretto/v2"

func mustNewCache[K ristretto.Key, V any](size int) *ristretto.Cache[K, V] {
	c, err := ristretto.NewCache(&ristretto.Config[K, V]{
		NumCounters: int64(size * 10),
		MaxCost:     int64(size),
		BufferItems: 64,
	})
	if err != nil {
		panic(err)
	}
	return c
}
