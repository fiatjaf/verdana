package mobile

import (
	"os"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"

	"verdana/backend"
)

// fakeUI is a phone with no screen: it only counts the wakes and closes.
type fakeUI struct {
	wakes  atomic.Int32
	closed atomic.Int32
}

func (u *fakeUI) OpenWindow(string, string) error           { return nil }
func (u *fakeUI) SendToWindow(string, string)               {}
func (u *fakeUI) CloseWindow(string)                        { u.closed.Add(1) }
func (u *fakeUI) StateChanged()                             {}
func (u *fakeUI) PromptsChanged()                           {}
func (u *fakeUI) CopyText(string) error                     { return nil }
func (u *fakeUI) SaveFile(string, []byte) (string, error)   { return "", nil }
func (u *fakeUI) SaveFileTarget() string                    { return "" }
func (u *fakeUI) OpenLink(string) error                     { return nil }
func (u *fakeUI) WasmInvalidate(string)                     { u.wakes.Add(1) }
func (u *fakeUI) AmberRequest(_, _, _, _, _, _ string) bool { return false }

// testNapp is a real wasm napp from the napps repository next to this one,
// when it is there to be had.
func testNapp(t *testing.T) string {
	path := os.Getenv("VERDANA_TEST_WASM")
	if path == "" {
		path = "../../../napps/words/dist/app.wasm"
	}
	if _, err := os.Stat(path); err != nil {
		t.Skipf("no wasm napp to run (set VERDANA_TEST_WASM): %v", err)
	}
	return path
}

func TestWasmWindow(t *testing.T) {
	ui := &fakeUI{}
	w := openWasm(ui, backend.WindowSpec{Instance: "t1", Wasm: testNapp(t), Mode: backend.ModeWasm})
	defer w.close()

	// frames come back empty until the module is up, and the window is
	// woken when it is
	deadline := time.Now().Add(30 * time.Second)
	var px []byte
	for px == nil {
		if time.Now().After(deadline) {
			t.Fatal("the module never loaded")
		}
		var err error
		px, err = WasmFrame("t1", 200, 150)
		if err != nil {
			t.Fatal(err)
		}
		if px == nil {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if ui.wakes.Load() == 0 {
		t.Error("the window was not woken when the module loaded")
	}
	if len(px) != 200*150*4 {
		t.Fatalf("got %d bytes for a 200x150 canvas", len(px))
	}

	// a resize is the next frame's canvas
	WasmInput("t1", EventPointerMove, 10, 10, 0, 0, 0)
	WasmInput("t1", EventPointerDown, 10, 10, ButtonPrimary, 0, 0)
	WasmInput("t1", EventPointerUp, 10, 10, 0, 0, 0)
	WasmText("t1", "hé")
	px, err := WasmFrame("t1", 120, 300)
	if err != nil {
		t.Fatal(err)
	}
	if len(px) != 120*300*4 {
		t.Fatalf("got %d bytes for a 120x300 canvas", len(px))
	}

	// the backend closing the window closes it on the phone
	wasmTransport{w: w}.Send(backend.WireMsg{T: "close"})
	if ui.closed.Load() != 1 {
		t.Error("a close from the backend did not close the window")
	}

	// and once it is gone, nothing runs it any more
	w.close()
	if _, err := WasmFrame("t1", 10, 10); err == nil {
		t.Error("a closed window still ran a frame")
	}
}

func TestWasmWindowRefusesABrokenModule(t *testing.T) {
	ui := &fakeUI{}
	bad := t.TempDir() + "/app.wasm"
	os.WriteFile(bad, []byte("not wasm"), 0644)
	w := openWasm(ui, backend.WindowSpec{Instance: "t2", Wasm: bad, Mode: backend.ModeWasm})
	defer w.close()

	deadline := time.Now().Add(10 * time.Second)
	for {
		_, err := WasmFrame("t2", 10, 10)
		if err != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("a broken module never said so")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A napp stuck in a loop must not take the launcher with it: closing its
// window comes back at once, whatever the module is doing.
func TestClosingAStuckWasmWindow(t *testing.T) {
	assembler, err := exec.LookPath("wat2wasm")
	if err != nil {
		t.Skip("no wat2wasm")
	}
	dir := t.TempDir()
	os.WriteFile(dir+"/spin.wat", []byte(`(module
	  (memory (export "memory") 1)
	  (func (export "napp_canvas_ptr") (result i32) (i32.const 0))
	  (func (export "napp_frame") (param f64) (result i32)
	    (loop $forever (br $forever))
	    (i32.const 0)))`), 0644)
	if out, err := exec.Command(assembler, dir+"/spin.wat", "-o", dir+"/app.wasm").CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}

	w := openWasm(&fakeUI{}, backend.WindowSpec{Instance: "t3", Wasm: dir + "/app.wasm", Mode: backend.ModeWasm})
	go func() {
		for {
			if px, err := WasmFrame("t3", 1, 1); px != nil || err != nil {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	time.Sleep(500 * time.Millisecond)

	closed := make(chan struct{})
	go func() {
		w.close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("closing waited on a module stuck in a loop")
	}
}
