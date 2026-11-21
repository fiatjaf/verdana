package webview

import (
	"strings"
	"testing"
)

func TestExpectedOrigin(t *testing.T) {
	cases := map[string]string{
		"http://127.0.0.1:41303":     "http://127.0.0.1:41303",
		"http://127.0.0.1:41303/":    "http://127.0.0.1:41303",
		"http://127.0.0.1:41303/a/b": "http://127.0.0.1:41303",
		"HTTP://LOCALHOST:80/app":    "http://localhost",
		"https://NAPP.VERDANA:443/":  "https://napp.verdana",
		"https://napp.verdana":       "https://napp.verdana",
		"http://localhost:5173/app":  "http://localhost:5173",
		"http://[::1]:80/x":          "http://[::1]",
		"http://[::1]:8080/x":        "http://[::1]:8080",
		"https://[::1]:443/x":        "https://[::1]",
		"javascript:alert(1)":        "",
		"file:///etc/passwd":         "",
		"":                           "",
		"   ":                        "",
		"not a url":                  "",
		"http://":                    "",
	}
	for in, want := range cases {
		if got := ExpectedOrigin(in); got != want {
			t.Errorf("ExpectedOrigin(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOriginGuardScript(t *testing.T) {
	s := OriginGuardScript("http://127.0.0.1:41303")
	for _, want := range []string{
		`"http://127.0.0.1:41303"`,
		"window.__nappOrigin",
		"__verdana_nav_violation",
		"delete window.__bridge_rpc",
		"delete window.__webview__",
		"window.top === window",
		"location.origin",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("guard script missing %q", want)
		}
	}
	if OriginGuardScript("") != "" {
		t.Error("empty expected origin should give an empty script")
	}
	gated := GateToExpectedOrigin("window.__nappStorage = {};")
	if !strings.Contains(gated, "window.top !== window") || !strings.Contains(gated, "window.__nappOrigin") {
		t.Errorf("gated script missing origin/top checks: %s", gated)
	}
}
