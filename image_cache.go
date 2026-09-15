package main

import (
	"image"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"gioui.org/op/paint"
)

type imgEntry struct {
	once   sync.Once
	op     paint.ImageOp
	ready  atomic.Bool
	failed atomic.Bool
}

var imgCache sync.Map

func getImage(url string) (paint.ImageOp, bool) {
	if url == "" {
		return paint.ImageOp{}, false
	}
	v, _ := imgCache.LoadOrStore(url, &imgEntry{})
	e := v.(*imgEntry)
	e.once.Do(func() {
		go func() {
			log.Debug().Str("url", url).Msg("fetching image")
			client := http.Client{Timeout: 15 * time.Second}
			resp, err := client.Get(url)
			if err != nil {
				log.Error().Err(err).Str("url", url).Msg("image fetch failed")
				e.failed.Store(true)
				return
			}
			defer resp.Body.Close()
			img, _, err := image.Decode(resp.Body)
			if err != nil {
				log.Error().Err(err).Str("url", url).Msg("image decode failed")
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
