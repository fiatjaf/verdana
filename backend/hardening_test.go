package backend

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLinksRequireHTTPS(t *testing.T) {
	if IsLink("http://nostrapps.com/#app=naddr1abc") {
		t.Error("cleartext http://nostrapps.com should be rejected by IsLink")
	}
	if !IsLink("https://nostrapps.com/#app=naddr1abc") {
		t.Error("https://nostrapps.com should be accepted by IsLink")
	}
	if !IsLink("nostr:npub1abc") {
		t.Error("nostr: URI should be accepted by IsLink")
	}
	for _, bad := range []string{
		"http://nostrapps.com/#app=naddr1abc",
		"javascript://nostrapps.com/#app=naddr1abc",
		"file://nostrapps.com/#app=naddr1abc",
		"https://evil.example/#app=naddr1abc",
	} {
		if err := OpenLink(bad); err == nil {
			t.Errorf("OpenLink(%q) should have failed", bad)
		}
	}
}

func TestSanitizeFilenameEdgeCases(t *testing.T) {
	cases := map[string]string{
		" ..":             "download",
		" .":              "download",
		".. ":             "download",
		" .bashrc":        "bashrc",
		"  ..secret.txt ": "secret.txt",
		"report.txt. ":    "report.txt",
		"CON":             "_CON",
		"con.txt":         "_con.txt",
		"NUL.pdf":         "_NUL.pdf",
		"COM1.log":        "_COM1.log",
		"a/b/c.png":       "c.png",
		`a\b\..\evil.sh`:  "evil.sh",
		"normal-file.pdf": "normal-file.pdf",
	}
	for in, want := range cases {
		if got := SanitizeFilename(in); got != want {
			t.Errorf("SanitizeFilename(%q) = %q, want %q", in, got, want)
		}
	}
}

type linkRecordingHost struct {
	noopHost
	opened []string
}

func (h *linkRecordingHost) OpenLink(u string) error {
	h.opened = append(h.opened, u)
	return nil
}

func TestOpenExternalLinkValidation(t *testing.T) {
	rec := &linkRecordingHost{}
	oldHost := host
	host = rec
	t.Cleanup(func() { host = oldHost })

	for _, bad := range []string{
		"",
		"javascript:alert(1)",
		"file:///etc/passwd",
		"https://example.com/foo\nbar",
		"https://example.com/foo bar",
		"https:///no-host",
	} {
		if err := openExternalLink(bad); err == nil {
			t.Errorf("openExternalLink(%q) should have been rejected", bad)
		}
	}
	if len(rec.opened) != 0 {
		t.Fatalf("rejected links reached host.OpenLink: %v", rec.opened)
	}
	if err := openExternalLink("https://example.com/path?a=1"); err != nil {
		t.Errorf("openExternalLink valid https failed: %v", err)
	}
	if len(rec.opened) != 1 || rec.opened[0] != "https://example.com/path?a=1" {
		t.Fatalf("expected valid link to reach host.OpenLink, got %v", rec.opened)
	}
}

func TestShortIDEntropy(t *testing.T) {
	a, b := shortID(), shortID()
	if len(a) != 32 || len(b) != 32 || a == b {
		t.Fatalf("shortID produced weak/colliding tokens: %q, %q", a, b)
	}
}

func TestGoogleCallbackHandlerHostAndOriginGuards(t *testing.T) {
	addr := "127.0.0.1:44444"
	nonce := shortID()
	tokens := make(chan string, 1)
	h := googleCallbackHandler(addr, nonce, tokens)

	validToken := base64.StdEncoding.EncodeToString([]byte(`{"tags":[["email","alice@example.com"]]}`))

	// Wrong Host (DNS rebinding) -> 421
	req := httptest.NewRequest("POST", "http://evil.example:44444/"+nonce+"/token", strings.NewReader(validToken))
	req.Host = "evil.example:44444"
	req.Header.Set("Origin", "http://"+addr)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMisdirectedRequest {
		t.Fatalf("wrong Host status = %d, want 421", rec.Code)
	}

	// Cross-origin POST (CSRF) -> 403
	req = httptest.NewRequest("POST", "http://"+addr+"/"+nonce+"/token", strings.NewReader(validToken))
	req.Host = addr
	req.Header.Set("Origin", "https://evil.example")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin status = %d, want 403", rec.Code)
	}

	// Cross-site Sec-Fetch-Site -> 403
	req = httptest.NewRequest("POST", "http://"+addr+"/"+nonce+"/token", strings.NewReader(validToken))
	req.Host = addr
	req.Header.Set("Origin", "http://"+addr)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-site status = %d, want 403", rec.Code)
	}

	// Same-origin valid token -> 200
	req = httptest.NewRequest("POST", "http://"+addr+"/"+nonce+"/token", strings.NewReader(validToken))
	req.Host = addr
	req.Header.Set("Origin", "http://"+addr)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid POST status = %d, want 200", rec.Code)
	}
	select {
	case got := <-tokens:
		if got != validToken {
			t.Fatalf("got token %q, want %q", got, validToken)
		}
	default:
		t.Fatal("token was not delivered")
	}
}

func TestStorageAndRuleKeyCollisionResistance(t *testing.T) {
	if storageFileFor("pub~a/b") == storageFileFor("pub~a_b") {
		t.Fatal("distinct napp IDs collided on the same storage filename")
	}
	k1 := RuleKey{Napp: "a\x1fsign", Permission: PermCopyText}
	k2 := RuleKey{Napp: "a", Permission: PermSign, Subject: "copy_text"}
	if k1.ruleID() == k2.ruleID() {
		t.Fatal("RuleKey separator injection caused collision")
	}
}
