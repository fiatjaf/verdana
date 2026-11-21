package main

import (
	"strings"
	"testing"
)

func TestOriginOf(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"http://127.0.0.1:41234/", "http://127.0.0.1:41234"},
		{"HTTP://LocalHost:80/index.html", "http://localhost"},
		{"https://EXAMPLE.COM:443/app", "https://example.com"},
		{"https://example.com:8443/app", "https://example.com:8443"},
		{"http://[::1]:80/test", "http://[::1]"},
		{"http://[::1]:8080/test", "http://[::1]:8080"},
		{"https://[::1]:443/test", "https://[::1]"},
		{"javascript:alert(1)", ""},
		{"file:///etc/passwd", ""},
		{"", ""},
	}
	for _, tc := range cases {
		if got := originOf(tc.in); got != tc.want {
			t.Errorf("originOf(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestBuildInitScriptGatesStorageAndSubframes(t *testing.T) {
	script := buildInitScript(
		"inst1",
		"http://127.0.0.1:9999",
		[]string{"ui"},
		`window.__nappStorage = {"secret":"token"};`,
		`window.__nappTheme = {name:"light",vars:{}};`,
	)
	guardIdx := strings.Index(script, "return;")
	storageIdx := strings.Index(script, "__nappStorage")
	webviewDelIdx := strings.Index(script, "delete window.__webview__")
	if guardIdx < 0 || storageIdx < 0 || guardIdx >= storageIdx {
		t.Fatalf("storage seed must appear after origin-guard return; guardIdx=%d storageIdx=%d", guardIdx, storageIdx)
	}
	if webviewDelIdx < 0 || webviewDelIdx >= guardIdx {
		t.Fatalf("expected delete window.__webview__ inside origin guard; idx=%d guardIdx=%d", webviewDelIdx, guardIdx)
	}
	if !strings.Contains(script, "window.top === window") {
		t.Fatalf("expected subframe guard in init script")
	}
}
