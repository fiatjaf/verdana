package main

import (
	"bytes"
	"context"
	"errors"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
	"verdana/backend"

	"gioui.org/op/paint"
	"github.com/dgraph-io/ristretto/v2"
)

type imgEntry struct {
	once   sync.Once
	op     paint.ImageOp
	ready  atomic.Bool
	failed atomic.Bool
}

var imgCache = mustNewCache[string, *imgEntry](256)

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

// cachedImage decodes an image once per key and hands the same ImageOp to
// every frame after that. load runs on its own goroutine — a layout function
// can't block — so the first frames get (zero, false) and the window is
// invalidated when the bytes land.
func cachedImage(key string, load func(context.Context) ([]byte, error)) (paint.ImageOp, bool) {
	if key == "" {
		return paint.ImageOp{}, false
	}
	e, ok := imgCache.Get(key)
	if !ok {
		e = &imgEntry{}
		imgCache.Set(key, e, 1)
		imgCache.Wait()
	}
	e.once.Do(func() {
		go func() {
			log.Debug().Str("image", key).Msg("loading image")
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			data, err := load(ctx)
			if err != nil {
				log.Error().Err(err).Str("image", key).Msg("image load failed")
				e.failed.Store(true)
				return
			}
			img, _, err := image.Decode(bytes.NewReader(data))
			if err != nil {
				log.Error().Err(err).Str("image", key).Msg("image decode failed")
				e.failed.Store(true)
				return
			}
			e.op = paint.NewImageOp(img)
			e.ready.Store(true)
			if gioWin != nil {
				gioWin.Invalidate()
			}
		}()
	})
	if e.ready.Load() {
		return e.op, true
	}
	return paint.ImageOp{}, false
}

// getImage loads an image off the web (profile pictures and the like).
func getImage(url string) (paint.ImageOp, bool) {
	return cachedImage(url, func(ctx context.Context) ([]byte, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, errors.New("status " + resp.Status)
		}
		return io.ReadAll(resp.Body)
	})
}

// nappIconImage loads the icon a napp declares. That icon is not a URL: the
// "icon" tag names one of the napp's own files, so it's a blossom blob like
// every other asset, and the backend knows how to get it — from the install
// directory or from the author's servers.
//
// Keyed by the blob hash, so two napps shipping the same icon share it and a
// reinstall doesn't refetch it.
func nappIconImage(n backend.Napp) (paint.ImageOp, bool) {
	hash := n.IconHash()
	if hash == "" {
		return paint.ImageOp{}, false
	}
	return cachedImage("sha256:"+hash, n.IconBlob)
}
