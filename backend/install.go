package backend

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// refreshInstalled republishes the installed list into the launcher state.
func refreshInstalled() {
	list := installedNapps()
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })

	ls.mu.Lock()
	ls.installed = list
	ls.mu.Unlock()
	notifyState()
}

// Install downloads a napp's files and records it as installed. Blocking:
// call it from a goroutine (progress shows up as IsBusy).
func Install(n Napp) {
	log.Info().Str("napp", n.ID).Str("name", n.Name).Msg("installing napp")
	setBusy(n.ID, true)
	defer setBusy(n.ID, false)

	base := nappBaseDir(n.ID)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	servers := n.blossomServers(ctx)
	if err := fetchNappAssets(ctx, n, base, servers); err != nil {
		log.Error().Err(err).Str("napp", n.ID).Msg("install failed")
		os.RemoveAll(base)
		SetFetchErr("install failed: " + err.Error())
		return
	}

	stateMu.Lock()
	if state.InstalledNapps == nil {
		state.InstalledNapps = make(map[string]Napp)
	}
	state.InstalledNapps[n.ID] = n
	saveState()
	stateMu.Unlock()

	refreshInstalled()
	log.Info().Str("napp", n.ID).Str("name", n.Name).Msg("install complete")
}

// Uninstall removes a napp's files and forgets it.
func Uninstall(id string) {
	log.Info().Str("napp", id).Msg("uninstalling napp")
	setBusy(id, true)
	defer setBusy(id, false)

	os.RemoveAll(nappBaseDir(id))

	stateMu.Lock()
	delete(state.InstalledNapps, id)
	saveState()
	stateMu.Unlock()

	refreshInstalled()
	log.Info().Str("napp", id).Msg("uninstall complete")
}

// maxParallelAssets caps how many of a napp's files are in flight at once, so
// a big napp doesn't open a connection per asset against the same server.
const maxParallelAssets = 6

// fetchNappAssets downloads every file of a napp into base. The assets go in
// parallel; the servers for any one asset are still tried in order, so a napp
// whose first server has everything is served entirely from there.
//
// The first failure cancels the rest: the install is lost either way, and
// there's no reason to keep pulling bytes for it.
func fetchNappAssets(ctx context.Context, n Napp, base string, servers []string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		firstErr error
	)
	sem := make(chan struct{}, maxParallelAssets)

	for _, p := range n.Paths {
		wg.Add(1)
		go func(p NappPath) {
			defer wg.Done()

			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}

			err := fetchNappAsset(ctx, servers, base, p)
			if err == nil {
				return
			}

			mu.Lock()
			defer mu.Unlock()
			if firstErr == nil {
				// what the others report from here on is just the
				// cancellation we are about to cause
				firstErr = err
				cancel()
			}
		}(p)
	}
	wg.Wait()

	return firstErr
}

// fetchNappAsset downloads one file and writes it where the napp expects it.
func fetchNappAsset(ctx context.Context, servers []string, base string, p NappPath) error {
	data, err := downloadBlob(ctx, servers, p.Sha256)
	if err != nil {
		return err
	}
	dest := filepath.Join(base, filepath.FromSlash(strings.TrimPrefix(p.Path, "/")))
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return fmt.Errorf("%s: %w", p.Path, err)
	}
	if err := os.WriteFile(dest, data, 0644); err != nil {
		return fmt.Errorf("%s: %w", p.Path, err)
	}
	return nil
}

// downloadBlob fetches a blob from the first server that has it and verifies
// it against its hash before returning it.
func downloadBlob(ctx context.Context, servers []string, sha string) ([]byte, error) {
	log.Debug().Str("sha256", sha).Int("servers", len(servers)).Msg("downloading blob")
	var lastErr error = errors.New("no servers")
	for _, srv := range servers {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv+"/"+sha, nil)
		if err != nil {
			lastErr = err
			continue
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			log.Debug().Str("server", srv).Err(err).Msg("blob download failed")
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
		log.Debug().Str("server", srv).Msg("blob downloaded and verified")
		return data, nil
	}
	return nil, errors.New("could not fetch/verify " + sha + ": " + lastErr.Error())
}
