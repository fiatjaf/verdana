package backend

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	neturl "net/url"
	"path"
	"slices"
	"strconv"
	"strings"
)

// ─── JS literal helpers ──────────────────────────────────────────
// Everything we push into a webview goes through an eval, so every value we
// interpolate has to be a valid JS literal. json.Marshal is exactly that for
// strings (and it escapes quotes, newlines and unicode for us).

func jsString(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(b)
}

func jsNumber(n int) string { return strconv.Itoa(n) }

func jsBool(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// ─── saveFile ────────────────────────────────────────────────────

// SanitizeFilename keeps only a basename and drops anything that could steer
// where the file lands or confuse the OS. A napp filename is untrusted input,
// and the platforms writing it (both of them) start from this.
func SanitizeFilename(raw string) string {
	base := raw
	if i := strings.LastIndexAny(base, `/\`); i >= 0 {
		base = base[i+1:]
	}
	cleaned := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		switch r {
		case ':', '*', '?', '"', '<', '>', '|':
			return -1
		}
		return r
	}, base)
	cleaned = strings.TrimLeft(cleaned, ".") // no ".." and no accidental dotfiles
	cleaned = strings.TrimSpace(cleaned)
	if len(cleaned) > 200 {
		cleaned = cleaned[:200]
	}
	if cleaned == "" {
		return "download"
	}
	return cleaned
}

// saveFileForNapp hands the bytes a napp gave us to the platform. The data
// arrives base64-encoded because that's all a JSON rpc can carry (bridge.js
// encodes Blobs/ArrayBuffers before sending).
func saveFileForNapp(name string, dataB64 string) (map[string]any, error) {
	data, err := base64.StdEncoding.DecodeString(dataB64)
	if err != nil {
		return nil, errors.New("invalid file data")
	}
	saved, err := host.SaveFile(SanitizeFilename(name), data)
	if err != nil {
		return nil, err
	}
	log.Info().Str("name", saved).Int("bytes", len(data)).Msg("saved file for napp")
	return map[string]any{"name": saved, "size": len(data)}, nil
}

// ─── copyText ────────────────────────────────────────────────────

const maxCopyChars = 100_000

func copyTextForNapp(text string) (map[string]any, error) {
	if len(text) > maxCopyChars {
		return nil, errors.New("text is too long to copy")
	}
	if err := host.CopyText(text); err != nil {
		return nil, err
	}
	log.Info().Int("length", len(text)).Msg("copied text to the clipboard for napp")
	return map[string]any{"length": len(text)}, nil
}

// ─── link ────────────────────────────────────────────────────────

// openExternalLink hands a url to the platform's browser. Napps can't
// navigate out of their own webview, so this is the only way out — and it is
// behind an approval prompt in the bridge.
func openExternalLink(url string) error {
	url = strings.TrimSpace(url)
	if url == "" {
		return errors.New("empty url")
	}
	lower := strings.ToLower(url)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		return errors.New("only http(s) links can be opened")
	}
	log.Info().Str("url", url).Msg("opening external link")
	return host.OpenLink(url)
}

// imageExts are the extensions napp.link takes for an image.
var imageExts = []string{".png", ".jpg", ".jpeg", ".gif", ".webp", ".avif", ".bmp", ".svg"}

// isImageURL says, by its extension, whether an http(s) url is an image.
func isImageURL(raw string) bool {
	u, err := neturl.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	return slices.Contains(imageExts, strings.ToLower(path.Ext(u.Path)))
}

// showImage is napp.link for an image: rather than going straight out to
// the browser it is shown over the napp's window, with a button beside it to
// open it in the browser after all. Looking at it asks nothing — napps load
// images off the web anyway — so it is never remembered, and only a rule
// that refuses the napp its links keeps it from coming up.
func showImage(ci *Instance, url string) error {
	if rule, ok := lookupRule(RuleKey{Napp: ci.napp.ID, Permission: PermOpenLink}); ok && !rule.Decision.granted() {
		return errors.New("denied by the rules")
	}
	name := "A napp"
	if ci.napp.Label() != "" {
		name = ci.napp.Label()
	}
	p := newPrompt(name, name+" shows an image", PromptBody{Code: preview(url, 200), Image: url}, nil)
	p.Instance = ci.instance
	log.Info().Str("napp", name).Str("url", url).Msg("showing an image")
	enqueuePrompt(p)
	if !p.wait().OK {
		return nil
	}
	return openExternalLink(url)
}
