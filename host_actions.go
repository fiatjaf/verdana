package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// ─── JS literal helpers ──────────────────────────────────────────
// Everything we push into a webview goes through Eval, so every value we
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

// sanitizeFilename keeps only a basename and drops anything that could steer
// where the file lands or confuse the OS. A napp filename is untrusted input.
func sanitizeFilename(raw string) string {
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

// downloadsDir is where saveFile writes: the user's XDG download directory
// when it exists, the home directory otherwise.
func downloadsDir() string {
	if dir := strings.TrimSpace(os.Getenv("XDG_DOWNLOAD_DIR")); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return verdanaDir
	}
	candidate := filepath.Join(home, "Downloads")
	if st, err := os.Stat(candidate); err == nil && st.IsDir() {
		return candidate
	}
	return home
}

// saveFileForNapp writes the bytes a napp handed over. The data arrives
// base64-encoded because that's all a JSON rpc can carry (bridge.js encodes
// Blobs/ArrayBuffers before sending).
func saveFileForNapp(name string, dataB64 string) (map[string]any, error) {
	data, err := base64.StdEncoding.DecodeString(dataB64)
	if err != nil {
		return nil, errors.New("invalid file data")
	}
	safe := sanitizeFilename(name)
	dir := downloadsDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}

	// never clobber: file.txt, file-1.txt, file-2.txt…
	dest := filepath.Join(dir, safe)
	ext := filepath.Ext(safe)
	stem := strings.TrimSuffix(safe, ext)
	for i := 1; ; i++ {
		if _, err := os.Stat(dest); os.IsNotExist(err) {
			break
		}
		if i > 999 {
			return nil, errors.New("could not find a free filename")
		}
		dest = filepath.Join(dir, stem+"-"+strconv.Itoa(i)+ext)
	}

	if err := os.WriteFile(dest, data, 0644); err != nil {
		return nil, err
	}
	log.Info().Str("path", dest).Int("bytes", len(data)).Msg("saved file for napp")
	return map[string]any{"name": filepath.Base(dest), "size": len(data)}, nil
}

// ─── copyText ────────────────────────────────────────────────────

const maxCopyChars = 100_000

// copyTextForNapp parks the text for the next Gio frame: writing to the
// clipboard is a frame command (clipboard.WriteCmd), not something an rpc
// goroutine can do on its own.
func copyTextForNapp(text string) (map[string]any, error) {
	if len(text) > maxCopyChars {
		return nil, errors.New("text is too long to copy")
	}
	ui.mu.Lock()
	ui.clipboard = append(ui.clipboard, text)
	ui.mu.Unlock()
	if gioWin != nil {
		gioWin.Invalidate()
	}
	log.Info().Int("length", len(text)).Msg("copied text to the clipboard for napp")
	return map[string]any{"length": len(text)}, nil
}

// ─── link ────────────────────────────────────────────────────────

// openExternalLink hands a url to the user's browser. Napps can't navigate
// out of their own webview, so this is the only way out — and it is behind an
// approval prompt in the bridge.
func openExternalLink(url string) error {
	url = strings.TrimSpace(url)
	if url == "" {
		return errors.New("empty url")
	}
	lower := strings.ToLower(url)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		return errors.New("only http(s) links can be opened")
	}

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	log.Info().Str("url", url).Msg("opening external link")
	return cmd.Start()
}
