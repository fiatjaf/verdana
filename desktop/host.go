package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"verdana/backend"

	"gioui.org/io/system"
)

// gioHost is this launcher's answer to everything platform-shaped the backend
// needs: a napp window is an OS window backed by its own webview process, a
// redraw is a Gio invalidation, a download goes to ~/Downloads and a link
// goes to whatever the desktop uses to open links.
type gioHost struct{}

func (gioHost) OpenWindow(spec backend.WindowSpec) (backend.Transport, error) {
	return startChild(spec)
}

func (gioHost) StateChanged() {
	if gioWin != nil {
		gioWin.Invalidate()
	}
}

func (gioHost) OpenDiscovery(archetype string) {
	ui.mu.Lock()
	ui.tab = tabDiscovery
	ui.discoveryArchetype = archetype
	ui.mu.Unlock()
	if gioWin != nil {
		gioWin.Perform(system.ActionRaise)
		gioWin.Invalidate()
	}
}

func (gioHost) PromptsChanged() {
	if gioWin != nil {
		gioWin.Invalidate()
	}
}

// CopyText parks the text for the next Gio frame: writing to the clipboard is
// a frame command (clipboard.WriteCmd), not something an rpc goroutine can do
// on its own.
func (gioHost) CopyText(text string) error {
	ui.mu.Lock()
	ui.clipboard = append(ui.clipboard, text)
	ui.mu.Unlock()
	if gioWin != nil {
		gioWin.Invalidate()
	}
	return nil
}

// SaveFile writes into the user's download directory, never clobbering:
// file.txt, file-1.txt, file-2.txt…
func (gioHost) SaveFile(name string, data []byte) (string, error) {
	dir := downloadsDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}

	dest := filepath.Join(dir, name)
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 1; ; i++ {
		if _, err := os.Stat(dest); os.IsNotExist(err) {
			break
		}
		if i > 999 {
			return "", errors.New("could not find a free filename")
		}
		dest = filepath.Join(dir, stem+"-"+strconv.Itoa(i)+ext)
	}

	if err := os.WriteFile(dest, data, 0644); err != nil {
		return "", err
	}
	log.Info().Str("path", dest).Int("bytes", len(data)).Msg("saved file for napp")
	return filepath.Base(dest), nil
}

func (gioHost) SaveFileTarget() string { return downloadsDir() }

// AmberRequest is a NIP-55 phone-signer concept: the desktop has no signer
// app to reach, so there is nothing to launch.
func (gioHost) AmberRequest(string, string, string, string, string, string) bool {
	return false
}

// CreateShortcutFile writes an OS shortcut whose whole job is calling
// verdana with one quoted argument: the bundle token. The OS-specific file
// shapes live in the shortcutfile_<goos>.go files.
func (gioHost) CreateShortcutFile(name, token string) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return writeShortcutFile(name, exe, token)
}

func (gioHost) DeleteShortcutFile(path string) error {
	if err := deleteShortcutFile(path); err != nil {
		return err
	}
	// refresh what the desktop environment has indexed, when one exists
	refreshShortcutParent(filepath.Dir(path))
	log.Info().Str("path", path).Msg("removed shortcut file")
	return nil
}

// ListShortcutFiles reads back every verdana shortcut in the OS shortcut
// folder, the file being the whole record: the bundle's name, and the token it
// runs.
func (gioHost) ListShortcutFiles() []backend.ShortcutFile {
	files := listShortcutFiles()
	log.Info().Int("count", len(files)).Msg("shortcut files found")
	return files
}

func (gioHost) OpenLink(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}

// downloadsDir is where saveFile writes: the user's XDG download directory
// when it exists, the home directory otherwise.
func downloadsDir() string {
	if dir := strings.TrimSpace(os.Getenv("XDG_DOWNLOAD_DIR")); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return backend.DataDir()
	}
	candidate := filepath.Join(home, "Downloads")
	if st, err := os.Stat(candidate); err == nil && st.IsDir() {
		return candidate
	}
	return home
}
