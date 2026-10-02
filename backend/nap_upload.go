package backend

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nipb7/blossom"
)

// NAP-UPLOAD is deliberately Blossom-only for now. The public NAP surface is
// rail-neutral, so NIP-96 can be added later without changing napplets.
const napUploadMaxBytes = 16 * 1024 * 1024

type napUploadStatus struct {
	OK           bool        `json:"ok"`
	UploadID     string      `json:"uploadId"`
	Status       string      `json:"status"`
	Rail         string      `json:"rail"`
	URL          string      `json:"url,omitempty"`
	FallbackURLs []string    `json:"fallbackUrls,omitempty"`
	SHA256       string      `json:"sha256,omitempty"`
	Size         int         `json:"size,omitempty"`
	MIMEType     string      `json:"mimeType,omitempty"`
	NIP94        []nostr.Tag `json:"nip94,omitempty"`
	Error        string      `json:"error,omitempty"`
	BytesSent    int         `json:"bytesSent,omitempty"`
	BytesTotal   int         `json:"bytesTotal,omitempty"`
	UpdatedAt    int64       `json:"updatedAt"`
	cancel       context.CancelFunc
}

type napUploadBlob struct {
	Blob struct {
		B64  string `json:"b64"`
		MIME string `json:"mime"`
	} `json:"__blob"`
}

type napUploadRequest struct {
	Rail        string          `json:"rail"`
	Data        napUploadBlob   `json:"data"`
	MIMEType    string          `json:"mimeType"`
	Filename    string          `json:"filename"`
	Caption     string          `json:"caption"`
	NoTransform bool            `json:"noTransform"`
	Metadata    json.RawMessage `json:"metadata"`
}

var (
	napUploadServers = func(ctx context.Context, pubkey nostr.PubKey) []string {
		if sys == nil {
			return nil
		}
		items := sys.FetchBlossomServerList(ctx, pubkey).Items
		servers := make([]string, 0, len(items))
		for _, item := range items {
			servers = append(servers, item.Value())
		}
		return servers
	}
	// napUploadAuth signs the Blossom (BUD-02) authorization for one blob.
	// ctx has no deadline: a remote signer may wait on the user for as long
	// as it likes.
	napUploadAuth = func(ctx context.Context, keyer nostr.Keyer, hash string) (string, error) {
		now := nostr.Now()
		evt := nostr.Event{
			Kind: 24242, CreatedAt: now, Content: "Upload blob",
			Tags: nostr.Tags{
				{"t", "upload"}, {"x", hash},
				// generous, since the user may take a while to sign: the
				// authorization is good for this one blob only
				{"expiration", strconv.FormatInt(int64(now)+napUploadAuthTTL, 10)},
			},
		}
		if err := keyer.SignEvent(ctx, &evt); err != nil {
			return "", err
		}
		j, err := json.Marshal(evt)
		if err != nil {
			return "", err
		}
		return "Nostr " + base64.StdEncoding.EncodeToString(j), nil
	}
	napUploadToServer = func(ctx context.Context, server string, data []byte, mimeType, auth string) (*blossom.BlobDescriptor, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPut, server+"/upload", bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", mimeType)
		req.Header.Set("Authorization", auth)
		resp, err := napUploadClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		if err != nil {
			return nil, err
		}
		if resp.StatusCode >= 300 {
			reason := resp.Header.Get("X-Reason")
			if reason == "" {
				reason = preview(string(body), 200)
			}
			return nil, fmt.Errorf("%s: %s", resp.Status, reason)
		}
		var descriptor blossom.BlobDescriptor
		if err := json.Unmarshal(body, &descriptor); err != nil {
			return nil, fmt.Errorf("unreadable blob descriptor: %w", err)
		}
		return &descriptor, nil
	}
)

const napUploadAuthTTL = 60 * 60

// napUploadClient has no overall deadline, since 16 MiB can take a while on a
// slow link. It only gives up on a server that stops answering.
var napUploadClient = &http.Client{
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 2 * time.Minute,
		IdleConnTimeout:       90 * time.Second,
	},
}

func init() {
	handleNap(map[string]napHandler{
		"upload.info":   napUploadInfo,
		"upload.upload": napUpload,
		"upload.status": napUploadGetStatus,
	})
}

func napUploadInfo(c *napCall) {
	enabled := userKeyer != nil && userPubkey != nostr.ZeroPK && sys != nil
	c.reply(map[string]any{"info": map[string]any{
		"rails": []map[string]any{{
			"rail": "blossom", "enabled": enabled, "returns": []string{"https", "blossom"},
		}},
		"maxBytes": napUploadMaxBytes,
	}})
}

func napUpload(c *napCall) {
	var envelope struct {
		Request napUploadRequest `json:"request"`
	}
	if err := c.decode(&envelope); err != nil {
		c.reply(map[string]any{"error": "invalid upload request"})
		return
	}
	r := envelope.Request
	if r.Rail != "" && r.Rail != "blossom" {
		c.reply(map[string]any{"error": "unsupported rail"})
		return
	}
	if userKeyer == nil || userPubkey == nostr.ZeroPK {
		c.reply(map[string]any{"error": "not-signed-in"})
		return
	}
	keyer, pubkey := userKeyer, userPubkey
	if len(r.Data.Blob.B64) == 0 || base64.StdEncoding.DecodedLen(len(r.Data.Blob.B64)) > napUploadMaxBytes {
		c.reply(map[string]any{"error": "file too large"})
		return
	}
	data, err := base64.StdEncoding.DecodeString(r.Data.Blob.B64)
	if err != nil {
		c.reply(map[string]any{"error": "invalid upload data"})
		return
	}
	if len(data) == 0 {
		c.reply(map[string]any{"error": "invalid upload data"})
		return
	}
	if len(data) > napUploadMaxBytes {
		c.reply(map[string]any{"error": "file too large"})
		return
	}
	mimeType, err := napUploadMIME(r.MIMEType, r.Data.Blob.MIME, r.Filename)
	if err != nil {
		c.reply(map[string]any{"error": "unsupported media type"})
		return
	}

	c.async(func(ctx context.Context) {
		lookupCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		servers := validUploadServers(napUploadServers(lookupCtx, pubkey))
		cancel()
		if len(servers) == 0 {
			c.reply(map[string]any{"error": "no server configured"})
			return
		}

		// Answer now, before the approval prompt: the shim gives up on
		// upload.upload after 30s, and the user may take longer than that to
		// find and answer the prompt. Everything after this is pushed as
		// upload.status.changed.
		uploadID := "upload-" + randomID()
		status := napUploadStatus{
			OK: true, UploadID: uploadID, Status: "pending", Rail: "blossom",
			BytesTotal: len(data), UpdatedAt: time.Now().Unix(),
		}
		napStoreUpload(c, &status)
		c.reply(map[string]any{"result": napUploadPublic(status)})

		detail := fmt.Sprintf("%s (%s, %s) to %d Blossom server(s): %s",
			napUploadFilename(r.Filename), mimeType, byteCount(len(data)), len(servers),
			preview(strings.Join(stripSchemes(servers), ", "), 180))
		if !askApproval(c.ci, PermUpload, "upload a public file", detail, preview(r.Caption, 200)) {
			status.OK = false
			status.Status = "cancelled"
			status.Error = "user cancelled"
			status.UpdatedAt = time.Now().Unix()
			napStoreUpload(c, &status)
			napPushUploadStatus(c, status)
			return
		}

		uploadCtx, uploadCancel := context.WithCancel(ctx)
		status.Status = "uploading"
		status.UpdatedAt = time.Now().Unix()
		status.cancel = uploadCancel
		napStoreUpload(c, &status)
		napPushUploadStatus(c, status)

		napRunUpload(uploadCtx, c, status, data, mimeType, r.Caption, servers, keyer)
	})
}

func napRunUpload(ctx context.Context, c *napCall, status napUploadStatus, data []byte, mimeType, caption string, servers []string, keyer nostr.Keyer) {
	defer func() {
		if status.cancel != nil {
			status.cancel()
		}
	}()
	sum := sha256.Sum256(data)
	wantHash := hex.EncodeToString(sum[:])
	// one signature covers every server: it names the blob, not the server,
	// so a remote signer is asked once
	auth, err := napUploadAuth(ctx, keyer, wantHash)
	if err != nil {
		log.Warn().Err(err).Str("napplet", c.ci.napp.ID).Msg("NAP-UPLOAD could not sign the Blossom authorization")
	}
	var descriptors []*blossom.BlobDescriptor
	for _, server := range servers {
		if err != nil || ctx.Err() != nil {
			break
		}
		descriptor, uploadErr := napUploadToServer(ctx, server, data, mimeType, auth)
		if uploadErr != nil || !validUploadDescriptor(descriptor, wantHash, len(data)) {
			log.Warn().Err(uploadErr).Str("server", server).Str("napplet", c.ci.napp.ID).
				Msg("NAP-UPLOAD server did not confirm the blob")
			continue
		}
		descriptors = append(descriptors, descriptor)
	}

	status.cancel = nil
	status.UpdatedAt = time.Now().Unix()
	if len(descriptors) == 0 {
		status.OK = false
		status.Status = "failed"
		status.Error = "upload failed"
		if err != nil {
			status.Error = "signing failed"
		}
		if errors.Is(ctx.Err(), context.Canceled) {
			status.Status = "cancelled"
			status.Error = "upload cancelled"
		}
		napStoreUpload(c, &status)
		napPushUploadStatus(c, status)
		return
	}

	primary := descriptors[0]
	status.OK = true
	status.Status = "complete"
	status.URL = primary.URL
	status.SHA256 = wantHash
	status.Size = len(data)
	status.MIMEType = mimeType
	status.BytesSent = len(data)
	for _, descriptor := range descriptors[1:] {
		if descriptor.URL != primary.URL {
			status.FallbackURLs = append(status.FallbackURLs, descriptor.URL)
		}
	}
	status.NIP94 = nostr.Tags{{"url", status.URL}, {"m", mimeType}, {"x", wantHash}, {"size", strconv.Itoa(len(data))}}
	for _, fallback := range status.FallbackURLs {
		status.NIP94 = append(status.NIP94, nostr.Tag{"fallback", fallback})
	}
	if caption != "" {
		status.NIP94 = append(status.NIP94, nostr.Tag{"alt", caption})
	}
	napStoreUpload(c, &status)
	napPushUploadStatus(c, status)
}

func napUploadGetStatus(c *napCall) {
	var r struct {
		UploadID string `json:"uploadId"`
	}
	_ = c.decode(&r)
	c.ci.nap.mu.Lock()
	status, ok := c.ci.nap.uploads[r.UploadID]
	var public napUploadStatus
	if ok {
		public = napUploadPublic(*status)
	}
	c.ci.nap.mu.Unlock()
	if !ok {
		c.reply(map[string]any{"error": "upload not found"})
		return
	}
	c.reply(map[string]any{"status": public})
}

func napStoreUpload(c *napCall, status *napUploadStatus) {
	s := c.ci.nap
	s.mu.Lock()
	if s.gen == c.gen {
		copy := *status
		s.uploads[status.UploadID] = &copy
	}
	s.mu.Unlock()
}

func napPushUploadStatus(c *napCall, status napUploadStatus) {
	c.ci.napPushGen(c.gen, map[string]any{"type": "upload.status.changed", "status": napUploadPublic(status)})
}

func napUploadPublic(status napUploadStatus) napUploadStatus {
	status.cancel = nil
	return status
}

func napUploadMIME(explicit, blob, filename string) (string, error) {
	value := strings.TrimSpace(explicit)
	if value == "" {
		value = strings.TrimSpace(blob)
	}
	if value == "" {
		value = mime.TypeByExtension(strings.ToLower(filepath.Ext(filename)))
	}
	if value == "" {
		return "application/octet-stream", nil
	}
	mediaType, _, err := mime.ParseMediaType(value)
	if err != nil || !strings.Contains(mediaType, "/") {
		return "", errors.New("invalid MIME type")
	}
	return strings.ToLower(mediaType), nil
}

func validUploadServers(values []string) []string {
	servers := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		u, err := url.Parse(strings.TrimSpace(value))
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
			continue
		}
		u.RawQuery, u.Fragment = "", ""
		server := strings.TrimRight(u.String(), "/")
		if !seen[server] {
			seen[server] = true
			servers = append(servers, server)
		}
	}
	return servers
}

func validUploadDescriptor(d *blossom.BlobDescriptor, hash string, size int) bool {
	if d == nil || !strings.EqualFold(d.SHA256, hash) || d.Size != size {
		return false
	}
	u, err := url.Parse(d.URL)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" && u.User == nil
}

func napUploadFilename(value string) string {
	name := filepath.Base(strings.TrimSpace(value))
	if name == "." || name == "" {
		return "a file"
	}
	return preview(name, 80)
}

func byteCount(n int) string {
	if n < 1024 {
		return fmt.Sprintf("%d bytes", n)
	}
	if n < 1024*1024 {
		return fmt.Sprintf("%.1f KiB", float64(n)/1024)
	}
	return fmt.Sprintf("%.1f MiB", float64(n)/(1024*1024))
}
