package backend

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nipb7/blossom"
)

// DevPublishFile describes one file that will become a path tag.
type DevPublishFile struct {
	Path   string
	Size   int64
	Sha256 string
}

// DevPublishInfo is the read-only data shown before publishing a dev napp.
type DevPublishInfo struct {
	Napp  Napp
	Files []DevPublishFile
}

// DevPublishDefaults returns useful initial targets for the publish form.
func DevPublishDefaults() (servers, relays []string) {
	servers = []string{"https://relay.nostrapps.com", "https://nostr.download"}
	relays = Relays()

	if sys != nil && userPubkey != nostr.ZeroPK {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		for _, server := range sys.FetchBlossomServerList(ctx, userPubkey).Items {
			servers = nostr.AppendUnique(servers, server.Value())
		}

		for _, relay := range sys.FetchWriteRelays(ctx, userPubkey) {
			relays = nostr.AppendUnique(relays, relay)
		}
	}

	return servers, relays
}

// DevPublishInfoFor reads current metadata and file sizes for a folder dev napp.
func DevPublishInfoFor(id string) (DevPublishInfo, error) {
	d := devLookup(id)
	if d == nil || d.source != "folder" {
		return DevPublishInfo{}, errors.New("dev napp is not a local folder")
	}
	napp, err := readDevFolder(d.dir)
	if err != nil {
		return DevPublishInfo{}, err
	}
	files := make([]DevPublishFile, 0, len(napp.Paths))
	for _, p := range napp.Paths {
		info, err := os.Stat(filepath.Join(d.dir, filepath.FromSlash(strings.TrimPrefix(p.Path, "/"))))
		if err != nil {
			return DevPublishInfo{}, err
		}
		files = append(files, DevPublishFile{Path: p.Path, Size: info.Size(), Sha256: p.Sha256})
	}
	return DevPublishInfo{Napp: napp, Files: files}, nil
}

// PublishDev uploads a folder dev napp, signs its kind-35130 manifest, and
// publishes it to selected relays. Every file must reach at least one server.
// Progress is reported line-by-line through onStep (may be nil).
func PublishDev(ctx context.Context, id string, servers, relays []string, protected bool, onStep func(string)) (int, int, error) {
	step := func(format string, args ...any) {
		if onStep != nil {
			onStep(fmt.Sprintf(format, args...))
		}
	}
	if userKeyer == nil {
		return 0, 0, errors.New("not logged in")
	}
	if sys == nil {
		return 0, 0, errors.New("system not ready")
	}
	d := devLookup(id)
	if d == nil || d.source != "folder" {
		return 0, 0, errors.New("dev napp is not a local folder")
	}
	if len(servers) == 0 {
		return 0, 0, errors.New("no Blossom servers selected")
	}
	if len(relays) == 0 {
		return 0, 0, errors.New("no relays selected")
	}
	napp, err := readDevFolder(d.dir)
	if err != nil {
		return 0, 0, err
	}

	okServers := make(map[string]bool)
	step("uploading %d file(s) to %d blossom server(s)...", len(napp.Paths), len(servers))
	for _, file := range napp.Paths {
		path := filepath.Join(d.dir, filepath.FromSlash(strings.TrimPrefix(file.Path, "/")))
		st, statErr := os.Stat(path)
		size := int64(-1)
		if statErr == nil {
			size = st.Size()
		}
		stored := false
		for _, server := range servers {
			if ctx.Err() != nil {
				return 0, 0, ctx.Err()
			}
			step("uploading %s (%d bytes) to %s ...", file.Path, size, server)
			f, openErr := os.Open(path)
			if openErr != nil {
				step("  failed: cannot open file: %v", openErr)
				continue
			}
			client := blossom.NewClient(server, userKeyer)
			descriptor, uploadErr := client.UploadBlob(ctx, f, mime.TypeByExtension(filepath.Ext(path)))
			f.Close()
			switch {
			case uploadErr != nil:
				step("  failed on %s: %v", server, uploadErr)
			case descriptor == nil:
				step("  failed on %s: empty response", server)
			case !strings.EqualFold(descriptor.SHA256, file.Sha256):
				step("  failed on %s: sha256 mismatch (got %s, want %s)", server, descriptor.SHA256, file.Sha256)
			default:
				step("  ok: stored on %s", server)
				stored = true
				okServers[server] = true
			}
		}
		if !stored {
			return 0, 0, fmt.Errorf("file %s failed to upload to every Blossom server", file.Path)
		}
	}

	tags := make(nostr.Tags, 0, len(napp.Paths)+len(napp.Actions)+len(napp.Requires)+8)
	for _, file := range napp.Paths {
		tags = append(tags, nostr.Tag{"path", file.Path, file.Sha256, mime.TypeByExtension(filepath.Ext(file.Path))})
	}
	serversForEvent := make([]string, 0, len(okServers))
	for _, server := range servers {
		if okServers[server] {
			serversForEvent = append(serversForEvent, server)
		}
	}
	for _, server := range serversForEvent {
		tags = append(tags, nostr.Tag{"server", server})
	}
	if napp.Name != "" {
		tags = append(tags, nostr.Tag{"title", napp.Name})
	}
	if napp.Description != "" {
		tags = append(tags, nostr.Tag{"description", napp.Description})
	}
	if napp.Icon != "" {
		tags = append(tags, nostr.Tag{"icon", napp.Icon})
	}
	for _, action := range napp.Actions {
		tags = append(tags, nostr.Tag{"action", action})
	}
	for _, requirement := range napp.Requires {
		tags = append(tags, nostr.Tag{"requires", requirement})
	}
	if napp.Singleton {
		tags = append(tags, nostr.Tag{"singleton"})
	}
	if napp.InitialSize != nil {
		if s, ok := sanitizeInitialSize(napp.InitialSize.Width, napp.InitialSize.Height); ok {
			tags = append(tags, nostr.Tag{"initial_size", strconv.Itoa(s.Width), strconv.Itoa(s.Height)})
		}
	}
	if protected {
		tags = append(tags, nostr.Tag{"-"})
	}
	tags = append(tags, nostr.Tag{"d", napp.D})
	step("signing kind:35130 event (%d tags, %d path tags)...", len(tags), len(napp.Paths))
	event := nostr.Event{Kind: 35130, CreatedAt: nostr.Now(), Tags: tags}
	if err := userKeyer.SignEvent(ctx, &event); err != nil {
		step("signing failed: %v", err)
		return 0, 0, fmt.Errorf("signing manifest: %w", err)
	}
	step("signed event %s, publishing to %d relay(s)...", event.ID, len(relays))

	results := 0
	failed := 0
	for result := range sys.Pool.PublishMany(ctx, relays, event) {
		if result.Error != nil {
			failed++
			step("  %s: failed: %v", result.RelayURL, result.Error)
			log.Warn().Str("relay", result.RelayURL).Err(result.Error).Msg("dev napp publish failed")
		} else {
			results++
			step("  %s: ok", result.RelayURL)
		}
	}
	step("done: published to %d relay(s), %d failed", results, failed)
	if results > 0 {
		if _, err := sys.Store.ReplaceEvent(event); err != nil {
			log.Warn().Err(err).Msg("failed to store published dev napp")
		}
		invalidateList(event.Kind, event.PubKey)
	}
	return results, failed, nil
}
