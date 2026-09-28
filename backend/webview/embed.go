// Package webview carries the JavaScript and CSS that turn a webview into a
// napp host: window.nostr, window.nostrdb and window.napp, as env.d.ts
// describes them, and napp-ui.css, the kit napps opt into.
//
// It lives in its own package, with no dependencies, so both shells can embed
// the very same files: the desktop's webview child process links it directly,
// and the Android app gets it through the gomobile binding. There is exactly
// one bridge.js, so the two platforms cannot drift apart.
package webview

import (
	_ "embed"
	"encoding/json"
	"slices"
)

//go:embed bridge.js
var bridgeJS string

//go:embed napp-ui.css
var uiCSS string

//go:embed napp-ui.js
var uiJS string

// JS is bridge.js, to be injected before a napp's page runs (and again on
// every navigation, which both shells do).
func JS() string { return bridgeJS }

// UIKitScript is the script that puts the kit in a napp's page, or "" when the
// napp did not ask for it: `requires: ["ui"]` in metadata.json is what asks.
// Both shells run it at document start, beside the bridge and the theme.
//
// It is the helpers (window.napp.ui, napp-ui.js) and then the stylesheet
// (napp-ui.css) as one <style>, added as the document opens, so the kit lands
// ahead of whatever the napp links itself: a napp that disagrees with the kit
// wins, and a napp with no styles at all still comes up in the launcher's
// colors.
func UIKitScript(requires []string) string {
	if !slices.Contains(requires, "ui") {
		return ""
	}
	css, err := json.Marshal(uiCSS)
	if err != nil {
		return ""
	}
	// the two statements are separate expressions: the semicolon keeps the
	// first one's result from being read as a call of the second
	return uiJS + ";\n(function(){" +
		"var css = " + string(css) + ";" +
		"function kit(){" +
		// a page can be torn down and built again under us; one style is enough
		"if (document.getElementById('__verdana_ui')) return true;" +
		"var root = document.head || document.documentElement;" +
		"if (!root) return false;" +
		"var s = document.createElement('style');" +
		"s.id = '__verdana_ui';" +
		"s.textContent = css;" +
		"root.appendChild(s);" +
		"return true;" +
		"}" +
		// at document start there may be no <html> yet, so wait for one
		"if (kit()) return;" +
		"var observer = new MutationObserver(function(){" +
		"if (kit()) observer.disconnect();" +
		"});" +
		"observer.observe(document, { childList: true, subtree: true });" +
		"})()"
}
