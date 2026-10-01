package backend

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"

	"verdana/backend/webview"
)

// The napplet runtime's Go half. A napplet window's host page (webview's
// napplet-host.js) forwards every NAP envelope its sandboxed frame posts as a
// nap.msg rpc; this file decides what each one means. Replies and everything
// the launcher has to say unprompted (relay events, inc events, theme and
// identity changes) go back the same way: a __nap_push eval, which the host
// page re-posts into the frame.
//
// Envelopes are handled one at a time, in the order the napplet sent them
// (a subscribe is never overtaken by its own close). A handler that has to
// wait — on the network, on the user — does so in its own goroutine, tied to
// the session, and replies when it is done.

// napDomains are the NAP domains the launcher implements, and so the ones
// the shim installs window.napplet.<domain> for. It is the launcher's policy,
// never the napplet's R/O tags (WEB-NAPPLET.md forbids gating on those).
var napDomains = []string{
	"relay", "identity", "storage", "resource", "common",
	"theme", "inc", "intent", "link", "upload", "outbox",
}

// napSession is what one napplet window has going: the subscriptions and
// channels a reset or a close has to tear down.
type napSession struct {
	mu sync.Mutex

	// established flips on shell.ready. Before it, every envelope is dropped
	// (NAP-SHELL): the activation script sends it ahead of any napplet code.
	established bool
	ready       chan struct{}

	// gen counts sessions in this window: a napplet that reloads itself (a
	// second shell.ready) starts a new one, and a late answer for the old
	// one must not reach the new document.
	gen    int
	ctx    context.Context
	cancel context.CancelFunc

	// relay subscriptions by subId
	subs map[string]context.CancelFunc
	// inc topics this napplet listens on (a set: the shim subscribes once per
	// handler but unsubscribes once per topic)
	topics map[string]bool
	// open resource requests by id, for resource.cancel
	fetches map[string]context.CancelFunc
	// uploads are scoped to one document/session. A reload cancels active
	// network work and makes its upload ids unreachable to the new document.
	uploads map[string]*napUploadStatus
	// the session's answers to its standing questions ("may it fetch from
	// the web", "may it read encrypted messages"): asked once per session,
	// grantMu makes concurrent requests wait for that one question
	grantMu sync.Mutex
	grants  map[Permission]bool

	// queue serializes envelopes; started lazily by the first one
	queue chan napCall
	once  sync.Once
}

func newNapSession() *napSession {
	s := &napSession{}
	s.resetLocked()
	return s
}

// resetLocked tears the session's state down and starts a fresh generation.
// The caller holds s.mu (or owns s exclusively).
func (s *napSession) resetLocked() {
	if s.ready != nil && !s.established {
		close(s.ready)
	}
	if s.cancel != nil {
		s.cancel()
	}
	for _, cancel := range s.subs {
		cancel()
	}
	for _, cancel := range s.fetches {
		cancel()
	}
	s.gen++
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.subs = make(map[string]context.CancelFunc)
	s.topics = make(map[string]bool)
	s.fetches = make(map[string]context.CancelFunc)
	for _, upload := range s.uploads {
		if upload.cancel != nil {
			upload.cancel()
		}
	}
	s.uploads = make(map[string]*napUploadStatus)
	s.grants = make(map[Permission]bool)
	s.established = false
	s.ready = make(chan struct{})
}

// napCall is one envelope from the napplet.
type napCall struct {
	ci  *Instance
	gen int
	ctx context.Context

	Type string
	// ID is echoed back verbatim: the shim's ids are uuid strings, but
	// nothing here needs to know that.
	ID  json.RawMessage
	raw json.RawMessage
}

// napHandler handles one envelope type. It runs on the session's queue, so
// it must not block: anything slow goes through c.async.
type napHandler func(c *napCall)

var napHandlers = map[string]napHandler{}

// handleNap registers handlers; each nap_*.go file does it in its init.
func handleNap(types map[string]napHandler) {
	for t, h := range types {
		if _, dup := napHandlers[t]; dup {
			panic("duplicate NAP handler for " + t)
		}
		napHandlers[t] = h
	}
}

func (c *napCall) decode(v any) error { return json.Unmarshal(c.raw, v) }

// envelope builds a message of the given type, carrying this call's id.
func (c *napCall) envelope(typ string, fields map[string]any) map[string]any {
	out := make(map[string]any, len(fields)+2)
	for k, v := range fields {
		out[k] = v
	}
	out["type"] = typ
	if len(c.ID) > 0 {
		out["id"] = c.ID
	}
	return out
}

// reply answers with <type>.result.
func (c *napCall) reply(fields map[string]any) {
	c.ci.napPushGen(c.gen, c.envelope(c.Type+".result", fields))
}

// replyAs answers with an explicit type (the resource and relay error types).
func (c *napCall) replyAs(typ string, fields map[string]any) {
	c.ci.napPushGen(c.gen, c.envelope(typ, fields))
}

// async runs fn off the queue, on the session's context: a reset or a closed
// window cancels it, and whatever it replies after that is dropped.
func (c *napCall) async(fn func(ctx context.Context)) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Error().Interface("panic", r).Str("type", c.Type).Msg("NAP handler panicked")
			}
		}()
		fn(c.ctx)
	}()
}

// ─── pushing into the napplet ────────────────────────────────────

// napPush sends envelopes to the napplet in this window, in one eval.
func (ci *Instance) napPush(envs ...any) {
	if ci.nap == nil || len(envs) == 0 {
		return
	}
	ci.nap.mu.Lock()
	gen := ci.nap.gen
	ci.nap.mu.Unlock()
	ci.napPushGen(gen, envs...)
}

// napPushGen pushes only if the session is still the one gen names.
func (ci *Instance) napPushGen(gen int, envs ...any) {
	if ci.nap == nil || len(envs) == 0 {
		return
	}
	ci.nap.mu.Lock()
	live := ci.nap.gen == gen && ci.nap.established
	ci.nap.mu.Unlock()
	if !live {
		return
	}
	var payload any = envs
	if len(envs) == 1 {
		payload = envs[0]
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		log.Error().Err(err).Msg("could not encode a NAP push")
		return
	}
	ci.eval("window.__nap_push && window.__nap_push(" + jsString(string(raw)) + ")")
}

// ─── the rpcs the host page makes ────────────────────────────────

// napRPC answers the host page. Nothing else is reachable from a napplet
// window: see bridgeRPC.
func napRPC(ci *Instance, method, params string) (any, error) {
	switch method {
	case "nap.boot":
		return nappletBoot(ci)
	case "nap.msg":
		ci.napEnqueue(params)
		return nil, nil
	case "nap.reset":
		ci.napReset()
		return nil, nil
	}
	return nil, fmt.Errorf("unsupported method: %s", method)
}

// napEnqueue parses one envelope and puts it on the session's queue. Anything
// that isn't a well-formed envelope is dropped without a word: unknown input
// must never tell a napplet anything (NIP-5D).
func (ci *Instance) napEnqueue(params string) {
	s := ci.nap
	if s == nil {
		return
	}
	// the host page sends the envelope as a JSON string (rpc params are
	// JSON); accept the object form too
	raw := json.RawMessage(params)
	var str string
	if json.Unmarshal(raw, &str) == nil {
		raw = json.RawMessage(str)
	}
	var head struct {
		Type string          `json:"type"`
		ID   json.RawMessage `json:"id"`
	}
	if err := json.Unmarshal(raw, &head); err != nil || head.Type == "" {
		return
	}

	s.once.Do(func() {
		s.queue = make(chan napCall, 256)
		go ci.napWorker()
	})

	s.mu.Lock()
	call := napCall{ci: ci, gen: s.gen, ctx: s.ctx, Type: head.Type, ID: head.ID, raw: raw}
	s.mu.Unlock()

	select {
	case s.queue <- call:
	case <-ci.gone:
	}
}

func (ci *Instance) napWorker() {
	s := ci.nap
	for {
		select {
		case <-ci.gone:
			return
		case c := <-s.queue:
			ci.napDispatch(c)
		}
	}
}

func (ci *Instance) napDispatch(c napCall) {
	s := ci.nap
	if c.Type == "shell.ready" {
		ci.napReady()
		return
	}

	s.mu.Lock()
	ok := s.established
	// the call was read under an older session: let it go
	stale := c.gen != s.gen
	c.gen, c.ctx = s.gen, s.ctx
	s.mu.Unlock()
	if !ok || stale {
		return
	}

	h := napHandlers[c.Type]
	if h == nil {
		// unknown types are dropped silently, never answered
		log.Debug().Str("type", c.Type).Str("napplet", ci.napp.ID).Msg("ignoring unknown NAP message")
		return
	}
	defer func() {
		if r := recover(); r != nil {
			log.Error().Interface("panic", r).Str("type", c.Type).Msg("NAP handler panicked")
		}
	}()
	h(&c)
}

// napReady handles shell.ready: the session starts and the napplet learns
// which domains it has. A repeated shell.ready comes from a new document in
// the same frame (the activation script sends exactly one per load), so it
// is a new session and nothing of the old one carries over.
func (ci *Instance) napReady() {
	s := ci.nap
	s.mu.Lock()
	if s.established {
		ci.napTeardownLocked("napplet reloaded")
	}
	s.established = true
	close(s.ready)
	gen := s.gen
	s.mu.Unlock()

	ci.napPushGen(gen, map[string]any{
		"type":         "shell.init",
		"capabilities": map[string]any{"domains": napDomains},
		"services":     []any{},
	})
	log.Info().Str("napplet", ci.napp.ID).Str("instance", ci.instance).Msg("napplet session started")
}

// napReset drops the session (a dev reload: new bytes are coming).
func (ci *Instance) napReset() {
	if ci.nap == nil {
		return
	}
	ci.nap.mu.Lock()
	ci.napTeardownLocked("napplet reset")
	ci.nap.mu.Unlock()
}

// napTeardownLocked ends the current session: subscriptions, resource
// fetches, inc topics and channels. The caller holds ci.nap.mu.
func (ci *Instance) napTeardownLocked(reason string) {
	incForget(ci, reason)
	ci.nap.resetLocked()
}

// napClosed is WindowClosed's part for a napplet window.
func (ci *Instance) napClosed() {
	if ci.nap == nil {
		return
	}
	ci.nap.mu.Lock()
	ci.napTeardownLocked("peer destroyed")
	ci.nap.mu.Unlock()
}

// ─── the document ────────────────────────────────────────────────

// nappletBoot answers nap.boot: the napplet's verified HTML, wrapped for the
// sandboxed frame.
func nappletBoot(ci *Instance) (any, error) {
	html, err := nappletDocument(ci.napp)
	if err != nil {
		log.Error().Err(err).Str("napplet", ci.napp.ID).Msg("napplet will not boot")
		return nil, err
	}
	doc, err := buildSrcdoc(html, napDomains)
	if err != nil {
		return nil, err
	}
	return map[string]any{"srcdoc": doc, "title": ci.napp.Label()}, nil
}

// nappletDocument reads the napplet's one file: from a dev napplet's folder,
// or from the install dir, re-checked against the event's hash right before
// it runs, so bytes changed on disk after install never execute.
func nappletDocument(n Napp) ([]byte, error) {
	if d := devLookup(n.ID); d != nil {
		if d.dir == "" {
			return nil, errors.New("dev napplets load from a folder")
		}
		return os.ReadFile(filepath.Join(d.dir, "index.html"))
	}
	data, err := os.ReadFile(filepath.Join(nappBaseDir(n.ID), "index.html"))
	if err != nil {
		return nil, fmt.Errorf("napplet %s is not installed", n.ID)
	}
	// the file that runs is index.html: for a WEB-NAPPLET its hash is the x
	// tag, for a NIP-5D manifest it is index.html's own path tag (whose
	// aggregate was checked against x when the event was read)
	want := n.IndexHash()
	sum := sha256.Sum256(data)
	if want == "" || hex.EncodeToString(sum[:]) != want {
		return nil, fmt.Errorf("napplet %s: installed file does not match its hash", n.ID)
	}
	return data, nil
}

// nappletCSP is NIP-5D's policy for the napplet's frame: inline script and
// style (the napplet is one file), wasm, data:/blob: images and fonts, and no
// network, no workers, no frames, no navigation targets of its own.
const nappletCSP = "default-src 'none'; script-src 'unsafe-inline' 'wasm-unsafe-eval'; " +
	"style-src 'unsafe-inline'; img-src data: blob:; font-src data:; connect-src 'none'; " +
	"worker-src 'none'; child-src 'none'; frame-src 'none'; media-src 'none'; " +
	"object-src 'none'; manifest-src 'none'; base-uri 'none'; form-action 'none'"

// buildSrcdoc puts the launcher's preamble in front of everything the
// napplet's document does: the CSP first, then the shim, then the activation
// (window.napplet for the given domains, and shell.ready).
//
// The preamble is not spliced into the napplet's HTML (finding "its <head>"
// in untrusted markup is a parsing contest the napplet can win, with a
// comment or a script that mentions <head>). Instead the document starts with
// the launcher's own doctype, <html> and <head>, closed after the preamble,
// and the napplet's bytes follow verbatim. HTML parsing then does the rest:
// the napplet's own doctype and <head> tag are ignored, its head elements
// (meta, title, style, script, link) are moved into this head after the
// preamble, and its <html> attributes are merged onto this <html>. Nothing
// the napplet writes can come before the CSP or the shim.
func buildSrcdoc(html []byte, domains []string) (string, error) {
	if !utf8.Valid(html) {
		return "", errors.New("napplet document is not valid UTF-8")
	}
	doc := strings.TrimPrefix(string(html), "\uFEFF")

	domainsJSON, err := json.Marshal(map[string]any{"domains": domains})
	if err != nil {
		return "", err
	}
	// "</script" cannot appear inside an inline script; the prelude has none
	// today, and this keeps a future one from ending the element early
	prelude := strings.ReplaceAll(webview.ShimPrelude(), "</script", `<\/script`)

	return "<!doctype html><html><head>" +
		`<meta http-equiv="Content-Security-Policy" content="` + nappletCSP + `">` +
		"<script>" + prelude + "\n</script>" +
		"<script>globalThis.NappletShimPrelude.install(" + string(domainsJSON) + ");" +
		`window.parent.postMessage({type:"shell.ready"},"*");</script>` +
		"</head>" + doc, nil
}

// sessionGrant asks once per session (or not at all, once the user said
// "always") whether the napplet may do something it will want to do over
// and over: fetching, decrypting. Every caller after the first gets the same
// answer without another prompt.
func (c *napCall) sessionGrant(perm Permission, title, detail string) bool {
	s := c.ci.nap
	s.grantMu.Lock()
	defer s.grantMu.Unlock()
	s.mu.Lock()
	ok, decided := s.grants[perm]
	stale := s.gen != c.gen
	s.mu.Unlock()
	if stale {
		return false
	}
	if decided {
		return ok
	}
	ok = askApproval(c.ci, perm, title, detail, "")
	s.mu.Lock()
	if s.gen == c.gen {
		s.grants[perm] = ok
	}
	s.mu.Unlock()
	return ok
}
