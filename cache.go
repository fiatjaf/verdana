package main

import (
	"context"
	"image"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"fiatjaf.com/nostr"
	"gioui.org/op/paint"
	"github.com/rs/zerolog/log"
)

type imgEntry struct {
	once   sync.Once
	op     paint.ImageOp
	ready  atomic.Bool
	failed atomic.Bool
}

var imgCache sync.Map

type authorEntry struct {
	once  sync.Once
	name  string
	pic   string
	ready atomic.Bool
}

var authorCache sync.Map

func authorMeta(pubkeyHex string) (string, string) {
	if pubkeyHex == "" {
		return "", ""
	}
	v, _ := authorCache.LoadOrStore(pubkeyHex, &authorEntry{})
	e := v.(*authorEntry)
	e.once.Do(func() {
		go func() {
			log.Debug().Str("pubkey", pubkeyHex).Msg("fetching author metadata")
			pk, err := nostr.PubKeyFromHex(pubkeyHex)
			if err != nil {
				log.Error().Err(err).Str("pubkey", pubkeyHex).Msg("invalid pubkey for author meta")
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			pm := sys.FetchProfileMetadata(ctx, pk)
			e.name = pm.Name
			if e.name == "" {
				e.name = pm.DisplayName
			}
			e.pic = pm.Picture
			e.ready.Store(true)
			if gioWin != nil {
				gioWin.Invalidate()
			}
		}()
	})
	if e.ready.Load() {
		return e.name, e.pic
	}
	return "", ""
}

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
