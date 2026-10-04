package main

import (
	"os"
	"path/filepath"
	"testing"
)

// A wasm napp is only worth having if the whole loop works: the module gets
// the canvas size, paints into its own memory, and the host reads those exact
// pixels back. This drives that loop with a module built for the test, since
// there is no display here to put a window on.
//
// The module is the smallest thing that honours the ABI: it fills the canvas
// with a colour derived from the width it was handed, so a wrong size or a
// stale pointer shows up as a wrong colour rather than as a crash.
const testModule = `
(module
  (import "env" "napp_canvas_width" (func $width (result i32)))
  (import "env" "napp_canvas_height" (func $height (result i32)))
  (import "env" "napp_now_ms" (func $now (result f64)))
  (memory (export "memory") 8)
  (global $ptr (mut i32) (i32.const 1024))

  (func (export "napp_canvas_ptr") (result i32)
    (global.get $ptr))

  ;; One pixel is "red with the width it was handed divided by four", so the
  ;; size the module was given is visible in what comes back out.
  (func (export "napp_frame") (param $dt f64) (result i32)
    (local $w i32) (local $h i32) (local $n i32)
    (local $p i32) (local $i i32) (local $o i32)
    (local.set $w (call $width))
    (local.set $h (call $height))
    (local.set $n (i32.mul (local.get $w) (local.get $h)))
    (local.set $p (global.get $ptr))
    (local.set $i (i32.const 0))
    (block $done
      (loop $pixels
        (br_if $done (i32.ge_s (local.get $i) (local.get $n)))
        (local.set $o (i32.add (local.get $p)
                               (i32.mul (local.get $i) (i32.const 4))))
        (i32.store8 (local.get $o)
          (i32.and (i32.shr_u (local.get $w) (i32.const 2)) (i32.const 255)))
        (i32.store8 (i32.add (local.get $o) (i32.const 1)) (i32.const 200))
        (i32.store8 (i32.add (local.get $o) (i32.const 2)) (i32.const 30))
        (i32.store8 (i32.add (local.get $o) (i32.const 3)) (i32.const 255))
        (local.set $i (i32.add (local.get $i) (i32.const 1)))
        (br $pixels)))
    (i32.const 1))
)`

// watModule is the one piece of WebAssembly text this test needs turned into
// a binary. Hand-assembling the instructions would be a worse trade than a
// parser for six functions.
func watModule(t *testing.T) []byte {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "test.wat")
	if err := os.WriteFile(src, []byte(testModule), 0644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "test.wasm")
	if err := wat2wasm(src, out); err != nil {
		t.Skipf("no wasm assembler available: %v", err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestFrameRoundTrip(t *testing.T) {
	raw := watModule(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "napp.wasm")
	if err := os.WriteFile(path, raw, 0644); err != nil {
		t.Fatal(err)
	}

	n, err := loadWasmNapp(path, func() float64 { return 0 })
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()

	n.Resize(320, 240)
	if err := n.Frame(0); err != nil {
		t.Fatal(err)
	}
	if !n.HasCanvas() {
		t.Fatal("the napp said it painted but there is no canvas")
	}
	if !n.Animating() {
		t.Error("a napp that asked for another frame should be reported as animating")
	}
	if got, want := len(n.Pixels()), 320*240*4; got != want {
		t.Fatalf("canvas is %d bytes, want %d", got, want)
	}

	// The module fills red with width/4, so the size it was handed is
	// visible in the pixels: a canvas read at the wrong stride or the wrong
	// offset would not come back like this.
	px := n.Pixels()
	for _, i := range []int{0, 4, 320*4*100 + 8, len(px) - 4} {
		if px[i] != 320/4 || px[i+1] != 200 || px[i+2] != 30 || px[i+3] != 255 {
			t.Fatalf("pixel %d is %v, want [%d 200 30 255]", i, px[i:i+4], 320/4)
		}
	}
}

func TestRefusesImportsBeyondTheCanvas(t *testing.T) {
	// A module built for WASI imports things no host here provides, which
	// is the mistake a napp author will make first.
	const wasi = `(module (import "wasi_snapshot_preview1" "proc_exit" (func $e)) (memory (export "memory") 1))`
	dir := t.TempDir()
	src := filepath.Join(dir, "wasi.wat")
	if err := os.WriteFile(src, []byte(wasi), 0644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "wasi.wasm")
	if err := wat2wasm(src, out); err != nil {
		t.Skipf("no wasm assembler available: %v", err)
	}

	_, err := loadWasmNapp(out, func() float64 { return 0 })
	if err == nil {
		t.Fatal("a module importing WASI should not run here")
	}
	if want := "wasi_snapshot_preview1.proc_exit"; !contains(err.Error(), want) {
		t.Errorf("the error should name what the module asked for (%s), got: %v", want, err)
	}
}

func TestRefusesAModuleWithoutTheExports(t *testing.T) {
	const bare = `(module (memory (export "memory") 1))`
	dir := t.TempDir()
	src := filepath.Join(dir, "bare.wat")
	if err := os.WriteFile(src, []byte(bare), 0644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "bare.wasm")
	if err := wat2wasm(src, out); err != nil {
		t.Skipf("no wasm assembler available: %v", err)
	}

	if _, err := loadWasmNapp(out, func() float64 { return 0 }); err == nil {
		t.Fatal("a module with no napp_frame should not run")
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) &&
		(func() bool {
			for i := 0; i+len(needle) <= len(haystack); i++ {
				if haystack[i:i+len(needle)] == needle {
					return true
				}
			}
			return false
		})()
}
