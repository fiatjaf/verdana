package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"verdana/backend/wasm"
)

// callingModule calls napp_call("ping", "{}") on every frame and keeps the
// last message napp_receive gave it: kind, id and length in three globals,
// the bytes wherever napp_alloc put them (a bump allocator from 4096).
const callingModule = `
(module
  (import "env" "napp_canvas_width" (func $width (result i32)))
  (import "env" "napp_canvas_height" (func $height (result i32)))
  (import "env" "napp_call" (func $call (param i32 i32 i32 i32) (result i32)))
  (memory (export "memory") 2)
  (data (i32.const 16) "ping{}")
  (global $next (mut i32) (i32.const 4096))
  (global $kind (export "kind") (mut i32) (i32.const -1))
  (global $id (export "id") (mut i32) (i32.const -1))
  (global $ptr (export "ptr") (mut i32) (i32.const 0))
  (global $len (export "len") (mut i32) (i32.const 0))
  (global $lastcall (export "lastcall") (mut i32) (i32.const 0))

  (func (export "napp_canvas_ptr") (result i32) (i32.const 8192))
  (func (export "napp_frame") (param f64) (result i32)
    (global.set $lastcall (call $call (i32.const 16) (i32.const 4) (i32.const 20) (i32.const 2)))
    (i32.const 0))
  (func (export "napp_alloc") (param $n i32) (result i32)
    (local $p i32)
    (local.set $p (global.get $next))
    (global.set $next (i32.add (global.get $next) (local.get $n)))
    (local.get $p))
  (func (export "napp_receive") (param i32 i32 i32 i32)
    (global.set $kind (local.get 0))
    (global.set $id (local.get 1))
    (global.set $ptr (local.get 2))
    (global.set $len (local.get 3)))
)`

func assemble(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	in := filepath.Join(dir, "m.wat")
	if err := os.WriteFile(in, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "m.wasm")
	if err := wat2wasm(in, out); err != nil {
		t.Skipf("no wasm assembler available: %v", err)
	}
	return out
}

func global(t *testing.T, n *napp, name string) int32 {
	t.Helper()
	g := n.mod.ExportedGlobal(name)
	if g == nil {
		t.Fatalf("no global %s", name)
	}
	return int32(uint32(g.Get()))
}

func TestCallAndReceive(t *testing.T) {
	var gotMethod, gotParams string
	n, err := loadWasmNapp(assemble(t, callingModule), func() float64 { return 0 },
		func(method string, params []byte) int32 {
			gotMethod, gotParams = method, string(params)
			return 7
		})
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()

	n.Resize(1, 1)
	if err := n.Frame(0); err != nil {
		t.Fatal(err)
	}
	if gotMethod != "ping" || gotParams != "{}" {
		t.Fatalf("napp_call reached the host as %q %q", gotMethod, gotParams)
	}
	if id := global(t, n, "lastcall"); id != 7 {
		t.Fatalf("napp_call returned %d to the module, want the host's 7", id)
	}

	payload := []byte(`{"pong":true}`)
	if err := n.Deliver(wasm.MsgResult, 7, payload); err != nil {
		t.Fatal(err)
	}
	if k, id := global(t, n, "kind"), global(t, n, "id"); k != wasm.MsgResult || id != 7 {
		t.Fatalf("module got kind %d id %d", k, id)
	}
	ptr, size := global(t, n, "ptr"), global(t, n, "len")
	got, _ := n.mod.Memory().Read(uint32(ptr), uint32(size))
	if !bytes.Equal(got, payload) {
		t.Fatalf("module got %q, want %q", got, payload)
	}

	// an empty message needs no room
	if err := n.Deliver(wasm.MsgTheme, 0, nil); err != nil {
		t.Fatal(err)
	}
	if ptr, size := global(t, n, "ptr"), global(t, n, "len"); ptr != 0 || size != 0 {
		t.Fatalf("an empty message arrived as %d+%d", ptr, size)
	}
}

func TestRefusesCallWithoutReceive(t *testing.T) {
	const src = `(module
	  (import "env" "napp_call" (func (param i32 i32 i32 i32) (result i32)))
	  (memory (export "memory") 1)
	  (func (export "napp_canvas_ptr") (result i32) (i32.const 0))
	  (func (export "napp_frame") (param f64) (result i32) (i32.const 0)))`
	_, err := loadWasmNapp(assemble(t, src), func() float64 { return 0 }, nil)
	if err == nil || !strings.Contains(err.Error(), wasm.ExportReceive) {
		t.Fatalf("a module that calls out but cannot be answered should be refused, got %v", err)
	}
}

// testBridge is a bridge writing to a buffer instead of the launcher.
func testBridge(t *testing.T) (*bridge, *bytes.Buffer) {
	t.Setenv("VERDANA_NAPP_STORAGE_FILE", filepath.Join(t.TempDir(), "none.json"))
	var out bytes.Buffer
	return newBridge(&out), &out
}

func lastSent(t *testing.T, out *bytes.Buffer) wireMsg {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	var m wireMsg
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestBridgeAnswersLocally(t *testing.T) {
	b, out := testBridge(t)
	const pk = "3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d"
	const npub = "npub180cvv07tjdrrgpa0j7j7tmnyl2yr6yr7l8j4s3evf6u64th6gkwsyjh6w6"

	id := b.call("nip19.npubEncode", []byte(`"`+pk+`"`))
	id2 := b.call("nip19.decode", []byte(`"`+npub+`"`))
	naddrID := b.call("nip19.naddrEncode", []byte(`{"identifier":"x","pubkey":"`+pk+`","kind":30023,"relays":["wss://r"]}`))
	sats := b.call("fx.satsFromBolt11", []byte(`"lnbc2500u1pvjluez"`))
	if out.Len() != 0 {
		t.Fatalf("local calls went to the launcher: %s", out.String())
	}

	msgs := b.drain()
	if len(msgs) != 4 {
		t.Fatalf("got %d answers, want 4", len(msgs))
	}
	if msgs[0].id != id || string(msgs[0].data) != `"`+npub+`"` {
		t.Errorf("npubEncode answered %d %s", msgs[0].id, msgs[0].data)
	}
	if msgs[1].id != id2 || string(msgs[1].data) != `{"type":"npub","data":"`+pk+`"}` {
		t.Errorf("decode answered %d %s", msgs[1].id, msgs[1].data)
	}
	if string(msgs[3].data) != "250000" || msgs[3].id != sats {
		t.Errorf("satsFromBolt11 answered %s", msgs[3].data)
	}

	// and the naddr decodes back to what went in
	var naddr string
	json.Unmarshal(msgs[2].data, &naddr)
	if msgs[2].id != naddrID || !strings.HasPrefix(naddr, "naddr1") {
		t.Fatalf("naddrEncode answered %s", msgs[2].data)
	}
	b.call("nip19.decode", msgs[2].data)
	back := b.drain()[0]
	want := `{"type":"naddr","data":{"identifier":"x","kind":30023,"pubkey":"` + pk + `","relays":["wss://r"]}}`
	if string(back.data) != want {
		t.Errorf("naddr round trip gave %s", back.data)
	}
}

func TestBridgeStorage(t *testing.T) {
	b, out := testBridge(t)
	b.call("napp.storageSet", []byte(`{"key":"a","value":"1"}`))
	if m := lastSent(t, out); m.T != "rpc" || m.Method != "napp.storageSet" {
		t.Fatalf("a write should be persisted by the launcher, sent %+v", m)
	}
	b.call("napp.storageGet", []byte(`"a"`))
	b.call("napp.storageGet", []byte(`"missing"`))
	msgs := b.drain()
	if len(msgs) != 2 || string(msgs[0].data) != `"1"` || string(msgs[1].data) != "null" {
		t.Fatalf("reads answered %+v", msgs)
	}

	// another window's change lands here and is announced
	b.handleWire(wireMsg{T: "storage", Params: `{"op":"set","key":"b","value":"2"}`})
	if m := b.drain(); len(m) != 1 || m[0].kind != wasm.MsgStorage {
		t.Fatalf("a synced change should be announced, got %+v", m)
	}
	b.call("napp.storageKeys", []byte("null"))
	if m := b.drain(); string(m[0].data) != `["a","b"]` {
		t.Fatalf("keys answered %s", m[0].data)
	}

	if b.call("napp.storageSet", []byte(`{"key":"big","value":"`+strings.Repeat("x", storageQuota)+`"}`)) == 0 {
		t.Fatal("no id")
	}
	if m := b.drain(); len(m) != 1 || m[0].kind != wasm.MsgError {
		t.Fatalf("a write over the quota should fail locally, got %+v", m)
	}
}

func TestBridgeForwardsAndRoutes(t *testing.T) {
	b, out := testBridge(t)

	id := b.call("nostrdb.query", []byte(`{"filters":{"kinds":[1]}}`))
	sent := lastSent(t, out)
	if sent.T != "rpc" || sent.ID != int(id) || sent.Method != "nostrdb.query" {
		t.Fatalf("sent %+v", sent)
	}
	b.handleWire(wireMsg{T: "resp", ID: int(id), Result: json.RawMessage(`[]`)})
	b.handleWire(wireMsg{T: "resp", ID: 99, Error: "nope"})
	msgs := b.drain()
	if msgs[0].kind != wasm.MsgResult || msgs[0].id != id || string(msgs[0].data) != "[]" {
		t.Errorf("result arrived as %+v", msgs[0])
	}
	if msgs[1].kind != wasm.MsgError || string(msgs[1].data) != "nope" {
		t.Errorf("error arrived as %+v", msgs[1])
	}

	// a feed's callbackId is the call's own id, and batches stop once it
	// is cancelled
	feed := b.call("napp.feeds.profile", []byte(`{"pubkey":"x","kinds":[1]}`))
	var p struct {
		CallbackID int32 `json:"callbackId"`
	}
	json.Unmarshal([]byte(lastSent(t, out).Params), &p)
	if p.CallbackID != feed {
		t.Fatalf("feed went up with callbackId %d, want %d", p.CallbackID, feed)
	}
	b.handleWire(wireMsg{T: "feed", ID: int(feed), Params: `{"events":[],"synced":true}`})
	if m := b.drain(); len(m) != 1 || m[0].kind != wasm.MsgFeed || m[0].id != feed {
		t.Fatalf("feed batch arrived as %+v", m)
	}
	b.call("napp.feeds.cancel", []byte(fmt.Sprintf(`{"callbackId":%d}`, feed)))
	b.handleWire(wireMsg{T: "feed", ID: int(feed), Params: `{"events":[],"synced":true}`})
	if m := b.drain(); len(m) != 0 {
		t.Fatalf("a cancelled feed still delivered %+v", m)
	}

	// registering defaults idx to 0, and an action comes back with it
	b.call("napp.registerAction", []byte(`{"pattern":"view:1"}`))
	if got := lastSent(t, out).Params; !strings.Contains(got, `"idx":0`) {
		t.Fatalf("registerAction went up as %s", got)
	}
	idx := 0
	b.handleWire(wireMsg{T: "action", ID: 3, Method: "view:1", Params: `{"id":"e"}`, Idx: &idx})
	m := b.drain()
	if len(m) != 1 || m[0].kind != wasm.MsgAction || m[0].id != 3 ||
		string(m[0].data) != `{"name":"view:1","payload":{"id":"e"},"idx":0}` {
		t.Fatalf("action arrived as %+v %s", m, m[0].data)
	}
}
