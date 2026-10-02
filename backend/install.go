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
	"strings"
	"sync"
	"time"
)

// refreshInstalled republishes the installed list into the launcher state.
// Ordering is installedNapps' business (most recently launched first), and the
// discovery list gets resorted around the new set: an install or uninstall
// moves its napp between the top and bottom halves of that list.
func refreshInstalled() {
	ls.mu.Lock()
	ls.installed = installedNapps()
	ls.sortDiscovery()
	ls.mu.Unlock()
	notifyState()
}

// Install downloads a napp's files and records it as installed. Blocking:
// call it from a goroutine (progress shows up as IsBusy). It also takes
// updates: an already-installed napp is simply re-downloaded over.
func Install(n Napp) {
	log.Info().Str("napp", n.ID).Str("name", n.Name).Msg("installing napp")
	setBusy(n.ID, true)
	defer setBusy(n.ID, false)

	base := nappBaseDir(n.ID)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	servers := n.BlossomServers(ctx)
	if err := replaceNappFiles(ctx, n, base, servers); err != nil {
		log.Error().Err(err).Str("napp", n.ID).Msg("install failed")
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

// Uninstall removes a napp's files and forgets it. Installed-only by
// convention: it silently no-ops for ids the launcher doesn't know.
func Uninstall(id string) {
	log.Info().Str("napp", id).Msg("uninstalling napp")
	setBusy(id, true)
	defer setBusy(id, false)

	os.RemoveAll(nappBaseDir(id))

	stateMu.Lock()
	delete(state.InstalledNapps, id)
	delete(state.LastLaunched, id)
	saveState()
	stateMu.Unlock()

	// a napp that isn't installed can't be anyone's habitual handler, and
	// whatever the next one installed under that id shouldn't inherit it
	forgetActionUsage(id)

	refreshInstalled()
	log.Info().Str("napp", id).Msg("uninstall complete")
}

// InstallFromDiscovery resolves an id the launcher knows — installed or just
// discovered — into a napp and installs (or updates) it.
func InstallFromDiscovery(id string) bool {
	if n, ok := InstalledNapp(id); ok {
		go Install(n)
		return true
	}
	if n, ok := DiscoveredNapp(id); ok {
		go Install(n)
		return true
	}
	return false
}

// maxParallelAssets caps how many of a napp's files are in flight at once, so
// a big napp doesn't open a connection per asset against the same server.
const maxParallelAssets = 6

// replaceNappFiles downloads every file of a napp into a fresh directory next
// to base and only then puts it in base's place, so a download that fails half
// way leaves whatever was installed there untouched (and a fresh install
// leaves nothing behind).
func replaceNappFiles(ctx context.Context, n Napp, base string, servers []string) error {
	parent := filepath.Dir(base)
	if err := os.MkdirAll(parent, 0755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(parent, ".install-*")
	if err != nil {
		return err
	}
	if err := fetchNappAssets(ctx, n, tmp, servers); err != nil {
		os.RemoveAll(tmp)
		return err
	}

	old := ""
	if _, err := os.Stat(base); err == nil {
		old = tmp + ".old"
		if err := os.Rename(base, old); err != nil {
			os.RemoveAll(tmp)
			return err
		}
	}
	if err := os.Rename(tmp, base); err != nil {
		if old != "" {
			os.Rename(old, base)
		}
		os.RemoveAll(tmp)
		return err
	}
	if old != "" {
		os.RemoveAll(old)
	}
	return nil
}

// fetchNappAssets downloads every file of a napp into base. The assets go in
// parallel; the servers for any one asset are still tried in order, so a napp
// whose first server has everything is served entirely from there.
//
// A server that is down costs nothing: its attempt is skipped and the next
// one is tried. A file that no server can produce does fail the whole napp —
// it would be an install that cannot start — but only after every other
// asset had its own chance, and the error names what went missing.
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
		// the file, not the hash: an install or update that failed has to
		// say which of the napp's files nobody could serve
		return fmt.Errorf("%s: %w", p.Path, err)
	}
	rel := filepath.FromSlash(strings.TrimPrefix(p.Path, "/"))
	if !filepath.IsLocal(rel) {
		// the path comes from the napp's event: never let it write outside
		// the napp's own directory
		return fmt.Errorf("%s: path escapes the napp directory", p.Path)
	}
	dest := filepath.Join(base, rel)
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return fmt.Errorf("%s: %w", p.Path, err)
	}
	if err := os.WriteFile(dest, data, 0644); err != nil {
		return fmt.Errorf("%s: %w", p.Path, err)
	}
	return nil
}

// blobAttemptTimeout bounds a single server attempt. Without it a server that
// accepts the connection and then stalls holds the download until the whole
// context is gone, taking the other servers down with it: one bad server
// failed the entire napp instead of being skipped for the next one.
var blobAttemptTimeout = 20 * time.Second

// downloadBlob fetches a blob from the first server that has it and verifies
// it against its hash before returning it. Every server gets its own deadline,
// so an unreachable or stalling one is skipped rather than waited out.
func downloadBlob(ctx context.Context, servers []string, sha string) ([]byte, error) {
	log.Debug().Str("sha256", sha).Int("servers", len(servers)).Msg("downloading blob")
	var lastErr error = errors.New("no servers")
	for _, srv := range servers {
		attemptCtx, cancel := context.WithTimeout(ctx, blobAttemptTimeout)

		req, err := http.NewRequestWithContext(attemptCtx, http.MethodGet, srv+"/"+sha, nil)
		if err != nil {
			cancel()
			lastErr = err
			continue
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			cancel()
			log.Debug().Str("server", srv).Err(err).Msg("blob download failed")
			lastErr = err
			continue
		}
		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		cancel()
		if err != nil {
			log.Debug().Str("server", srv).Err(err).Msg("blob download failed")
			lastErr = err
			continue
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = errors.New(srv + ": status " + resp.Status)
			log.Debug().Str("server", srv).Str("last_error", lastErr.Error()).
				Msg("blob server does not have it, trying the next")
			continue
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != sha {
			lastErr = errors.New(srv + ": sha256 mismatch")
			log.Debug().Str("server", srv).Str("last_error", lastErr.Error()).
				Msg("blob server sent the wrong bytes, trying the next")
			continue
		}
		log.Debug().Str("server", srv).Msg("blob downloaded and verified")
		return data, nil
	}
	return nil, errors.New("could not fetch/verify " + sha + ": " + lastErr.Error())
}
