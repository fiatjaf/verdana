package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func nappBaseDir(id string) string {
	return filepath.Join(verdanaDir, "napps", id)
}

func refreshInstalled() {
	stateMu.Lock()
	list := make([]Napp, 0, len(state.InstalledNapps))
	for _, n := range state.InstalledNapps {
		list = append(list, n)
	}
	stateMu.Unlock()
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })

	ui.mu.Lock()
	ui.installed = list
	ui.mu.Unlock()
	if gioWin != nil {
		gioWin.Invalidate()
	}
}

func setBusy(id string, busy bool) {
	ui.mu.Lock()
	if busy {
		ui.busy[id] = true
	} else {
		delete(ui.busy, id)
	}
	ui.mu.Unlock()
	if gioWin != nil {
		gioWin.Invalidate()
	}
}

func setFetchErr(msg string) {
	ui.mu.Lock()
	ui.fetchErr = msg
	ui.mu.Unlock()
	if gioWin != nil {
		gioWin.Invalidate()
	}
}

func installNapp(n Napp) {
	setBusy(n.ID, true)
	defer setBusy(n.ID, false)

	base := nappBaseDir(n.ID)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	servers := append(append([]string(nil), n.Servers...), defaultBlossomServers...)
	for _, p := range n.Paths {
		data, err := downloadBlob(ctx, servers, p.Sha256)
		if err != nil {
			os.RemoveAll(base)
			setFetchErr("install failed: " + err.Error())
			return
		}
		dest := filepath.Join(base, filepath.FromSlash(strings.TrimPrefix(p.Path, "/")))
		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			setFetchErr("install failed: " + err.Error())
			return
		}
		if err := os.WriteFile(dest, data, 0644); err != nil {
			setFetchErr("install failed: " + err.Error())
			return
		}
	}

	stateMu.Lock()
	if state.InstalledNapps == nil {
		state.InstalledNapps = make(map[string]Napp)
	}
	state.InstalledNapps[n.ID] = n
	saveState()
	stateMu.Unlock()

	refreshInstalled()
}

func uninstallNapp(id string) {
	setBusy(id, true)
	defer setBusy(id, false)

	os.RemoveAll(nappBaseDir(id))

	stateMu.Lock()
	delete(state.InstalledNapps, id)
	saveState()
	stateMu.Unlock()

	refreshInstalled()
}

func downloadBlob(ctx context.Context, servers []string, sha string) ([]byte, error) {
	var lastErr error = errors.New("no servers")
	for _, srv := range servers {
		srv = strings.TrimRight(srv, "/")
		if !strings.Contains(srv, "://") {
			srv = "https://" + srv
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv+"/"+sha, nil)
		if err != nil {
			lastErr = err
			continue
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = errors.New(srv + ": status " + resp.Status)
			continue
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != sha {
			lastErr = errors.New(srv + ": sha256 mismatch")
			continue
		}
		return data, nil
	}
	return nil, errors.New("could not fetch/verify " + sha + ": " + lastErr.Error())
}
