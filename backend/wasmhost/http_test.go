package wasmhost

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A 40x20 png, left half red and right half blue, answered by a local server.
func pictureServer(t *testing.T) *httptest.Server {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 40, 20))
	for y := 0; y < 20; y++ {
		for x := 0; x < 40; x++ {
			c := color.NRGBA{R: 255, A: 255}
			if x >= 20 {
				c = color.NRGBA{B: 255, A: 128}
			}
			img.SetNRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/pic.png":
			w.Header().Set("Content-Type", "image/png")
			w.Write(buf.Bytes())
		case "/echo":
			w.Header().Set("X-Method", r.Method)
			var body bytes.Buffer
			body.ReadFrom(r.Body)
			w.Write(body.Bytes())
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func bitmapOf(t *testing.T, raw []byte) (int, int, []byte) {
	t.Helper()
	w := int(binary.LittleEndian.Uint32(raw[0:]))
	h := int(binary.LittleEndian.Uint32(raw[4:]))
	if len(raw) != 8+w*h*4 {
		t.Fatalf("bitmap is %d bytes for %dx%d", len(raw), w, h)
	}
	return w, h, raw[8:]
}

func TestHTTPFetchImage(t *testing.T) {
	restore := plainDialForTests(t)
	defer restore()
	srv := pictureServer(t)

	// Natural size, unpremultiplied: the blue half keeps its full blue.
	raw, err := httpFetchImage([]byte(`"` + srv.URL + `/pic.png"`))
	if err != nil {
		t.Fatal(err)
	}
	w, h, px := bitmapOf(t, raw)
	if w != 40 || h != 20 {
		t.Fatalf("natural size is %dx%d", w, h)
	}
	if got := px[(5*40+30)*4:][:4]; got[2] != 255 || got[3] != 128 {
		t.Fatalf("blue pixel came out %v", got)
	}

	// Contain into 10x10 keeps the aspect.
	raw, err = httpFetchImage([]byte(`{"url":"` + srv.URL + `/pic.png","width":10,"height":10}`))
	if err != nil {
		t.Fatal(err)
	}
	if w, h, _ = bitmapOf(t, raw); w != 10 || h != 5 {
		t.Fatalf("contain gave %dx%d", w, h)
	}

	// Cover with one side is a square, cropped from the middle: both
	// halves still show.
	raw, err = httpFetchImage([]byte(`{"url":"` + srv.URL + `/pic.png","width":8,"fit":"cover"}`))
	if err != nil {
		t.Fatal(err)
	}
	w, h, px = bitmapOf(t, raw)
	if w != 8 || h != 8 {
		t.Fatalf("cover gave %dx%d", w, h)
	}
	if left, right := px[(4*8+0)*4:][:4], px[(4*8+7)*4:][:4]; left[0] != 255 || right[2] != 255 {
		t.Fatalf("cover crop is off: left %v right %v", left, right)
	}

	if _, err := httpFetchImage([]byte(`"` + srv.URL + `/missing"`)); err == nil {
		t.Fatal("a 404 decoded")
	}
	if _, err := httpFetchImage([]byte(`"file:///etc/passwd"`)); err == nil {
		t.Fatal("a file url was fetched")
	}
}

func TestHTTPFetch(t *testing.T) {
	restore := plainDialForTests(t)
	defer restore()
	srv := pictureServer(t)
	res, err := httpFetch([]byte(`{"url":"` + srv.URL + `/echo","method":"post","body":"hello"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := base64.StdEncoding.DecodeString(res.Body)
	if res.Status != 200 || string(body) != "hello" || res.Headers["x-method"] != "POST" {
		raw, _ := json.Marshal(res)
		t.Fatalf("unexpected answer %s", raw)
	}
}

func TestIsForbiddenIP(t *testing.T) {
	for ip, want := range map[string]bool{
		"127.0.0.1":               true,
		"::1":                     true,
		"0.0.0.0":                 true,
		"::":                      true,
		"169.254.169.254":         true,
		"fe80::1":                 true,
		"224.0.0.1":               true,
		"ff02::1":                 true,
		"::ffff:127.0.0.1":        true,
		"::ffff:169.254.169.254":  true,
		"::127.0.0.1":             true,
		"::169.254.169.254":       true,
		"64:ff9b::127.0.0.1":      true,
		"64:ff9b::169.254.169.254": true,
		"64:ff9b:1::127.0.0.1":    true,
		"8.8.8.8":                 false,
		"192.168.1.10":            false,
		"10.0.0.2":                false,
		"2620:0:2d0:200::7":       false,
		"64:ff9b::8.8.8.8":        false,
	} {
		if got := isForbiddenIP(net.ParseIP(ip)); got != want {
			t.Errorf("isForbiddenIP(%s) = %v, want %v", ip, got, want)
		}
	}
}

func TestWebURLRejectsLocalhost(t *testing.T) {
	for _, bad := range []string{
		"http://localhost:8080/x",
		"HTTP://LOCALHOST:8080/x",
		"https://LocalHost./x",
		"http://sub.localhost:9000/x",
		"http://foo.LOCALHOST./x",
		"file:///etc/passwd",
	} {
		if _, err := webURL(bad); err == nil {
			t.Errorf("webURL(%q) should have been rejected", bad)
		}
	}
	if _, err := webURL("https://relay.example.com"); err != nil {
		t.Errorf("webURL(https://relay.example.com) = %v, want nil", err)
	}
}

func TestCheckRedirectBlocksDowngradeAndStripsAuth(t *testing.T) {
	orig, _ := http.NewRequest("GET", "https://a.example/start", nil)
	orig.Header.Set("Authorization", "Bearer secret")
	orig.Header.Set("Cookie", "sid=1")

	downgrade, _ := http.NewRequest("GET", "http://a.example/next", nil)
	if err := checkRedirect(downgrade, []*http.Request{orig}); err == nil {
		t.Fatal("expected https->http redirect downgrade to be rejected")
	}

	cross, _ := http.NewRequest("GET", "https://b.example/next", nil)
	cross.Header.Set("Authorization", "Bearer secret")
	cross.Header.Set("Cookie", "sid=1")
	if err := checkRedirect(cross, []*http.Request{orig}); err != nil {
		t.Fatalf("unexpected error on https->https redirect: %v", err)
	}
	if cross.Header.Get("Authorization") != "" || cross.Header.Get("Cookie") != "" {
		t.Fatal("expected Authorization and Cookie to be stripped on cross-origin redirect")
	}
}

// bombPNG is a well-formed PNG header declaring far more pixels than any
// real image needs: the signature, an IHDR chunk (100000 x 100000, 8-bit
// RGB) and nothing else. DecodeConfig reads exactly this far, so the test
// needs no pixel data - it exercises the header gate, not the decoder.
func bombPNG(t *testing.T) []byte {
	t.Helper()
	crc := func(chunk []byte) []byte {
		table := crc32.MakeTable(crc32.IEEE)
		return []byte{byte(crc32.Checksum(chunk, table) >> 24),
			byte(crc32.Checksum(chunk, table) >> 16),
			byte(crc32.Checksum(chunk, table) >> 8),
			byte(crc32.Checksum(chunk, table))}
	}
	ihdr := []byte{'I', 'H', 'D', 'R',
		0x00, 0x01, 0x86, 0xA0, // width  100000
		0x00, 0x01, 0x86, 0xA0, // height 100000
		8, 2, 0, 0, 0} // 8-bit, truecolor, no interlace
	png := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n'}
	png = append(png, 0, 0, 0, 13)
	png = append(png, ihdr...)
	png = append(png, crc(ihdr)...)
	return png
}

// A 16MB-compressed body can declare a hundred-million-pixel canvas: the
// header gate has to refuse it before the decoder allocates for it.
func TestHTTPFetchImageRejectsDecompressionBomb(t *testing.T) {
	url := allowTestServer(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(bombPNG(t))
	})))

	_, err := httpFetchImage([]byte(`"` + url + `/bomb.png"`))
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("a decompression bomb was not refused: %v", err)
	}
}

// plainDialForTests swaps the fetch guard for an unfiltered dialer for the
// duration of a test: the tests exercise the proxy against local httptest
// servers, which the guard exists to keep napps away from. It restores the
// guard on the way out.
func plainDialForTests(t *testing.T) func() {
	old := fetchDialContext
	fetchDialContext = (&net.Dialer{}).DialContext
	return func() { fetchDialContext = old }
}

// allowTestServer keeps the guard fully on and admits exactly one address —
// the test's own server — so everything else the fetch reaches for, a
// redirect target included, meets the guard for real.
func allowTestServer(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	old := testAllowedAddr
	testAllowedAddr = srv.Listener.Addr().String()
	t.Cleanup(func() { testAllowedAddr = old })
	return srv.URL
}

// The guard refuses loopback on its own: a fetch aimed straight at this
// machine never dials, whatever port it names.
func TestGuardedDialRefusesLoopback(t *testing.T) {
	_, err := httpFetch([]byte(`{"url":"http://127.0.0.1:9/x"}`))
	if err == nil || !strings.Contains(err.Error(), "local address") {
		t.Fatalf("a loopback fetch was not refused by the guard: %v", err)
	}
}

// A redirect's target meets the same guard as the fetch's own url: the
// allowed server answers with a 302 to another loopback server, and the
// hop toward it must refuse rather than follow. This is the end-to-end
// proof that re-checking happens per connection, not once per fetch.
func TestRedirectTargetIsGuarded(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the redirect target's server was reached")
		w.WriteHeader(200)
	}))
	defer other.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/secret", http.StatusFound)
	}))
	defer srv.Close()
	allowTestServer(t, srv)

	_, err := httpFetch([]byte(`{"url":"` + srv.URL + `/start"}`))
	if err == nil || !strings.Contains(err.Error(), "local address") {
		t.Fatalf("a redirect to a local address was not refused by the guard: %v", err)
	}
}
