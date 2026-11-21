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
	// Loop trim so inputs like " .." or " .bashrc" or "foo. " cannot smuggle
	// leading dots/spaces or trailing dots/spaces past a single pass.
	for {
		next := strings.TrimSpace(cleaned)
		next = strings.TrimLeft(next, ".")
		next = strings.TrimRight(next, ". ")
		if next == cleaned {
			break
		}
		cleaned = next
	}
	if len(cleaned) > 200 {
		cleaned = strings.TrimRight(cleaned[:200], ". ")
	}
	if cleaned == "" || cleaned == "." || cleaned == ".." {
		return "download"
	}
	// Avoid Windows reserved DOS device names (CON, PRN, AUX, NUL, COM1-9, LPT1-9).
	stem := cleaned
	if dot := strings.IndexByte(stem, '.'); dot >= 0 {
		stem = stem[:dot]
	}
	if isWindowsReservedName(stem) {
		cleaned = "_" + cleaned
	}
	return cleaned
}

func isWindowsReservedName(stem string) bool {
	upper := strings.ToUpper(strings.TrimSpace(stem))
	switch upper {
	case "CON", "PRN", "AUX", "NUL",
		"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return true
	}
	return false
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
func openExternalLink(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return errors.New("empty url")
	}
	for _, r := range raw {
		if r <= 0x20 || r == 0x7f {
			return errors.New("invalid characters in url")
		}
	}
	u, err := neturl.Parse(raw)
	if err != nil || u.Host == "" || u.Hostname() == "" {
		return errors.New("invalid url")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return errors.New("only http(s) links can be opened")
	}
	safe := u.String()
	log.Info().Str("url", safe).Msg("opening external link")
	return host.OpenLink(safe)
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
