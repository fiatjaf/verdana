// Package backend is everything Verdana does that isn't drawing: the nostr
// system and its local eventstore, the napp registry (discovery, install,
// launch), the action router and the whole window.nostr / window.nostrdb /
// window.napp surface a napp gets — env.d.ts is the contract for that surface
// and behavior.md for the behaviors around it.
//
// It deliberately knows nothing about how the launcher is drawn, nor what a
// "window" is. Both are the Host's business: the Gio desktop app gives a napp
// an OS window backed by its own webview process, the Android app gives it a
// tab backed by an in-process WebView, and neither difference reaches this
// package.
package backend

import (
	"os"
	"path/filepath"

	"fiatjaf.com/nostr/sdk"
	"github.com/rs/zerolog"
)

var (
	sys     *sdk.System
	log     zerolog.Logger
	host    Host
	dataDir string
)

// Options is what a GUI has to hand over to get a working backend.
type Options struct {
	// DataDir is where the eventstore, the kvstore, state.json and the
	// installed napps live. It is created if missing.
	DataDir string

	// Host is the platform: windows, prompts, clipboard, files, links.
	Host Host

	// Log is optional; without one, logs go to stderr.
	Log *zerolog.Logger
}

// Start brings the backend up: stores open, state loaded, profile index
// building, and the stored login being resumed (so the GUI can render
// State().Phase right away). The returned function closes the stores.
func Start(opts Options) (func(), error) {
	if opts.Log != nil {
		log = *opts.Log
	} else {
		log = zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr}).With().Timestamp().Logger()
	}

	if opts.Host == nil {
		opts.Host = noopHost{}
	}
	host = opts.Host

	dataDir = opts.DataDir
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, err
	}

	closeStores, err := initSystem(dataDir)
	if err != nil {
		return nil, err
	}

	loadState()
	refreshInstalled()
	go restoreWindows()
	go buildUserIndex()

	// resume the stored login, or ask for one
	if stored := StoredLogin(); stored != "" {
		go Login(stored)
	} else {
		setPhase(PhaseLogin)
	}

	return closeStores, nil
}

// DataDir is where everything the backend persists lives.
func DataDir() string { return dataDir }

// Logger is the backend's logger, so a GUI can log into the same stream.
func Logger() zerolog.Logger { return log }

func nappBaseDir(id string) string {
	return filepath.Join(dataDir, "napps", id)
}

// NappBaseDir is where a napp's files are unpacked.
func NappBaseDir(id string) string { return nappBaseDir(id) }
