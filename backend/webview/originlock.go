package webview

import (
	"encoding/json"
	"net/url"
	"strings"
)

// The origin lock. A napp's shell binds the bridge (window.nostr /
// window.nostrdb / window.napp and their native transports) for the webview
// as a whole, and those bindings survive navigation: a napp page that
// navigates itself to some other origin would land there with the bridge
// still attached — a remote document reading the shared local store, and
// answering this window's own permission prompts, as the napp.
//
// Both shells prevent this differently:
//
//   - Android injects the bridge and its web message listeners scoped to the
//     napp's origin, and blocks main-frame navigation off it
//     (NappWebView.shouldOverrideUrlLoading).
//   - Desktop has no per-origin binding, so the guard here is the lock: a
//     document-start script compares the page origin against the one the
//     shell chose, and reports any mismatch through __verdana_nav_violation.
//     The shell then kills the bridge for the window — synchronously, before
//     any of the new page's scripts can use it.
//
// The guard is main-frame-only: a cross-origin iframe is content the napp
// chose to embed, and it never sees the bridge on Android. If that ever
// changes on the desktop, tighten here too.

// ExpectedOrigin reduces a shell-chosen url to scheme://host[:port], the
// shape location.origin reports. Empty for an empty url (nothing loads).
func ExpectedOrigin(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return ""
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	origin := u.Scheme + "://" + u.Host
	return origin
}

// OriginGuardScript is the document-start check itself. One argument: the
// origin the shell serving this window picked. Any other origin on the main
// frame reports the violation once and never lets the bridge answer again.
func OriginGuardScript(expectedOrigin string) string {
	if expectedOrigin == "" {
		return ""
	}
	expected, _ := json.Marshal(expectedOrigin)
	return "(function(){" +
		"if (window.top !== window) return;" +
		"try { if (location.origin === " + string(expected) + ") return; } catch (e) {}" +
		"try { window.__verdana_nav_violation(); } catch (e) {}" +
		"})()"
}
