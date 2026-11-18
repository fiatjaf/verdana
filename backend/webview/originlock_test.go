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
		"https://napp.verdana":       "https://napp.verdana",
		"http://localhost:5173/app":  "http://localhost:5173",
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
		"__verdana_nav_violation",
		"window.top !== window",
		"location.origin",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("guard script missing %q", want)
		}
	}
	if OriginGuardScript("") != "" {
		t.Error("empty expected origin should give an empty script")
	}
}
