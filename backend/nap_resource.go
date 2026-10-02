package backend

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// NAP-RESOURCE: bytes for a napplet that has no network. Its frame can show
// data: and blob: URLs only, so profile pictures, media and blossom blobs
// come through here: fetched by the launcher, on the public internet only,
// size- and time-boxed, typed by sniffing (never by what the server claims),
// and handed back as a Blob.
//
// Fetching on a napplet's behalf is a way out of its sandbox (a URL can
// carry data), so the first https fetch of a session asks the user, once,
// under the napplet's own "fetch" permission.

func init() {
	handleNap(map[string]napHandler{
		"resource.info":      napResourceInfo,
		"resource.bytes":     napResourceBytes,
		"resource.bytesMany": napResourceBytesMany,
		"resource.cancel":    napResourceCancel,
	})
}

const (
	resourceMaxBytes     = 10 << 20
	resourceMaxURLs      = 100
	resourceMaxServers   = 8
	resourceTimeout      = 30 * time.Second
	resourceMaxRedirects = 3
	resourceParallel     = 6
)

// resourceClient reaches public addresses only, on every hop.
var resourceClient = &http.Client{
	Timeout: resourceTimeout,
	Transport: &http.Transport{
		DialContext:           guardedDialContext,
		Proxy:                 nil,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          32,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
	},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= resourceMaxRedirects {
			return errors.New("too many redirects")
		}
		if req.URL.Scheme != "https" {
			return errors.New("redirect away from https")
		}
		return nil
	},
}

// resourceErr is a failure with its NAP-RESOURCE code.
type resourceErr struct {
	code, msg string
}

func (e *resourceErr) Error() string { return e.code + ": " + e.msg }

func rerr(code, msg string) *resourceErr { return &resourceErr{code, msg} }

type resourceResult struct {
	data []byte
	mime string
}

type resourceFetch struct {
	cancel context.CancelFunc
}

func napResourceInfo(c *napCall) {
	c.reply(map[string]any{"info": map[string]any{
		"schemes": []map[string]any{
			{"scheme": "data", "enabled": true},
			{"scheme": "https", "enabled": true},
			{"scheme": "blossom", "enabled": true},
			{"scheme": "nostr", "enabled": true},
			{"scheme": "http", "enabled": false},
		},
		"maxBytes":   resourceMaxBytes,
		"maxUrls":    resourceMaxURLs,
		"maxServers": resourceMaxServers,
	}})
}

// resourceTrack registers a request id for resource.cancel. A live id has one
// owner, so its completion cannot remove another request's registration.
func (c *napCall) resourceTrack() (context.Context, func(), bool) {
	ctx, cancel := context.WithCancel(c.ctx)
	id := string(c.ID)
	s := c.ci.nap
	entry := &resourceFetch{cancel: cancel}
	s.mu.Lock()
	if _, live := s.fetches[id]; live {
		s.mu.Unlock()
		cancel()
		return ctx, func() {}, false
	}
	s.fetches[id] = entry
	s.mu.Unlock()
	var once sync.Once
	return ctx, func() {
		once.Do(func() {
			s.mu.Lock()
			if s.fetches[id] == entry {
				delete(s.fetches, id)
			}
			s.mu.Unlock()
			cancel()
		})
	}, true
}

func napResourceCancel(c *napCall) {
	s := c.ci.nap
	s.mu.Lock()
	id := string(c.ID)
	entry := s.fetches[id]
	if entry != nil {
		delete(s.fetches, id)
	}
	s.mu.Unlock()
	if entry != nil {
		entry.cancel()
	}
}

func blobField(r resourceResult) map[string]any {
	return map[string]any{"__blob": map[string]any{
		"b64": base64.StdEncoding.EncodeToString(r.data), "mime": r.mime,
	}}
}

func napResourceBytes(c *napCall) {
	var r struct {
		URL     string   `json:"url"`
		Servers []string `json:"servers"`
	}
	if err := c.decode(&r); err != nil || r.URL == "" {
		c.replyAs("resource.bytes.error", map[string]any{"error": "invalid-request"})
		return
	}
	ctx, done, ok := c.resourceTrack()
	if !ok {
		c.replyAs("resource.bytes.error", map[string]any{"error": "duplicate-request"})
		return
	}
	c.async(func(context.Context) {
		defer done()
		res, err := fetchResource(ctx, c, r.URL, r.Servers)
		if err != nil {
			c.replyAs("resource.bytes.error", resourceErrFields(err))
			return
		}
		c.reply(map[string]any{"blob": blobField(res), "mime": res.mime})
	})
}

func napResourceBytesMany(c *napCall) {
	var r struct {
		URLs     []string `json:"urls"`
		Requests []struct {
			URL     string   `json:"url"`
			Servers []string `json:"servers"`
		} `json:"requests"`
	}
	if err := c.decode(&r); err != nil {
		c.replyAs("resource.bytesMany.error", map[string]any{"error": "invalid-request"})
		return
	}
	type req struct {
		url     string
		servers []string
	}
	reqs := []req{}
	for _, u := range r.URLs {
		reqs = append(reqs, req{url: u})
	}
	for _, q := range r.Requests {
		reqs = append(reqs, req{url: q.URL, servers: q.Servers})
	}
	if len(reqs) == 0 {
		c.replyAs("resource.bytesMany.error", map[string]any{"error": "invalid-request"})
		return
	}
	if len(reqs) > resourceMaxURLs {
		c.replyAs("resource.bytesMany.error", map[string]any{"error": "too-large"})
		return
	}

	ctx, done, ok := c.resourceTrack()
	if !ok {
		c.replyAs("resource.bytesMany.error", map[string]any{"error": "duplicate-request"})
		return
	}
	c.async(func(context.Context) {
		defer done()
		items := make([]map[string]any, len(reqs))
		sem := make(chan struct{}, resourceParallel)
		var wg sync.WaitGroup
		for i, q := range reqs {
			wg.Add(1)
			go func(i int, q req) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				res, err := fetchResource(ctx, c, q.url, q.servers)
				if err != nil {
					item := resourceErrFields(err)
					item["url"], item["ok"] = q.url, false
					items[i] = item
					return
				}
				items[i] = map[string]any{"url": q.url, "ok": true, "blob": blobField(res), "mime": res.mime}
			}(i, q)
		}
		wg.Wait()
		if ctx.Err() != nil {
			return // cancelled: the napplet stopped waiting
		}
		c.reply(map[string]any{"items": items})
	})
}

func resourceErrFields(err error) map[string]any {
	var re *resourceErr
	if errors.As(err, &re) {
		out := map[string]any{"error": re.code}
		if re.msg != "" {
			out["message"] = re.msg
		}
		return out
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return map[string]any{"error": "timeout"}
	}
	return map[string]any{"error": "network-error", "message": err.Error()}
}

// ─── fetching ────────────────────────────────────────────────────

func fetchResource(ctx context.Context, c *napCall, raw string, servers []string) (resourceResult, error) {
	raw = strings.TrimSpace(raw)
	switch {
	case strings.HasPrefix(raw, "data:"):
		return decodeDataURL(raw)
	case strings.HasPrefix(raw, "blossom:"):
		return fetchBlossomResource(ctx, c, strings.TrimPrefix(raw, "blossom:"), servers)
	case strings.HasPrefix(raw, "nostr:"):
		return fetchNostrResource(ctx, strings.TrimPrefix(raw, "nostr:"))
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return resourceResult{}, rerr("invalid-request", "not a url")
	}
	if u.Scheme != "https" {
		return resourceResult{}, rerr("unsupported-scheme", u.Scheme)
	}
	// a host that could never be fetched fails before anyone is asked; the
	// dialer checks again on every connection (DNS can change its mind)
	if err := publicHost(ctx, u.Hostname()); err != nil {
		return resourceResult{}, rerr("blocked-by-policy", "not a public address")
	}
	if !c.allowFetch() {
		return resourceResult{}, rerr("blocked-by-policy", "the user did not allow fetching")
	}
	return httpsResource(ctx, u.String())
}

// allowFetch asks, once per session, if the napplet may have the launcher
// download from the web.
func (c *napCall) allowFetch() bool {
	return c.sessionGrant(PermFetch, "download images and files from the web",
		"The napplet has no network of its own; the launcher fetches for it.")
}

func httpsResource(ctx context.Context, target string) (resourceResult, error) {
	ctx, cancel := context.WithTimeout(ctx, resourceTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return resourceResult{}, rerr("invalid-request", err.Error())
	}
	req.Header.Set("User-Agent", "verdana-napplet-resource")
	resp, err := resourceClient.Do(req)
	if err != nil {
		if errors.Is(err, errPrivateAddress) || strings.Contains(err.Error(), errPrivateAddress.Error()) {
			return resourceResult{}, rerr("blocked-by-policy", "not a public address")
		}
		if ctx.Err() != nil {
			return resourceResult{}, rerr("timeout", "")
		}
		return resourceResult{}, rerr("network-error", err.Error())
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
		return resourceResult{}, rerr("not-found", resp.Status)
	}
	if resp.StatusCode != http.StatusOK {
		return resourceResult{}, rerr("network-error", resp.Status)
	}
	if resp.ContentLength > resourceMaxBytes {
		return resourceResult{}, rerr("too-large", "")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, resourceMaxBytes+1))
	if err != nil {
		return resourceResult{}, rerr("network-error", err.Error())
	}
	if len(data) > resourceMaxBytes {
		return resourceResult{}, rerr("too-large", "")
	}
	mime, err := sniffResource(data, resp.Header.Get("Content-Type"))
	if err != nil {
		return resourceResult{}, err
	}
	return resourceResult{data: data, mime: mime}, nil
}

// sniffResource types bytes by their content. Only passive media and plain
// data go through: no HTML, no SVG (scriptable, and the spec wants it
// rasterized, which the launcher does not do), nothing executable.
func sniffResource(data []byte, declared string) (string, error) {
	mime := http.DetectContentType(data)
	base, _, _ := strings.Cut(mime, ";")
	switch {
	case strings.HasPrefix(base, "image/"), strings.HasPrefix(base, "audio/"),
		strings.HasPrefix(base, "video/"), strings.HasPrefix(base, "font/"):
		return base, nil
	case base == "application/ogg", base == "application/pdf":
		return base, nil
	case base == "application/octet-stream":
		if len(data) > 12 && string(data[4:8]) == "ftyp" {
			switch string(data[8:12]) {
			case "avif", "avis":
				return "image/avif", nil
			case "heic", "heix", "mif1":
				return "image/heic", nil
			}
			return "video/mp4", nil
		}
		return "", rerr("blocked-by-policy", "unrecognized content")
	case base == "text/plain":
		head := bytes.ToLower(data[:min(len(data), 512)])
		if bytes.Contains(head, []byte("<svg")) {
			return "", rerr("blocked-by-policy", "svg is not served")
		}
		d, _, _ := strings.Cut(strings.ToLower(declared), ";")
		if strings.TrimSpace(d) == "application/json" && json.Valid(data) {
			return "application/json", nil
		}
		return "text/plain; charset=utf-8", nil
	}
	return "", rerr("blocked-by-policy", "content type "+base+" is not served")
}

func decodeDataURL(raw string) (resourceResult, error) {
	meta, payload, ok := strings.Cut(strings.TrimPrefix(raw, "data:"), ",")
	if !ok {
		return resourceResult{}, rerr("invalid-request", "malformed data url")
	}
	var data []byte
	if strings.HasSuffix(meta, ";base64") {
		var err error
		if data, err = base64.StdEncoding.DecodeString(payload); err != nil {
			return resourceResult{}, rerr("decode-failed", err.Error())
		}
	} else {
		s, err := url.PathUnescape(payload)
		if err != nil {
			return resourceResult{}, rerr("decode-failed", err.Error())
		}
		data = []byte(s)
	}
	if len(data) > resourceMaxBytes {
		return resourceResult{}, rerr("too-large", "")
	}
	mime, err := sniffResource(data, strings.TrimSuffix(meta, ";base64"))
	if err != nil {
		return resourceResult{}, err
	}
	return resourceResult{data: data, mime: mime}, nil
}

// fetchBlossomResource gets blossom:sha256:<hex> from the servers the
// napplet suggested, the user's own, and the launcher's defaults, and only
// accepts bytes that hash right.
func fetchBlossomResource(ctx context.Context, c *napCall, ref string, hinted []string) (resourceResult, error) {
	if len(hinted) > resourceMaxServers {
		return resourceResult{}, rerr("too-large", "too many Blossom servers")
	}
	sha := strings.TrimPrefix(ref, "sha256:")
	sha, _, _ = strings.Cut(sha, ".")
	if !hex64.MatchString(sha) {
		return resourceResult{}, rerr("invalid-request", "not a sha256 blob reference")
	}
	servers := []string{}
	add := func(s string) {
		if u, err := url.Parse(s); err == nil && u.Scheme == "https" && u.Host != "" {
			o := "https://" + u.Host
			for _, have := range servers {
				if have == o {
					return
				}
			}
			servers = append(servers, o)
		}
	}
	for _, s := range hinted {
		add(s)
	}
	if pk, ok := currentUser(); ok && sys != nil {
		lctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		for _, s := range sys.FetchBlossomServerList(lctx, pk).Items {
			add(string(s))
		}
		cancel()
	}
	add("https://nostr.download")
	add("https://blossom.primal.net")

	var lastErr error = rerr("not-found", "")
	for _, srv := range servers {
		res, err := httpsBlobAttempt(ctx, srv+"/"+sha)
		if err != nil {
			lastErr = err
			continue
		}
		sum := sha256.Sum256(res.data)
		if hex.EncodeToString(sum[:]) != sha {
			lastErr = rerr("decode-failed", "hash mismatch")
			continue
		}
		return res, nil
	}
	return resourceResult{}, lastErr
}

// httpsBlobAttempt is httpsResource with its own deadline per server, so one
// stalling server doesn't eat the whole request.
func httpsBlobAttempt(ctx context.Context, target string) (resourceResult, error) {
	actx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return httpsResource(actx, target)
}

// fetchNostrResource resolves nostr:<nip19> one hop: the event itself, as
// JSON.
func fetchNostrResource(ctx context.Context, code string) (resourceResult, error) {
	if sys == nil {
		return resourceResult{}, rerr("network-error", "not ready")
	}
	raw, _ := json.Marshal(code)
	evt := loadEvent(ctx, raw, nil, "")
	if evt == nil {
		return resourceResult{}, rerr("not-found", "")
	}
	data, err := json.Marshal(evt)
	if err != nil {
		return resourceResult{}, rerr("decode-failed", err.Error())
	}
	return resourceResult{data: data, mime: "application/json"}, nil
}
