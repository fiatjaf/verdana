package wasmhost

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"

	"verdana/backend/wasm"
)

// http_fetch and http_fetch_image: the web, for a napp with no page.
//
// A web napp reaches the web with its own fetch(). A wasm napp has no fetch
// and no sockets, so this window makes the request for it and hands back
// what came: the raw response for http_fetch, and for http_fetch_image the
// picture already decoded, so a napp can show an avatar without carrying a
// png, jpeg, gif and webp decoder in its module. Both run beside the frame
// loop and answer through the inbox like everything else.

// maxBody is the biggest body either call reads, so a napp cannot fill
// memory.
const maxBody = 16 << 20

// maxSide is the longest side a decoded image is handed over at, absent a
// size.
const maxSide = 1024

// maxMegapixels is the largest decoded image http_fetch_image will accept,
// in 1024x1024 units: a decompression bomb's compressed bytes fit the 16MB
// cap easily, so the header is what has to be checked. 64MP bounds the
// decoder's allocation to a few hundred MB worst case.
const maxMegapixels = 64

// fetchDialContext is the dialer http_fetch's client uses. It is a variable
// so tests can serve from local httptest servers without the guard in the
// way; the real value keeps loopback and link-local out of reach.
var fetchDialContext = guardedDial

// testAllowedAddr, set by tests, is the one address the guard lets through
// despite being loopback: their own server's. Everything else — including
// where that server redirects — goes through the full check, which is how
// the redirect re-checking is tested end to end.
//
// A plain var, mutated by tests and read from dial goroutines: wasmhost's
// tests must not run parallel (none do — no t.Parallel in the package), or
// this and fetchDialContext need to become atomic.
var testAllowedAddr string

var httpClient = &http.Client{
	Timeout: 20 * time.Second,
	// fetches run on the host's network stack, so a wasm napp gets the
	// machine's own reach: dial-time checks keep loopback and link-local
	// addresses out of reach (this machine's own services — the launcher's
	// local ports — and cloud metadata endpoints), however the url spelled
	// them. Private LAN ranges stay reachable: self-hosted relays and
	// blossom servers are a legitimate use.
	Transport: &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return fetchDialContext(ctx, network, addr)
		},
	},
}

// guardedDial is net.Dialer.DialContext with an address check after
// resolution: whatever the hostname resolved to, the connection itself
// must not target loopback or link-local space. Checking here — per
// connection, not per url — is what keeps a redirect's target to the same
// rules as the fetch's own target.
//
// The test-allowance shortcut must stay ahead of the parse: it compares the
// raw dial address against the test's server, and in production
// testAllowedAddr is empty so the shortcut is unreachable and every dial
// goes through the full check below.
func guardedDial(ctx context.Context, network, addr string) (net.Conn, error) {
	if addr == testAllowedAddr {
		return (&net.Dialer{Timeout: 20 * time.Second}).DialContext(ctx, network, addr)
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, &net.DNSError{Err: "no addresses", Name: host, IsNotFound: true}
	}
	for _, ip := range ips {
		if isForbiddenIP(ip.IP) {
			return nil, errors.New("http_fetch: " + host + " resolves to a local address napps may not reach")
		}
	}
	return (&net.Dialer{Timeout: 20 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
}

// isForbiddenIP reports addresses a napp's fetch must not touch: this
// machine (loopback) and the link-local ranges where DHCP networks and
// cloud metadata endpoints live.
func isForbiddenIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}

// httpCall answers http_fetch and http_fetch_image; ok is false for every
// other method.
func (b *Bridge) httpCall(id int32, method string, params []byte) (ok bool) {
	switch method {
	case "http_fetch":
		go func() {
			result, err := httpFetch(params)
			b.answer(id, result, err)
		}()
	case "http_fetch_image":
		go func() {
			bitmap, err := httpFetchImage(params)
			if err != nil {
				b.answer(id, nil, err)
				return
			}
			b.push(Message{Kind: wasm.MsgBitmap, ID: id, Data: bitmap})
		}()
	default:
		return false
	}
	return true
}

type httpFetchParams struct {
	URL     string            `json:"url"`
	Method  string            `json:"method"`
	Headers map[string]string `json:"headers"`
	// Body is utf-8 text; a napp sending bytes base64s them itself.
	Body *string `json:"body"`
}

type httpFetchResult struct {
	Status  int               `json:"status"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	// Body is the response body, base64: JSON has no bytes.
	Body string `json:"body"`
}

func httpFetch(params []byte) (*httpFetchResult, error) {
	var p httpFetchParams
	if err := unmarshalURLParams(params, &p.URL, &p); err != nil {
		return nil, errors.New("http_fetch: params must be {url, method?, headers?, body?}")
	}
	target, err := webURL(p.URL)
	if err != nil {
		return nil, err
	}
	method := strings.ToUpper(strings.TrimSpace(p.Method))
	if method == "" {
		method = http.MethodGet
	}
	var body io.Reader
	if p.Body != nil {
		body = strings.NewReader(*p.Body)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, target, body)
	if err != nil {
		return nil, fmt.Errorf("http_fetch: %w", err)
	}
	for k, v := range p.Headers {
		req.Header.Set(k, v)
	}

	res, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http_fetch: %s could not be reached (%w)", target, err)
	}
	defer res.Body.Close()
	raw, err := readCapped(res.Body)
	if err != nil {
		return nil, err
	}

	headers := make(map[string]string, len(res.Header))
	for k, v := range res.Header {
		headers[strings.ToLower(k)] = strings.Join(v, ", ")
	}
	return &httpFetchResult{
		Status:  res.StatusCode,
		URL:     res.Request.URL.String(),
		Headers: headers,
		Body:    base64.StdEncoding.EncodeToString(raw),
	}, nil
}

type httpImageParams struct {
	URL    string  `json:"url"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
	// Fit is "contain" (the default), which fits inside width×height, or
	// "cover", which fills it exactly.
	Fit string `json:"fit"`
}

// httpFetchImage is the image at the url, decoded and scaled, as the bitmap
// message's bytes: width and height as little-endian uint32s, then RGBA rows
// top-down, alpha not premultiplied.
func httpFetchImage(params []byte) ([]byte, error) {
	var p httpImageParams
	if err := unmarshalURLParams(params, &p.URL, &p); err != nil {
		return nil, errors.New("http_fetch_image: params must be {url, width?, height?, fit?}")
	}
	target, err := webURL(p.URL)
	if err != nil {
		return nil, err
	}
	res, err := httpClient.Get(target)
	if err != nil {
		return nil, fmt.Errorf("http_fetch_image: %s could not be reached (%w)", target, err)
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return nil, fmt.Errorf("http_fetch_image: HTTP %d", res.StatusCode)
	}
	raw, err := readCapped(res.Body)
	if err != nil {
		return nil, err
	}

	// decompression-bomb gate, before decode: the 16MB cap above bounds the
	// compressed bytes, not the pixels they expand to. DecodeConfig reads
	// only the header, so a 16MB PNG declaring a hundred-million-pixel
	// canvas is refused before the decoder allocates for it. (Checking
	// src.Bounds after image.Decode would be too late - the allocation
	// happens inside the decode.)
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return nil, errors.New("http_fetch_image: not an image this launcher can decode")
	}
	if int64(cfg.Width)*int64(cfg.Height) > maxMegapixels<<20 {
		return nil, fmt.Errorf("http_fetch_image: image is too large to decode (%dx%d)", cfg.Width, cfg.Height)
	}

	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, errors.New("http_fetch_image: not an image this launcher can decode")
	}
	sb := src.Bounds()
	if sb.Dx() <= 0 || sb.Dy() <= 0 {
		return nil, errors.New("http_fetch_image: the image is empty")
	}
	from, w, h := placement(sb, p)

	// NRGBA is unpremultiplied, which is what the message carries; draw
	// converts whatever the decoder produced on the way in.
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, from, draw.Src, nil)

	out := make([]byte, 8+len(dst.Pix))
	binary.LittleEndian.PutUint32(out[0:], uint32(w))
	binary.LittleEndian.PutUint32(out[4:], uint32(h))
	copy(out[8:], dst.Pix)
	return out, nil
}

// placement is which part of the source gets drawn and at what size. contain
// scales the whole image into the box, never up; cover scales it to fill the
// box and crops what overhangs, evenly on both sides — a square avatar out of
// a portrait photo. A box with one side left out is a square.
func placement(sb image.Rectangle, p httpImageParams) (image.Rectangle, int, int) {
	side := func(v float64) int {
		if math.IsNaN(v) || v < 1 {
			return 0
		}
		return int(min(math.Floor(v), 4096))
	}
	bw, bh := side(p.Width), side(p.Height)
	sw, sh := float64(sb.Dx()), float64(sb.Dy())

	if p.Fit == "cover" && (bw > 0 || bh > 0) {
		if bw == 0 {
			bw = bh
		}
		if bh == 0 {
			bh = bw
		}
		scale := max(float64(bw)/sw, float64(bh)/sh)
		cw := min(sw, float64(bw)/scale)
		ch := min(sh, float64(bh)/scale)
		x0 := sb.Min.X + int(math.Round((sw-cw)/2))
		y0 := sb.Min.Y + int(math.Round((sh-ch)/2))
		crop := image.Rect(x0, y0, x0+max(1, int(math.Round(cw))), y0+max(1, int(math.Round(ch))))
		return crop.Intersect(sb), bw, bh
	}

	if bw == 0 {
		bw = maxSide
	}
	if bh == 0 {
		bh = maxSide
	}
	scale := min(1, float64(bw)/sw, float64(bh)/sh)
	return sb, max(1, int(math.Round(sw*scale))), max(1, int(math.Round(sh*scale)))
}

// unmarshalURLParams reads params that are either a bare url string or an
// object.
func unmarshalURLParams(params []byte, u *string, into any) error {
	if json.Unmarshal(params, u) == nil {
		return nil
	}
	return json.Unmarshal(params, into)
}

func webURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("http_fetch: not a url: %q", raw)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return "", fmt.Errorf("http_fetch: only http and https urls, not %s:", u.Scheme)
	}
	// only the name "localhost" gets an early, friendly refusal here; an IP
	// literal (or anything else that resolves local) is caught just as dead
	// by the dial-time guard, which is the real defense — this check exists
	// so the common case fails with a clearer message.
	if host := u.Hostname(); host == "localhost" {
		return "", fmt.Errorf("http_fetch: %q is local, and napps may not fetch it", raw)
	}
	return u.String(), nil
}

func readCapped(r io.Reader) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(r, maxBody+1))
	if err != nil {
		return nil, fmt.Errorf("http_fetch: reading the response: %w", err)
	}
	if len(raw) > maxBody {
		return nil, fmt.Errorf("http_fetch: the response is over %dMB", maxBody>>20)
	}
	return raw, nil
}
