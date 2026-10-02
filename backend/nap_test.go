package backend

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/keyer"
	"fiatjaf.com/nostr/nip19"
	"fiatjaf.com/nostr/nipb7/blossom"
)

// ─── test rig ────────────────────────────────────────────────────

// recTransport stands in for a napplet window: it keeps every NAP push the
// backend evals into the host page.
type recTransport struct {
	mu     sync.Mutex
	pushes []map[string]any
	notify chan struct{}
}

func newRecTransport() *recTransport { return &recTransport{notify: make(chan struct{}, 1024)} }

const pushPrefix = "window.__nap_push && window.__nap_push("

func (r *recTransport) Send(m WireMsg) {
	if m.T != "eval" || !strings.HasPrefix(m.Code, pushPrefix) {
		return
	}
	var js string
	if err := json.Unmarshal([]byte(strings.TrimSuffix(strings.TrimPrefix(m.Code, pushPrefix), ")")), &js); err != nil {
		panic(err)
	}
	var one map[string]any
	var many []map[string]any
	r.mu.Lock()
	if json.Unmarshal([]byte(js), &many) == nil {
		r.pushes = append(r.pushes, many...)
	} else if json.Unmarshal([]byte(js), &one) == nil {
		r.pushes = append(r.pushes, one)
	}
	r.mu.Unlock()
	select {
	case r.notify <- struct{}{}:
	default:
	}
}

func (r *recTransport) Close() {}

// find returns the pushes of a type.
func (r *recTransport) find(typ string) []map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []map[string]any{}
	for _, p := range r.pushes {
		if p["type"] == typ {
			out = append(out, p)
		}
	}
	return out
}

func (r *recTransport) types() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []string{}
	for _, p := range r.pushes {
		out = append(out, p["type"].(string))
	}
	return out
}

// wait blocks until a push of the type shows up (the n-th one).
func (r *recTransport) wait(t *testing.T, typ string, n int) map[string]any {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		if got := r.find(typ); len(got) >= n {
			return got[n-1]
		}
		select {
		case <-r.notify:
		case <-deadline:
			t.Fatalf("no %s (#%d) pushed; got %v", typ, n, r.types())
		}
	}
}

func setupNapTest(t *testing.T) {
	t.Helper()
	dataDir = t.TempDir()
	host = noopHost{}
	t.Cleanup(func() {
		storagesMu.Lock()
		storages = make(map[string]*nappStorage)
		storagesMu.Unlock()
	})
}

// openNapplet registers a napplet window with a recording transport.
func openNapplet(t *testing.T, d string) (*Instance, *recTransport) {
	t.Helper()
	n := Napp{ID: "napplet~0123456789abcdef~" + d, D: d, Name: d, Format: FormatNapplet, Kind: KindNapplet}
	ci := &Instance{
		instance:   d + "-" + randomID()[:6],
		napp:       n,
		subs:       map[int]context.CancelFunc{},
		actions:    map[string]int{},
		changed:    make(chan struct{}),
		dispatches: map[int]chan WireMsg{},
		gone:       make(chan struct{}),
		nap:        newNapSession(),
	}
	registerInstance(ci)
	rec := newRecTransport()
	ci.attach(rec)
	t.Cleanup(func() { WindowClosed(ci.instance) })
	return ci, rec
}

// post is the napplet posting an envelope (through the host page's nap.msg).
func post(t *testing.T, ci *Instance, env map[string]any) {
	t.Helper()
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	param, _ := json.Marshal(string(raw))
	if _, err := napRPC(ci, "nap.msg", string(param)); err != nil {
		t.Fatal(err)
	}
}

func ready(t *testing.T, ci *Instance, rec *recTransport, n int) {
	t.Helper()
	post(t, ci, map[string]any{"type": "shell.ready"})
	rec.wait(t, "shell.init", n)
}

// ─── srcdoc ──────────────────────────────────────────────────────

func TestBuildSrcdoc(t *testing.T) {
	napplet := `<!doctype html><html lang="en"><head><title>x</title></head><body><header>h</header><script>napplet()</script></body></html>`
	doc, err := buildSrcdoc([]byte(napplet), []string{"relay", "storage"})
	if err != nil {
		t.Fatal(err)
	}
	// the launcher's preamble comes first, whatever the napplet wrote
	if !strings.HasPrefix(doc, `<!doctype html><html><head><meta http-equiv="Content-Security-Policy"`) {
		t.Fatalf("preamble not first: %.120s", doc)
	}
	csp := strings.Index(doc, `http-equiv="Content-Security-Policy"`)
	shim := strings.Index(doc, "NappletShimPrelude")
	activate := strings.Index(doc, `NappletShimPrelude.install({"domains":["relay","storage"]})`)
	ready := strings.Index(doc, `{type:"shell.ready"}`)
	end := strings.Index(doc, "</head>")
	if !(csp < shim && shim < activate && activate < ready && ready < end) {
		t.Fatalf("preamble out of order: csp=%d shim=%d activate=%d ready=%d end=%d", csp, shim, activate, ready, end)
	}
	// the napplet's bytes follow, untouched
	if !strings.HasSuffix(doc, napplet) {
		t.Error("napplet bytes changed")
	}
	if !strings.Contains(doc, "connect-src 'none'") || strings.Contains(doc, "'unsafe-eval'") {
		t.Error("wrong CSP")
	}

	// nothing the napplet writes can get ahead of the preamble or into it:
	// a commented or scripted "<head>" is just more napplet bytes after it
	for _, tricky := range []string{
		`<!-- <head> --><html><head><script>fetch("https://x")</script>`,
		`<script>"<head>"; fetch("https://x")</script><head></head>`,
		`<!DOCTYPE html>` + "\n<p>hi</p>",
		`<p>hi</p>`,
		"\uFEFF<!doctype html><head></head>",
	} {
		doc, err := buildSrcdoc([]byte(tricky), nil)
		if err != nil {
			t.Fatal(err)
		}
		pre := doc[:strings.Index(doc, "</head>")+len("</head>")]
		if !strings.HasPrefix(doc, "<!doctype html><html><head><meta http-equiv") || strings.Contains(pre, "fetch") ||
			!strings.HasSuffix(doc, strings.TrimPrefix(tricky, "\uFEFF")) {
			t.Errorf("%q: %.160s", tricky, doc)
		}
	}
	if _, err := buildSrcdoc([]byte{0xff, 0xfe, 'x'}, nil); err == nil {
		t.Error("invalid UTF-8 accepted")
	}
}

// ─── session ─────────────────────────────────────────────────────

func TestNapSessionHandshake(t *testing.T) {
	setupNapTest(t)
	ci, rec := openNapplet(t, "alpha")

	// nothing is answered before shell.ready
	post(t, ci, map[string]any{"type": "storage.keys", "id": "early"})
	ready(t, ci, rec, 1)
	if got := rec.find("storage.keys.result"); len(got) != 0 {
		t.Fatalf("answered before shell.ready: %v", got)
	}

	init := rec.find("shell.init")[0]
	domains, _ := init["capabilities"].(map[string]any)["domains"].([]any)
	// Supported APIs are a shell capability, not a least-privilege grant. The
	// shell injects all of them even though this test napplet advertises no
	// requirements.
	if len(ci.napp.Requires) != 0 {
		t.Fatalf("test napplet unexpectedly has requirements: %v", ci.napp.Requires)
	}
	for _, want := range napDomains {
		if !slices.Contains(domains, any(want)) {
			t.Errorf("shell.init lacks %s: %v", want, domains)
		}
	}

	// unknown types: silence, and the session keeps working
	post(t, ci, map[string]any{"type": "nope.whatever", "id": "x"})
	post(t, ci, map[string]any{"type": "storage.keys", "id": "k1"})
	rec.wait(t, "storage.keys.result", 1)
	for _, typ := range rec.types() {
		if strings.HasPrefix(typ, "nope") {
			t.Fatalf("unknown type was answered: %v", rec.types())
		}
	}
}

// ─── uploads ────────────────────────────────────────────────────

func setupNapUploadTest(t *testing.T, ci *Instance) {
	t.Helper()
	previousKeyer, previousPubkey := userKeyer, userPubkey
	previousServers, previousUpload := napUploadServers, napUploadToServer
	userSK := nostr.Generate()
	userKeyer, userPubkey = keyer.NewPlainKeySigner(userSK), userSK.Public()
	setSessionRule(RuleKey{Napp: ci.napp.ID, Permission: PermUpload}, Rule{Decision: DecisionAllow})
	t.Cleanup(func() {
		userKeyer, userPubkey = previousKeyer, previousPubkey
		napUploadServers, napUploadToServer = previousServers, previousUpload
		clearSessionRule(RuleKey{Napp: ci.napp.ID, Permission: PermUpload})
	})
}

func uploadEnvelope(id string, data []byte) map[string]any {
	return map[string]any{
		"type": "upload.upload", "id": id,
		"request": map[string]any{
			"rail": "blossom", "filename": "hello.txt", "caption": "a greeting",
			"data": map[string]any{"__blob": map[string]any{
				"b64": base64.StdEncoding.EncodeToString(data), "mime": "text/plain",
			}},
		},
	}
}

func TestNapUploadBlossomReplicatesAndReportsStatus(t *testing.T) {
	setupNapTest(t)
	ci, rec := openNapplet(t, "uploader")
	setupNapUploadTest(t, ci)
	ready(t, ci, rec, 1)

	data := []byte("hello blossom")
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	napUploadServers = func(context.Context, nostr.PubKey) []string {
		return []string{"https://one.example/", "bad", "https://two.example"}
	}
	var attempted []string
	napUploadToServer = func(_ context.Context, server string, got []byte, mimeType string, _ nostr.Keyer) (*blossom.BlobDescriptor, error) {
		attempted = append(attempted, server)
		if !bytes.Equal(got, data) || mimeType != "text/plain" {
			t.Fatalf("upload bytes/mime: %q %q", got, mimeType)
		}
		return &blossom.BlobDescriptor{
			URL: server + "/" + hash + ".txt", SHA256: hash, Size: len(data), Type: mimeType,
		}, nil
	}

	post(t, ci, uploadEnvelope("u1", data))
	started := rec.wait(t, "upload.upload.result", 1)
	result := started["result"].(map[string]any)
	if result["status"] != "uploading" || result["rail"] != "blossom" {
		t.Fatalf("initial result: %v", result)
	}
	complete := rec.wait(t, "upload.status.changed", 2)["status"].(map[string]any)
	if complete["status"] != "complete" || complete["sha256"] != hash || complete["url"] != "https://one.example/"+hash+".txt" {
		t.Fatalf("complete status: %v", complete)
	}
	if !slices.Equal(attempted, []string{"https://one.example", "https://two.example"}) {
		t.Fatalf("server order: %v", attempted)
	}
	fallbacks := complete["fallbackUrls"].([]any)
	if len(fallbacks) != 1 || fallbacks[0] != "https://two.example/"+hash+".txt" {
		t.Fatalf("fallbacks: %v", fallbacks)
	}
	tags := complete["nip94"].([]any)
	for _, want := range []any{"url", "m", "x", "size", "fallback", "alt"} {
		if !slices.ContainsFunc(tags, func(tag any) bool { return tag.([]any)[0] == want }) {
			t.Errorf("NIP-94 lacks %s: %v", want, tags)
		}
	}

	post(t, ci, map[string]any{"type": "upload.status", "id": "s1", "uploadId": result["uploadId"]})
	if got := rec.wait(t, "upload.status.result", 1)["status"].(map[string]any); got["status"] != "complete" {
		t.Fatalf("status lookup: %v", got)
	}
}

func TestNapUploadRejectsBadRequestsAndUnverifiedResults(t *testing.T) {
	setupNapTest(t)
	ci, rec := openNapplet(t, "upload-errors")
	setupNapUploadTest(t, ci)
	ready(t, ci, rec, 1)

	post(t, ci, map[string]any{"type": "upload.upload", "id": "rail", "request": map[string]any{"rail": "nip96"}})
	if got := rec.wait(t, "upload.upload.result", 1); got["error"] != "unsupported rail" {
		t.Fatalf("unsupported rail: %v", got)
	}
	post(t, ci, map[string]any{"type": "upload.upload", "id": "data", "request": map[string]any{
		"data": map[string]any{"__blob": map[string]any{"b64": "%%%"}},
	}})
	if got := rec.wait(t, "upload.upload.result", 2); got["error"] != "invalid upload data" {
		t.Fatalf("invalid data: %v", got)
	}

	data := []byte("exact bytes")
	napUploadServers = func(context.Context, nostr.PubKey) []string { return []string{"https://bad.example"} }
	napUploadToServer = func(_ context.Context, server string, got []byte, mimeType string, _ nostr.Keyer) (*blossom.BlobDescriptor, error) {
		return &blossom.BlobDescriptor{URL: server + "/wrong", SHA256: strings.Repeat("0", 64), Size: len(got)}, nil
	}
	post(t, ci, uploadEnvelope("bad-descriptor", data))
	rec.wait(t, "upload.upload.result", 3)
	failed := rec.wait(t, "upload.status.changed", 2)["status"].(map[string]any)
	if failed["status"] != "failed" || failed["error"] != "upload failed" {
		t.Fatalf("unverified descriptor accepted: %v", failed)
	}
}

func TestNapUploadIsCancelledOnReload(t *testing.T) {
	setupNapTest(t)
	ci, rec := openNapplet(t, "upload-reload")
	setupNapUploadTest(t, ci)
	ready(t, ci, rec, 1)

	napUploadServers = func(context.Context, nostr.PubKey) []string { return []string{"https://slow.example"} }
	cancelled := make(chan struct{})
	napUploadToServer = func(ctx context.Context, _ string, _ []byte, _ string, _ nostr.Keyer) (*blossom.BlobDescriptor, error) {
		<-ctx.Done()
		close(cancelled)
		return nil, ctx.Err()
	}
	post(t, ci, uploadEnvelope("slow", []byte("wait")))
	rec.wait(t, "upload.upload.result", 1)
	if _, err := napRPC(ci, "nap.reset", ""); err != nil {
		t.Fatal(err)
	}
	ready(t, ci, rec, 2)
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("reload did not cancel the upload")
	}
}

func TestNapStorage(t *testing.T) {
	setupNapTest(t)
	ci, rec := openNapplet(t, "store")
	ready(t, ci, rec, 1)

	post(t, ci, map[string]any{"type": "storage.set", "id": "1", "key": "a", "value": "shared"})
	post(t, ci, map[string]any{"type": "storage.set", "id": "2", "key": "a", "value": "mine", "scope": "instance"})
	post(t, ci, map[string]any{"type": "storage.get", "id": "3", "key": "a"})
	post(t, ci, map[string]any{"type": "storage.get", "id": "4", "key": "a", "scope": "instance"})
	post(t, ci, map[string]any{"type": "storage.get", "id": "5", "key": "missing"})
	post(t, ci, map[string]any{"type": "storage.keys", "id": "6"})
	rec.wait(t, "storage.keys.result", 1)

	gets := rec.find("storage.get.result")
	if len(gets) != 3 {
		t.Fatalf("gets: %v", gets)
	}
	if gets[0]["id"] != "3" || gets[0]["value"] != "shared" {
		t.Errorf("shared get: %v", gets[0])
	}
	if gets[1]["value"] != "mine" {
		t.Errorf("instance get: %v", gets[1])
	}
	if v, present := gets[2]["value"]; !present || v != nil {
		t.Errorf("missing key must be an explicit null: %v", gets[2])
	}
	if keys := rec.find("storage.keys.result")[0]["keys"]; len(keys.([]any)) != 1 {
		t.Errorf("keys: %v", keys)
	}

	// quota
	post(t, ci, map[string]any{"type": "storage.set", "id": "big", "key": "b", "value": strings.Repeat("x", nappletStorageQuota)})
	res := rec.wait(t, "storage.set.result", 3)
	if res["id"] != "big" || res["error"] == nil {
		t.Errorf("quota not enforced: %v", res)
	}

	// shared storage outlives the window: another window of the same napplet sees it
	if v, ok := storageGet(ci.napp.ID, "a"); !ok || v != "shared" {
		t.Errorf("not persisted under the napplet id: %q %v", v, ok)
	}
}

func TestNapReloadIsANewSession(t *testing.T) {
	setupNapTest(t)
	a, recA := openNapplet(t, "reloader")
	b, recB := openNapplet(t, "listener")
	ready(t, a, recA, 1)
	ready(t, b, recB, 1)

	post(t, b, map[string]any{"type": "inc.subscribe", "id": "s", "topic": "t"})
	recB.wait(t, "inc.subscribe.result", 1)

	// b reloads: nap.reset drops its old session before the new document is ready.
	if _, err := napRPC(b, "nap.reset", ""); err != nil {
		t.Fatal(err)
	}
	ready(t, b, recB, 2)
	post(t, a, map[string]any{"type": "inc.emit", "topic": "t", "payload": 1})
	post(t, a, map[string]any{"type": "storage.keys", "id": "sync"})
	recA.wait(t, "storage.keys.result", 1)
	time.Sleep(50 * time.Millisecond)
	if got := recB.find("inc.event"); len(got) != 0 {
		t.Fatalf("subscription survived a reload: %v", got)
	}
}

func TestNapDuplicateReadyIsIdempotent(t *testing.T) {
	setupNapTest(t)
	ci, rec := openNapplet(t, "duplicate-ready")
	ready(t, ci, rec, 1)

	post(t, ci, map[string]any{"type": "inc.subscribe", "id": "topic", "topic": "keep"})
	rec.wait(t, "inc.subscribe.result", 1)

	ci.nap.mu.Lock()
	gen := ci.nap.gen
	ctx := ci.nap.ctx
	ci.nap.grants[PermFetch] = true
	ci.nap.mu.Unlock()

	post(t, ci, map[string]any{"type": "shell.ready"})
	// This call confirms the duplicate ready has been dispatched without
	// depending on an output that an idempotent ready must not produce.
	post(t, ci, map[string]any{"type": "storage.keys", "id": "after-ready"})
	rec.wait(t, "storage.keys.result", 1)

	if got := rec.find("shell.init"); len(got) != 1 {
		t.Fatalf("shell.init sent %d times: %v", len(got), got)
	}
	ci.nap.mu.Lock()
	defer ci.nap.mu.Unlock()
	if ci.nap.gen != gen || ci.nap.ctx != ctx || !ci.nap.established {
		t.Fatalf("duplicate ready changed session: gen=%d (want %d), established=%v", ci.nap.gen, gen, ci.nap.established)
	}
	if !ci.nap.topics["keep"] || !ci.nap.grants[PermFetch] {
		t.Fatalf("duplicate ready cleared session state: topics=%v grants=%v", ci.nap.topics, ci.nap.grants)
	}
}

// ─── inc + intents ───────────────────────────────────────────────

func TestNapInc(t *testing.T) {
	setupNapTest(t)
	a, recA := openNapplet(t, "sender")
	b, recB := openNapplet(t, "receiver")
	ready(t, a, recA, 1)
	ready(t, b, recB, 1)

	post(t, a, map[string]any{"type": "inc.subscribe", "id": "sa", "topic": "chat"})
	post(t, b, map[string]any{"type": "inc.subscribe", "id": "sb", "topic": "chat"})
	recA.wait(t, "inc.subscribe.result", 1)
	recB.wait(t, "inc.subscribe.result", 1)

	post(t, a, map[string]any{"type": "inc.emit", "topic": "chat", "payload": map[string]any{"msg": "hi"}, "sender": "forged"})
	ev := recB.wait(t, "inc.event", 1)
	if ev["topic"] != "chat" || ev["sender"] != "sender" || ev["payload"].(map[string]any)["msg"] != "hi" {
		t.Errorf("event: %v", ev)
	}
	time.Sleep(50 * time.Millisecond)
	if got := recA.find("inc.event"); len(got) != 0 {
		t.Errorf("echoed to the sender: %v", got)
	}

	// channels: the target hears about it before the opener gets its id
	post(t, a, map[string]any{"type": "inc.channel.open", "id": "c1", "target": "receiver"})
	res := recA.wait(t, "inc.channel.open.result", 1)
	opened := recB.wait(t, "inc.channel.opened", 1)
	id, _ := res["channelId"].(string)
	if id == "" || opened["channelId"] != id || opened["peer"] != "sender" || res["peer"] != "receiver" {
		t.Fatalf("open: %v / %v", res, opened)
	}
	post(t, b, map[string]any{"type": "inc.channel.emit", "channelId": id, "payload": "pong"})
	if got := recA.wait(t, "inc.channel.event", 1); got["payload"] != "pong" || got["sender"] != "receiver" {
		t.Errorf("channel event: %v", got)
	}
	post(t, a, map[string]any{"type": "inc.channel.list", "id": "l"})
	if list := recA.wait(t, "inc.channel.list.result", 1)["channels"].([]any); len(list) != 1 {
		t.Errorf("list: %v", list)
	}

	// a closing window closes its channels, and the peer is told
	WindowClosed(b.instance)
	if got := recA.wait(t, "inc.channel.closed", 1); got["channelId"] != id {
		t.Errorf("closed: %v", got)
	}

	// no such target
	post(t, a, map[string]any{"type": "inc.channel.open", "id": "c2", "target": "nobody"})
	if got := recA.wait(t, "inc.channel.open.result", 2); got["error"] == nil || got["channelId"] != nil {
		t.Errorf("open to nobody: %v", got)
	}
}

func TestNapChannelCloseNotifiesBothEndpointsOnce(t *testing.T) {
	setupNapTest(t)
	a, recA := openNapplet(t, "closer")
	b, recB := openNapplet(t, "peer")
	ready(t, a, recA, 1)
	ready(t, b, recB, 1)

	post(t, a, map[string]any{"type": "inc.channel.open", "id": "open", "target": "peer"})
	opened := recA.wait(t, "inc.channel.open.result", 1)
	id, _ := opened["channelId"].(string)
	if id == "" {
		t.Fatalf("channel was not opened: %v", opened)
	}
	recB.wait(t, "inc.channel.opened", 1)

	post(t, a, map[string]any{"type": "inc.channel.close", "channelId": id})
	for name, rec := range map[string]*recTransport{"closer": recA, "peer": recB} {
		closed := rec.wait(t, "inc.channel.closed", 1)
		if closed["channelId"] != id || closed["reason"] != "closed by peer" {
			t.Errorf("%s terminal notification: %v", name, closed)
		}
	}

	// Once removed, a repeated close must not deliver another terminal event.
	post(t, a, map[string]any{"type": "inc.channel.close", "channelId": id})
	post(t, a, map[string]any{"type": "storage.keys", "id": "after-close"})
	recA.wait(t, "storage.keys.result", 1)
	if got := recA.find("inc.channel.closed"); len(got) != 1 {
		t.Errorf("closer received %d terminal notifications: %v", len(got), got)
	}
	if got := recB.find("inc.channel.closed"); len(got) != 1 {
		t.Errorf("peer received %d terminal notifications: %v", len(got), got)
	}
}

// A napplet can post any envelope it likes, ids and extra fields included:
// what keeps it in its lane is that the answer goes back to the window that
// asked and nowhere else. A windowId naming another window changes nothing.
func TestNapReplyIsBoundToTheSendingWindow(t *testing.T) {
	setupNapTest(t)
	a, recA := openNapplet(t, "asker")
	b, recB := openNapplet(t, "bystander")
	ready(t, a, recA, 1)
	ready(t, b, recB, 1)

	post(t, a, map[string]any{"type": "identity.getPublicKey", "id": "replay-id", "windowId": b.instance})
	if got := recA.wait(t, "identity.getPublicKey.result", 1); got["id"] != "replay-id" {
		t.Errorf("reply: %v", got)
	}

	post(t, b, map[string]any{"type": "storage.keys", "id": "sync"})
	recB.wait(t, "storage.keys.result", 1)
	if got := recB.find("identity.getPublicKey.result"); len(got) != 0 {
		t.Errorf("another window's reply reached the bystander: %v", got)
	}
	if got := recA.find("identity.getPublicKey.result"); len(got) != 1 {
		t.Errorf("asker got %d replies: %v", len(got), got)
	}
}

// A channel id is not a capability: a window that is not one of its two ends
// can neither speak on it nor close it, however it learned the id.
func TestNapIncChannelOpsRequireMembership(t *testing.T) {
	setupNapTest(t)
	a, recA := openNapplet(t, "end-a")
	b, recB := openNapplet(t, "end-b")
	c, recC := openNapplet(t, "outsider")
	ready(t, a, recA, 1)
	ready(t, b, recB, 1)
	ready(t, c, recC, 1)

	post(t, a, map[string]any{"type": "inc.channel.open", "id": "open", "target": "end-b"})
	id, _ := recA.wait(t, "inc.channel.open.result", 1)["channelId"].(string)
	if id == "" {
		t.Fatal("channel was not opened")
	}
	recB.wait(t, "inc.channel.opened", 1)

	post(t, c, map[string]any{"type": "inc.channel.emit", "channelId": id, "payload": "intruder"})
	post(t, c, map[string]any{"type": "inc.channel.close", "channelId": id})
	post(t, c, map[string]any{"type": "storage.keys", "id": "sync"})
	recC.wait(t, "storage.keys.result", 1)

	// the channel still works, and the first thing b hears on it is a
	post(t, a, map[string]any{"type": "inc.channel.emit", "channelId": id, "payload": "ping"})
	if got := recB.wait(t, "inc.channel.event", 1); got["payload"] != "ping" || got["sender"] != "end-a" {
		t.Errorf("first event on the channel: %v", got)
	}
	for name, rec := range map[string]*recTransport{"end-a": recA, "end-b": recB} {
		if got := rec.find("inc.channel.closed"); len(got) != 0 {
			t.Errorf("%s saw the outsider close the channel: %v", name, got)
		}
	}
	if got := recA.find("inc.channel.event"); len(got) != 0 {
		t.Errorf("outsider spoke on the channel: %v", got)
	}
}

func TestIntentDeliveryToNapplet(t *testing.T) {
	setupNapTest(t)
	ci, rec := openNapplet(t, "handler")

	done := make(chan error, 1)
	go func() {
		_, err := dispatchToNapplet(context.Background(), ci,
			&actionRequest{name: "napplet:profile/open", sender: "caller"}, json.RawMessage(`{"pubkey":"abc"}`))
		done <- err
	}()

	// A cold-start delivery waits for shell.ready, not an INC subscription.
	time.Sleep(50 * time.Millisecond)
	if len(rec.find("intent.deliver")) != 0 {
		t.Fatal("delivered before the napplet was ready")
	}
	ready(t, ci, rec, 1)
	ev := rec.wait(t, "intent.deliver", 1)["delivery"].(map[string]any)
	if ev["convention"] != "napplet:profile/open" || ev["archetype"] != "profile" ||
		ev["action"] != "open" || ev["sender"] != "caller" ||
		ev["payload"].(map[string]any)["pubkey"] != "abc" {
		t.Errorf("intent event: %v", ev)
	}
	if err := <-done; err != nil {
		t.Errorf("dispatch: %v", err)
	}
}

func TestConventionPartsAcceptsOnlyStableIdentity(t *testing.T) {
	archetype, action, ok := conventionParts("napplet:profile/open")
	if !ok || archetype != "profile" || action != "open" {
		t.Fatalf("parts = %q %q %v", archetype, action, ok)
	}
	for _, bad := range []string{"napplet:p/open#f", "napplet:p/open?a=1", "https://x/y", "napplet:P/open", "napplet:p/a/b"} {
		if _, _, ok := conventionParts(bad); ok {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestNapIntentAcceptanceSurvivesSourceLifecycle(t *testing.T) {
	setupNapTest(t)
	caller, recCaller := openNapplet(t, "intent-caller")
	handler, recHandler := openNapplet(t, "profile-handler")
	handler.napp.Conventions = []NappletConvention{{ID: "napplet:profile/open"}}
	handler.napp.Actions = []string{"napplet:profile/open"}

	stateMu.Lock()
	if state.InstalledNapps == nil {
		state.InstalledNapps = make(map[string]Napp)
	}
	state.InstalledNapps[handler.napp.ID] = handler.napp
	stateMu.Unlock()
	t.Cleanup(func() {
		stateMu.Lock()
		delete(state.InstalledNapps, handler.napp.ID)
		stateMu.Unlock()
	})
	key := RuleKey{Napp: caller.napp.ID, Permission: PermDispatch, Subject: "napplet:profile/open"}
	setSessionRule(key, Rule{Decision: DecisionAllow, Target: handler.napp.ID})
	t.Cleanup(func() { clearSessionRule(key) })

	ready(t, caller, recCaller, 1)
	post(t, caller, map[string]any{"type": "intent.available", "id": "available", "archetype": "profile"})
	availability := recCaller.wait(t, "intent.available.result", 1)["availability"].(map[string]any)
	candidates := availability["candidates"].([]any)
	contract := candidates[0].(map[string]any)["contracts"].([]any)[0].(map[string]any)
	if contract["convention"] != "napplet:profile/open" {
		t.Fatalf("availability contracts: %v", availability)
	}

	post(t, caller, map[string]any{"type": "intent.invoke", "id": "mismatch", "request": map[string]any{
		"archetype": "profile", "action": "open", "convention": "napplet:note/open",
	}})
	rejected := recCaller.wait(t, "intent.invoke.result", 1)["result"].(map[string]any)
	if rejected["ok"] != false || rejected["error"] != "invalid convention" {
		t.Fatalf("mismatched convention accepted: %v", rejected)
	}

	post(t, caller, map[string]any{"type": "intent.invoke", "id": "invoke", "request": map[string]any{
		"archetype": "profile", "action": "open", "convention": "napplet:profile/open",
		"payload": map[string]any{"pubkey": "abc"},
	}})
	result := recCaller.wait(t, "intent.invoke.result", 2)["result"].(map[string]any)
	if result["ok"] != true || result["handler"] != handler.napp.D || result["handled"] != nil || result["windowId"] != nil {
		t.Fatalf("acceptance result: %v", result)
	}

	// End the source session before the target becomes ready. Accepted delivery
	// belongs to the runtime and must still reach the target afterward.
	caller.napReset()
	ready(t, handler, recHandler, 1)
	delivery := recHandler.wait(t, "intent.deliver", 1)["delivery"].(map[string]any)
	if delivery["sender"] != caller.napp.D || delivery["convention"] != "napplet:profile/open" ||
		delivery["payload"].(map[string]any)["pubkey"] != "abc" {
		t.Errorf("delivery: %v", delivery)
	}
}

// ─── link ────────────────────────────────────────────────────────

func TestNapLinkRejectsSchemes(t *testing.T) {
	setupNapTest(t)
	ci, rec := openNapplet(t, "linker")
	ready(t, ci, rec, 1)
	for i, u := range []string{"javascript:alert(1)", "data:text/html,x", "file:///etc/passwd", "blob:null/x"} {
		post(t, ci, map[string]any{"type": "link.open", "id": u, "url": u})
		got := rec.wait(t, "link.open.result", i+1)
		if got["id"] != u || got["status"] != "denied" || got["error"] != "unsupported-scheme" {
			t.Errorf("%s: %v", u, got)
		}
	}
}

type napLinkTestHost struct {
	noopHost
	opened []string
	err    error
}

func (h *napLinkTestHost) OpenLink(url string) error {
	h.opened = append(h.opened, url)
	return h.err
}

func TestNapLinkResultsAndLabelSanitizing(t *testing.T) {
	setupNapTest(t)
	ci, rec := openNapplet(t, "link-results")
	ready(t, ci, rec, 1)
	key := RuleKey{Napp: ci.napp.ID, Permission: PermOpenLink}
	t.Cleanup(func() { clearSessionRule(key) })

	invalid := []struct {
		id, url, code string
	}{
		{"missing", "", "invalid-url"},
		{"relative", "/somewhere", "invalid-url"},
		{"hostless", "https:///somewhere", "invalid-url"},
	}
	for i, tc := range invalid {
		post(t, ci, map[string]any{"type": "link.open", "id": tc.id, "url": tc.url})
		got := rec.wait(t, "link.open.result", i+1)
		if got["id"] != tc.id || got["status"] != "denied" || got["error"] != tc.code {
			t.Errorf("%s: %v", tc.id, got)
		}
	}

	setSessionRule(key, Rule{Decision: DecisionDeny})
	post(t, ci, map[string]any{"type": "link.open", "id": "deny", "url": "https://example.com"})
	denied := rec.wait(t, "link.open.result", len(invalid)+1)
	if denied["status"] != "denied" || denied["error"] != "user-denied" {
		t.Fatalf("user denial: %v", denied)
	}

	linkHost := &napLinkTestHost{}
	host = linkHost
	setSessionRule(key, Rule{Decision: DecisionAllow})
	post(t, ci, map[string]any{
		"type": "link.open", "id": "open", "url": "https://example.com/read",
		"options": map[string]any{"label": "Read this\nmisleading page"},
	})
	opened := rec.wait(t, "link.open.result", len(invalid)+2)
	if opened["id"] != "open" || opened["status"] != "opened" || opened["error"] != nil {
		t.Fatalf("opened result: %v", opened)
	}
	if !slices.Equal(linkHost.opened, []string{"https://example.com/read"}) {
		t.Fatalf("opened URLs: %v", linkHost.opened)
	}

	linkHost.err = errors.New("platform details must not cross the wire")
	post(t, ci, map[string]any{"type": "link.open", "id": "failure", "url": "https://example.com/fail"})
	failed := rec.wait(t, "link.open.result", len(invalid)+3)
	if failed["status"] != "denied" || failed["error"] != "blocked-by-policy" {
		t.Fatalf("platform failure: %v", failed)
	}
}

func TestNapLinkLabel(t *testing.T) {
	if got := napLinkLabel("  Read\nthis\t\u202epage  "); got != "Read this page" {
		t.Fatalf("sanitized label = %q", got)
	}
	long := strings.Repeat("界", maxNapLinkLabelRunes+1)
	if got := napLinkLabel(long); len([]rune(got)) != maxNapLinkLabelRunes+1 || !strings.HasSuffix(got, "…") {
		t.Fatalf("truncated label has %d runes and suffix %q", len([]rune(got)), got[len(got)-3:])
	}
}

// ─── network guard + resources ───────────────────────────────────

func TestPublicAddr(t *testing.T) {
	for _, s := range []string{"127.0.0.1", "10.1.2.3", "192.168.1.1", "172.20.0.1", "169.254.169.254",
		"100.64.0.1", "0.0.0.0", "::1", "fe80::1", "fd00::1", "::ffff:127.0.0.1"} {
		if publicAddr(netip.MustParseAddr(s)) {
			t.Errorf("%s counted as public", s)
		}
	}
	for _, s := range []string{"1.1.1.1", "8.8.8.8", "2606:4700:4700::1111"} {
		if !publicAddr(netip.MustParseAddr(s)) {
			t.Errorf("%s counted as private", s)
		}
	}
	if err := publicHost(context.Background(), "localhost"); err == nil {
		t.Error("localhost allowed")
	}
	if err := publicHost(context.Background(), "[::1]"); err == nil {
		t.Error("::1 allowed")
	}
}

func TestResourceFetchBlocksLoopback(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("secret"))
	}))
	defer srv.Close()
	_, err := httpsResource(context.Background(), srv.URL)
	var re *resourceErr
	if err == nil || !errorsAs(err, &re) || re.code != "blocked-by-policy" {
		t.Fatalf("loopback fetch: %v", err)
	}
	if _, err := napExplicitRelay(context.Background(), "ws://127.0.0.1:7777"); err == nil {
		t.Error("loopback relay allowed")
	}
	if _, err := napExplicitRelay(context.Background(), "https://relay.example.com"); err == nil {
		t.Error("non-websocket relay allowed")
	}
}

func TestNapResourceTrackKeepsRequestIDOwnership(t *testing.T) {
	setupNapTest(t)
	ci, rec := openNapplet(t, "resource-tracker")
	ready(t, ci, rec, 1)
	call := func() *napCall {
		return &napCall{ci: ci, gen: ci.nap.gen, ctx: ci.nap.ctx, ID: json.RawMessage(`"request"`)}
	}

	first := call()
	firstCtx, firstDone, ok := first.resourceTrack()
	if !ok {
		t.Fatal("first request was rejected")
	}
	duplicate := call()
	duplicate.Type = "resource.bytes"
	duplicate.raw = json.RawMessage(`{"url":"data:text/plain,duplicate"}`)
	napResourceBytes(duplicate)
	if got := rec.wait(t, "resource.bytes.error", 1); got["error"] != "duplicate-request" {
		t.Fatalf("duplicate request was not rejected: %v", got)
	}

	// Cancellation releases A's id immediately, so B may reuse it before A's
	// deferred completion runs. A must not delete B's registration.
	napResourceCancel(call())
	select {
	case <-firstCtx.Done():
	default:
		t.Fatal("live request was not cancelled")
	}

	secondCtx, secondDone, ok := call().resourceTrack()
	if !ok {
		t.Fatal("request id was not reusable after cancellation")
	}
	// A's first deferred completion is stale and must not remove B.
	firstDone()
	ci.nap.mu.Lock()
	_, tracked := ci.nap.fetches[`"request"`]
	ci.nap.mu.Unlock()
	if !tracked {
		t.Fatal("old completion removed the newer request registration")
	}
	napResourceCancel(call())
	select {
	case <-secondCtx.Done():
	default:
		t.Fatal("new request was not cancelled")
	}
	secondDone()
	ci.nap.mu.Lock()
	_, tracked = ci.nap.fetches[`"request"`]
	ci.nap.mu.Unlock()
	if tracked {
		t.Fatal("completed request remained registered")
	}
}

func TestNapResourceInfoReportsCurrentBulkLimits(t *testing.T) {
	setupNapTest(t)
	ci, rec := openNapplet(t, "resource-info")
	ready(t, ci, rec, 1)
	c := &napCall{ci: ci, gen: ci.nap.gen, ctx: ci.nap.ctx, Type: "resource.info", ID: json.RawMessage(`"info"`)}
	napResourceInfo(c)
	got := rec.wait(t, "resource.info.result", 1)
	info, ok := got["info"].(map[string]any)
	if !ok || info["maxUrls"] != float64(resourceMaxURLs) || info["maxServers"] != float64(resourceMaxServers) {
		t.Fatalf("resource info limits: %#v", got["info"])
	}
}

func TestNapResourceBytesManyUsesTooLargeForBulkCap(t *testing.T) {
	setupNapTest(t)
	ci, rec := openNapplet(t, "resource-many-cap")
	ready(t, ci, rec, 1)
	requests := make([]map[string]any, resourceMaxURLs+1)
	for i := range requests {
		requests[i] = map[string]any{"url": "data:text/plain,ok"}
	}
	raw, err := json.Marshal(map[string]any{"requests": requests})
	if err != nil {
		t.Fatal(err)
	}
	c := &napCall{ci: ci, gen: ci.nap.gen, ctx: ci.nap.ctx, Type: "resource.bytesMany", ID: json.RawMessage(`"many"`), raw: raw}
	napResourceBytesMany(c)
	if got := rec.wait(t, "resource.bytesMany.error", 1); got["error"] != "too-large" {
		t.Fatalf("bulk cap error: %#v", got)
	}
}

func TestBlossomResourceCapsServerHints(t *testing.T) {
	hints := make([]string, resourceMaxServers+1)
	_, err := fetchBlossomResource(context.Background(), nil, "sha256:"+strings.Repeat("0", 64), hints)
	var re *resourceErr
	if !errors.As(err, &re) || re.code != "too-large" {
		t.Fatalf("server hint cap: %v", err)
	}
}

func TestSniffResource(t *testing.T) {
	var buf bytes.Buffer
	_ = png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 1, 1)))
	if m, err := sniffResource(buf.Bytes(), "text/html"); err != nil || m != "image/png" {
		t.Errorf("png: %q %v", m, err)
	}
	for name, body := range map[string]string{
		"svg":  `<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"></svg>`,
		"svg2": `<svg xmlns="http://www.w3.org/2000/svg"><script>x</script></svg>`,
		"html": `<!doctype html><script>steal()</script>`,
	} {
		if m, err := sniffResource([]byte(body), "image/png"); err == nil {
			t.Errorf("%s served as %q", name, m)
		}
	}
	if m, err := sniffResource([]byte(`{"a":1}`), "application/json; charset=utf-8"); err != nil || m != "application/json" {
		t.Errorf("json: %q %v", m, err)
	}
	if r, err := decodeDataURL("data:image/png;base64," + b64(buf.Bytes())); err != nil || r.mime != "image/png" {
		t.Errorf("data url: %v", err)
	}
}

func errorsAs(err error, target any) bool { return errors.As(err, target) }

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func TestNappletDocumentChecksTheHash(t *testing.T) {
	setupNapTest(t)
	html := []byte("<!doctype html><p>napplet</p>")
	sum := sha256.Sum256(html)
	n := Napp{ID: "napplet~0123456789abcdef~doc", D: "doc", Format: FormatNapplet,
		Paths: []NappPath{{Path: "/index.html", Sha256: hex.EncodeToString(sum[:])}}}
	if err := os.MkdirAll(nappBaseDir(n.ID), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(nappBaseDir(n.ID), "index.html")
	if err := os.WriteFile(path, html, 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := nappletDocument(n); err != nil || !bytes.Equal(got, html) {
		t.Fatalf("verified document refused: %v", err)
	}
	// changed on disk after install: never runs
	if err := os.WriteFile(path, append(html, '!'), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := nappletDocument(n); err == nil {
		t.Fatal("tampered document accepted")
	}
}

func TestNapCommonNip19(t *testing.T) {
	setupNapTest(t)
	ci, rec := openNapplet(t, "nip19")
	ready(t, ci, rec, 1)
	pk := nostr.Generate().Public()

	post(t, ci, map[string]any{"type": "common.encodeNip19", "id": "e1", "input": map[string]any{"type": "npub", "hex": pk.Hex()}})
	enc := rec.wait(t, "common.encodeNip19.result", 1)
	if enc["ok"] != true || enc["value"] != nip19.EncodeNpub(pk) {
		t.Fatalf("encode: %v", enc)
	}
	post(t, ci, map[string]any{"type": "common.encodeNip19", "id": "e2", "input": map[string]any{
		"type": "naddr", "pubkey": pk.Hex(), "kind": 30023, "identifier": "post", "relays": []string{"wss://r.example.com"}}})
	naddr := rec.wait(t, "common.encodeNip19.result", 2)["value"].(string)

	post(t, ci, map[string]any{"type": "common.decodeNip19", "id": "d1", "value": "nostr:" + naddr})
	dec := rec.wait(t, "common.decodeNip19.result", 1)
	if dec["ok"] != true || dec["nip19Type"] != "naddr" || dec["pubkey"] != pk.Hex() ||
		dec["identifier"] != "post" || dec["kind"] != float64(30023) {
		t.Errorf("decode naddr: %v", dec)
	}

	post(t, ci, map[string]any{"type": "common.encodeNip19", "id": "e3", "input": map[string]any{"type": "nrelay", "relay": "wss://r.example.com"}})
	nrelay := rec.wait(t, "common.encodeNip19.result", 3)["value"].(string)
	post(t, ci, map[string]any{"type": "common.decodeNip19", "id": "d2", "value": nrelay})
	if got := rec.wait(t, "common.decodeNip19.result", 2); got["relay"] != "wss://r.example.com" {
		t.Errorf("nrelay round trip: %v", got)
	}

	// never secret keys
	post(t, ci, map[string]any{"type": "common.decodeNip19", "id": "d3", "value": nip19.EncodeNsec(nostr.Generate())})
	if got := rec.wait(t, "common.decodeNip19.result", 3); got["ok"] != false || got["pubkey"] != nil {
		t.Errorf("nsec decoded: %v", got)
	}
	post(t, ci, map[string]any{"type": "common.encodeNip19", "id": "e4", "input": map[string]any{"type": "nsec", "hex": pk.Hex()}})
	if got := rec.wait(t, "common.encodeNip19.result", 4); got["ok"] != false {
		t.Errorf("nsec encoded: %v", got)
	}
}
