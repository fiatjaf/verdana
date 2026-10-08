package wasmhost

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
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
