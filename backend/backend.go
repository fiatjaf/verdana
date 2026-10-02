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
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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
	go buildUserIndex()

	// a first update round on its own, a bit after startup: not blocking the
	// launcher, and late enough not to compete with whatever the user is
	// doing in the first seconds (the manual reload button in the UI is
	// there for when they don't want to wait).
	go func() {
		time.Sleep(startupUpdateCheckDelay)
		CheckForUpdates()
	}()

	// resume the stored login, or ask for one
	if stored := StoredLogin(); stored != "" {
		go Login(stored)
	} else {
		setPhase(PhaseLogin)
	}

	return closeStores, nil
}

// startupUpdateCheckDelay is how long after startup the automatic check for
// napp updates waits before going out.
const startupUpdateCheckDelay = 20 * time.Second

// DataDir is where everything the backend persists lives.
func DataDir() string { return dataDir }

// Logger is the backend's logger, so a GUI can log into the same stream.
func Logger() zerolog.Logger { return log }

// nappBaseDir is where a napp's files live. The id carries the author's d-tag,
// which may hold separators or "..": those are escaped so a napp can never
// point its directory (and with it installs and uninstalls) outside napps/.
func nappBaseDir(id string) string {
	var b strings.Builder
	for i := 0; i < len(id); i++ {
		switch c := id[i]; {
		case c == '/' || c == '\\' || c == ':' || c == '%' || c < 0x20:
			fmt.Fprintf(&b, "%%%02X", c)
		default:
			b.WriteByte(c)
		}
	}
	name := b.String()
	if !filepath.IsLocal(name) {
		name = "_" + name
	}
	return filepath.Join(dataDir, "napps", name)
}

// migrateNappDir moves an installed napp's files from where they went before
// ids were escaped (the raw id) to where nappBaseDir looks for them now.
// Nothing happens when the names are the same, when the new place is already
// taken, or when the old name pointed outside the napps directory.
func migrateNappDir(id string) {
	to := nappBaseDir(id)
	from := filepath.Join(dataDir, "napps", id)
	if from == to || !filepath.IsLocal(id) {
		return
	}
	if _, err := os.Stat(to); err == nil {
		return
	}
	if _, err := os.Stat(from); err != nil {
		return
	}
	if err := os.Rename(from, to); err != nil {
		log.Warn().Err(err).Str("napp", id).Msg("could not move napp files to their new directory")
	}
}

// NappBaseDir is where a napp's files are unpacked.
func NappBaseDir(id string) string { return nappBaseDir(id) }
