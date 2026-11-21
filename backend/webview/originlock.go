package webview

import (
	"encoding/json"
	"net"
	"net/url"
	"strings"
)

// The origin lock. A napp's shell binds the bridge (window.nostr /
// window.nostrdb / window.napp and their native transports) for the webview
// as a whole, and those bindings survive navigation: a napp page that
// navigates itself to some other origin would land there with the bridge
// still attached — a remote document reading the shared local store or
// invoking host RPCs as the napp.
//
// Both shells prevent this differently:
//
//   - Android injects the bridge and its web message listeners scoped to the
//     napp's origin, and blocks main-frame navigation off it
//     (NappWebView.shouldOverrideUrlLoading).
//   - Desktop has no per-origin binding, so the guard runs FIRST at document
//     start on every document: on any subframe or off-origin main frame it
//     synchronously deletes the host-wide bindings (including window.__webview__)
//     before any page script or storage seed runs. On an off-origin main frame
//     it also notifies the shell via __verdana_nav_violation (note: go-webview
//     runs bound Go callbacks asynchronously, so synchronous JS deletion is
//     what closes the race window until the shell disables RPCs and closes
//     the window).

// ExpectedOrigin reduces a shell-chosen url to lowercased scheme://host[:port]
// with default ports (:80 for http, :443 for https) stripped, matching
// location.origin. Empty for an empty or non-http(s) url.
func ExpectedOrigin(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return ""
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return ""
	}
	port := u.Port()
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}
	if port != "" {
		return scheme + "://" + net.JoinHostPort(host, port)
	}
	if strings.Contains(host, ":") {
		return scheme + "://[" + host + "]"
	}
	return scheme + "://" + host
}

// OriginGuardScript is the document-start check itself. It runs before any
// storage/theme/bridge script: sets window.__nappOrigin, strips host-wide
// bindings synchronously in subframes and off-origin documents, and reports
// off-origin main-frame navigations via __verdana_nav_violation.
func OriginGuardScript(expectedOrigin string) string {
	if expectedOrigin == "" {
		return ""
	}
	expected, _ := json.Marshal(expectedOrigin)
	return "(function(){" +
		"var expected = " + string(expected) + ";" +
		"window.__nappOrigin = expected;" +
		"var isTop = (window.top === window);" +
		"var ok = false;" +
		"try { ok = (location.origin === expected); } catch (e) {}" +
		"if (isTop && ok) return;" +
		"try { delete window.__bridge_rpc; } catch (e) {}" +
		"try { delete window.__verdana_prompt_answer; } catch (e) {}" +
		"if (isTop && !ok && typeof window.__verdana_nav_violation === 'function') {" +
		"try { window.__verdana_nav_violation(); } catch (e) {}" +
		"}" +
		"try { delete window.__verdana_nav_violation; } catch (e) {}" +
		"try { delete window.__webview__; } catch (e) {}" +
		"})();"
}

// GateToExpectedOrigin wraps a document-start script so it executes only in
// the top frame of the expected origin. Used to prevent leaking __nappStorage,
// __nappDomains, __nappTheme, or UIKit into foreign origins or subframes.
func GateToExpectedOrigin(script string) string {
	if script == "" {
		return ""
	}
	return "(function(){" +
		"if (window.top !== window) return;" +
		"if (window.__nappOrigin && location.origin !== window.__nappOrigin) return;" +
		script +
		"})();"
}
