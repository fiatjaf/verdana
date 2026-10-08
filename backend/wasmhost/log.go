// Package wasmhost runs a wasm napp: the module (Napp), and the bridge that
// carries its napp_call traffic to and from the launcher (Bridge). It is the
// host side of the contract in verdana/backend/wasm, with nothing about
// windows in it — the desktop's canvas shell (desktop/wasmchild) puts it in a
// Gio window of its own process, the Android app runs it inside the launcher
// and paints its canvas into a View.
package wasmhost

import (
	"os"

	"github.com/rs/zerolog"
)

var log = zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr}).With().Timestamp().Logger()

// SetLogger sends this package's log lines where the shell's own go. Call it
// before anything else here runs.
func SetLogger(l zerolog.Logger) { log = l }
