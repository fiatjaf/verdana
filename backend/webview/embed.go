// Package webview carries the JavaScript that turns a webview into a napp
// host: window.nostr, window.nostrdb and window.napp, as env.d.ts describes
// them.
//
// It lives in its own package, with no dependencies, so both shells can embed
// the very same file: the desktop's webview child process links it directly,
// and the Android app gets it through the gomobile binding. There is exactly
// one bridge.js, so the two platforms cannot drift apart.
package webview

import _ "embed"

//go:embed bridge.js
var bridgeJS string

// JS is bridge.js, to be injected before a napp's page runs (and again on
// every navigation, which both shells do).
func JS() string { return bridgeJS }
