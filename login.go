package main

import (
	"context"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/keyer"
)

var (
	userKeyer  nostr.Keyer
	userPubkey nostr.PubKey
)

func doLogin(input string) {
	setPhase("loading")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	k, err := keyer.New(ctx, sys.Pool, input, &keyer.SignerOptions{
		BunkerClientSecretKey: state.ClientKey,
		BunkerAuthHandler:     func(url string) {},
	})
	if err != nil {
		ui.mu.Lock()
		ui.loginErr = err.Error()
		ui.phase = "login"
		ui.mu.Unlock()
		gioWin.Invalidate()
		return
	}

	pk, err := k.GetPublicKey(ctx)
	if err != nil {
		ui.mu.Lock()
		ui.loginErr = err.Error()
		ui.phase = "login"
		ui.mu.Unlock()
		gioWin.Invalidate()
		return
	}

	userKeyer = k
	userPubkey = pk

	if state.Login != input {
		state.Login = input
		saveState()
	}

	pm := sys.FetchProfileMetadata(ctx, pk)
	name := pm.Name
	if name == "" {
		name = pm.DisplayName
	}
	if name == "" {
		name = pk.Hex()
	}

	ui.mu.Lock()
	ui.loginErr = ""
	ui.profName = name
	ui.profPic = pm.Picture
	ui.phase = "main"
	ui.mu.Unlock()
	gioWin.Invalidate()

	go doFetch(state.Relays)
}
