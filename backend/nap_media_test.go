package backend

import (
	"errors"
	"slices"
	"sync"
	"testing"
	"time"
)

type fakeMediaPlayer struct {
	mu      sync.Mutex
	calls   []string
	onState func(MediaState)
}

func (p *fakeMediaPlayer) record(s string) error {
	p.mu.Lock()
	p.calls = append(p.calls, s)
	p.mu.Unlock()
	return nil
}
func (p *fakeMediaPlayer) Play() error             { return p.record("play") }
func (p *fakeMediaPlayer) Pause() error            { return p.record("pause") }
func (p *fakeMediaPlayer) Stop() error             { return p.record("stop") }
func (p *fakeMediaPlayer) Seek(float64) error      { return p.record("seek") }
func (p *fakeMediaPlayer) SetVolume(float64) error { return p.record("volume") }
func (p *fakeMediaPlayer) SetTitle(t string) error { return p.record("title:" + t) }
func (p *fakeMediaPlayer) called() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.calls)
}

type mediaTestHost struct {
	noopHost
	mu      sync.Mutex
	reqs    []MediaRequest
	players []*fakeMediaPlayer
	err     error
}

func (h *mediaTestHost) MediaPlay(req MediaRequest, onState func(MediaState)) (MediaPlayer, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.err != nil {
		return nil, h.err
	}
	p := &fakeMediaPlayer{onState: onState}
	h.reqs = append(h.reqs, req)
	h.players = append(h.players, p)
	return p, nil
}

func (h *mediaTestHost) player(t *testing.T, i int) *fakeMediaPlayer {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	if i >= len(h.players) {
		t.Fatalf("player %d never started (%d started)", i, len(h.players))
	}
	return h.players[i]
}

// setupMediaTest opens a ready napplet whose media permission is granted.
func setupMediaTest(t *testing.T, d string) (*Instance, *recTransport, *mediaTestHost) {
	t.Helper()
	setupNapTest(t)
	mh := &mediaTestHost{}
	host = mh
	ci, rec := openNapplet(t, d)
	ready(t, ci, rec, 1)
	key := RuleKey{Napp: ci.napp.ID, Permission: PermMedia}
	setSessionRule(key, Rule{Decision: DecisionAllow})
	t.Cleanup(func() {
		clearSessionRule(key)
		mediaOutput.Lock()
		mediaOutput.cur = mediaHolder{}
		mediaOutput.Unlock()
	})
	return ci, rec, mh
}

func shellCreate(id, url string) map[string]any {
	return map[string]any{
		"type": "media.session.create", "id": id, "owner": "shell",
		"source": map[string]any{"url": url}, "metadata": map[string]any{"title": "Song", "artist": "Band"},
		"autoplay": true,
	}
}

func waitCalls(t *testing.T, p *fakeMediaPlayer, want []string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !slices.Equal(p.called(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("player calls = %v, want %v", p.called(), want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestMediaCreateRequiresOwner(t *testing.T) {
	ci, rec, _ := setupMediaTest(t, "media-owner")
	post(t, ci, map[string]any{"type": "media.session.create", "id": "a"})
	if got := rec.wait(t, "media.session.create.result", 1); got["error"] != "missing owner" || got["sessionId"] != nil {
		t.Fatalf("no owner: %v", got)
	}
	post(t, ci, map[string]any{"type": "media.session.create", "id": "b", "owner": "someone"})
	if got := rec.wait(t, "media.session.create.result", 2); got["error"] != "unsupported owner mode" {
		t.Fatalf("bad owner: %v", got)
	}
}

func TestMediaShellOwnedRejectsBadSources(t *testing.T) {
	ci, rec, mh := setupMediaTest(t, "media-sources")
	cases := []struct {
		env  map[string]any
		want string
	}{
		{map[string]any{"type": "media.session.create", "id": "1", "owner": "shell"}, "missing source"},
		{shellCreate("2", "http://1.1.1.1/a.mp3"), "unsupported source"},
		{shellCreate("3", "file:///etc/passwd"), "unsupported source"},
		{shellCreate("4", "https://user:pw@1.1.1.1/a.mp3"), "unsupported source"},
		{shellCreate("5", "https://127.0.0.1/a.mp3"), "source blocked"},
		{shellCreate("6", "https://localhost/a.mp3"), "source blocked"},
		{map[string]any{"type": "media.session.create", "id": "7", "owner": "shell",
			"source": map[string]any{"blossomHash": "nothex"}}, "unsupported source"},
		{map[string]any{"type": "media.session.create", "id": "8", "owner": "shell",
			"source": map[string]any{"nostr": map[string]any{"eventId": "abc"}}}, "unsupported source"},
	}
	for i, tc := range cases {
		post(t, ci, tc.env)
		got := rec.wait(t, "media.session.create.result", i+1)
		if got["id"] != tc.env["id"] || got["error"] != tc.want {
			t.Errorf("%v: got %v, want error %q", tc.env["id"], got, tc.want)
		}
	}
	if len(mh.reqs) != 0 {
		t.Fatalf("a player started: %v", mh.reqs)
	}
	ci.nap.mu.Lock()
	n := len(ci.nap.media)
	ci.nap.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d failed sessions kept", n)
	}
}

func TestMediaShellOwnedPlaysAndPushesState(t *testing.T) {
	ci, rec, mh := setupMediaTest(t, "media-play")
	post(t, ci, shellCreate("m1", "https://1.1.1.1/live.mp3"))
	res := rec.wait(t, "media.session.create.result", 1)
	id, _ := res["sessionId"].(string)
	if res["id"] != "m1" || res["owner"] != "shell" || id == "" || res["error"] != nil {
		t.Fatalf("create result: %v", res)
	}
	if req := mh.reqs[0]; req.URL != "https://1.1.1.1/live.mp3" || req.Title != "Band – Song" || !req.Autoplay {
		t.Fatalf("player request: %+v", req)
	}
	caps := rec.wait(t, "media.capabilities", 1)
	if caps["sessionId"] != id || !slices.Contains(toStrings(caps["actions"]), "seek") {
		t.Fatalf("capabilities: %v", caps)
	}
	rec.wait(t, "media.controls", 1)

	p := mh.player(t, 0)
	pos := 1.0
	p.onState(MediaState{Status: "playing", Position: &pos})
	st := rec.wait(t, "media.state", 1)
	if st["sessionId"] != id || st["status"] != "playing" || st["position"] != 1.0 {
		t.Fatalf("state: %v", st)
	}
	// position alone, inside the interval: held back; a status change: sent
	pos2 := 1.1
	p.onState(MediaState{Status: "playing", Position: &pos2})
	p.onState(MediaState{Status: "paused", Position: &pos2})
	if st := rec.wait(t, "media.state", 2); st["status"] != "paused" {
		t.Fatalf("second state: %v", st)
	}
	if n := len(rec.find("media.state")); n != 2 {
		t.Fatalf("%d states pushed, want 2", n)
	}

	post(t, ci, map[string]any{"type": "media.session.update", "sessionId": id, "metadata": map[string]any{"title": "Other"}})
	post(t, ci, map[string]any{"type": "media.session.destroy", "sessionId": id})
	waitCalls(t, p, []string{"title:Band – Other", "stop"})
}

func TestMediaLiveCannotSeek(t *testing.T) {
	ci, rec, mh := setupMediaTest(t, "media-live")
	env := shellCreate("m", "https://1.1.1.1/live.mp3")
	env["live"] = true
	post(t, ci, env)
	id := rec.wait(t, "media.session.create.result", 1)["sessionId"]
	if caps := toStrings(rec.wait(t, "media.capabilities", 1)["actions"]); slices.Contains(caps, "seek") {
		t.Fatalf("live capabilities: %v", caps)
	}
	post(t, ci, map[string]any{"type": "media.command", "sessionId": id, "action": "seek", "value": 3})
	post(t, ci, map[string]any{"type": "media.command", "sessionId": id, "action": "pause"})
	waitCalls(t, mh.player(t, 0), []string{"pause"})
}

func TestMediaCommandRoutesToPlayer(t *testing.T) {
	ci, rec, mh := setupMediaTest(t, "media-command")
	post(t, ci, shellCreate("m", "https://1.1.1.1/a.mp3"))
	id := rec.wait(t, "media.session.create.result", 1)["sessionId"]
	post(t, ci, map[string]any{"type": "media.session.create", "id": "n", "owner": "napplet",
		"capabilities": []string{"play", "pause"}})
	own := rec.wait(t, "media.session.create.result", 2)["sessionId"]

	for _, env := range []map[string]any{
		{"type": "media.command", "sessionId": id, "action": "volume", "value": 1.5},
		{"type": "media.command", "sessionId": id, "action": "volume"},
		{"type": "media.command", "sessionId": id, "action": "seek", "value": -1},
		{"type": "media.command", "sessionId": id, "action": "next"},
		{"type": "media.command", "sessionId": "shell-999", "action": "play"},
		{"type": "media.command", "sessionId": own, "action": "play"},
		{"type": "media.command", "sessionId": id, "action": "play"},
		{"type": "media.command", "sessionId": id, "action": "seek", "value": 30},
		{"type": "media.command", "sessionId": id, "action": "volume", "value": 0.5},
		{"type": "media.command", "sessionId": id, "action": "stop"},
	} {
		post(t, ci, env)
	}
	waitCalls(t, mh.player(t, 0), []string{"play", "seek", "volume", "stop"})
}

func TestMediaSessionLimit(t *testing.T) {
	ci, rec, _ := setupMediaTest(t, "media-limit")
	for i := 1; i <= mediaMaxSessions+1; i++ {
		post(t, ci, map[string]any{"type": "media.session.create", "id": i, "owner": "napplet"})
	}
	got := rec.wait(t, "media.session.create.result", mediaMaxSessions+1)
	if got["error"] != "session limit exceeded" {
		t.Fatalf("over the limit: %v", got)
	}
	first := rec.find("media.session.create.result")[0]
	post(t, ci, map[string]any{"type": "media.session.destroy", "sessionId": first["sessionId"]})
	post(t, ci, map[string]any{"type": "media.session.create", "id": "again", "owner": "napplet"})
	if got := rec.wait(t, "media.session.create.result", mediaMaxSessions+2); got["error"] != nil {
		t.Fatalf("after a destroy: %v", got)
	}
}

func TestMediaPlayerFailure(t *testing.T) {
	ci, rec, mh := setupMediaTest(t, "media-fail")
	mh.err = errors.New("exec: mpv: not found")
	post(t, ci, shellCreate("m", "https://1.1.1.1/a.mp3"))
	if got := rec.wait(t, "media.session.create.result", 1); got["error"] != "no media player" {
		t.Fatalf("player failure: %v", got)
	}
}

func TestMediaResetStopsPlayers(t *testing.T) {
	ci, rec, mh := setupMediaTest(t, "media-reset")
	post(t, ci, shellCreate("m", "https://1.1.1.1/a.mp3"))
	id := rec.wait(t, "media.session.create.result", 1)["sessionId"]
	p := mh.player(t, 0)

	if _, err := napRPC(ci, "nap.reset", ""); err != nil {
		t.Fatal(err)
	}
	waitCalls(t, p, []string{"stop"})
	// the old session's id and its player's reports are gone with it
	ready(t, ci, rec, 2)
	post(t, ci, map[string]any{"type": "media.command", "sessionId": id, "action": "play"})
	p.onState(MediaState{Status: "playing"})
	post(t, ci, shellCreate("m2", "https://1.1.1.1/b.mp3"))
	if got := rec.wait(t, "media.session.create.result", 2); got["sessionId"] == id {
		t.Fatalf("id reused across sessions: %v", got)
	}
	if n := len(rec.find("media.state")); n != 0 {
		t.Fatalf("stale state pushed: %v", rec.find("media.state"))
	}
	waitCalls(t, p, []string{"stop"})

	p2 := mh.player(t, 1)
	napRPC(ci, "nap.reset", "")
	WindowClosed(ci.instance)
	waitCalls(t, p2, []string{"stop"})
}

func TestMediaNewSessionTakesThePlayer(t *testing.T) {
	a, recA, mh := setupMediaTest(t, "media-first")
	b, recB := openNapplet(t, "media-second")
	ready(t, b, recB, 1)
	key := RuleKey{Napp: b.napp.ID, Permission: PermMedia}
	setSessionRule(key, Rule{Decision: DecisionAllow})
	t.Cleanup(func() { clearSessionRule(key) })

	post(t, a, shellCreate("m", "https://1.1.1.1/a.mp3"))
	idA := recA.wait(t, "media.session.create.result", 1)["sessionId"]
	recA.wait(t, "media.capabilities", 1)
	pA := mh.player(t, 0)

	post(t, b, shellCreate("m", "https://1.1.1.1/b.mp3"))
	idB := recB.wait(t, "media.session.create.result", 1)["sessionId"]
	if st := recA.wait(t, "media.state", 1); st["sessionId"] != idA || st["status"] != "stopped" {
		t.Fatalf("replaced session's state: %v", st)
	}
	if caps := recA.wait(t, "media.capabilities", 2); caps["sessionId"] != idA || len(toStrings(caps["actions"])) != 0 {
		t.Fatalf("replaced session's capabilities: %v", caps)
	}

	// the replaced session keeps its id but no longer steers anything: the
	// host retired its player, and the launcher doesn't call it either
	pA.onState(MediaState{Status: "playing"})
	post(t, a, map[string]any{"type": "media.command", "sessionId": idA, "action": "play"})
	post(t, a, map[string]any{"type": "media.session.destroy", "sessionId": idA})
	pB := mh.player(t, 1)
	post(t, b, map[string]any{"type": "media.command", "sessionId": idB, "action": "pause"})
	waitCalls(t, pB, []string{"pause"})
	if calls := pA.called(); len(calls) != 0 {
		t.Fatalf("retired player was called: %v", calls)
	}
	if n := len(recA.find("media.state")); n != 1 {
		t.Fatalf("replaced session got %d states", n)
	}

	// a second session in the same window takes it the same way
	post(t, b, shellCreate("m2", "https://1.1.1.1/c.mp3"))
	recB.wait(t, "media.session.create.result", 2)
	if st := recB.wait(t, "media.state", 1); st["sessionId"] != idB || st["status"] != "stopped" {
		t.Fatalf("same-window replacement: %v", st)
	}
}

func TestMediaNappletOwnedRegistry(t *testing.T) {
	ci, rec, mh := setupMediaTest(t, "media-own")
	post(t, ci, map[string]any{"type": "media.session.create", "id": "n", "owner": "napplet", "sessionId": "mine",
		"source": map[string]any{"url": "https://1.1.1.1/a.mp3"}, "capabilities": []string{"play", "bogus", "play"}})
	res := rec.wait(t, "media.session.create.result", 1)
	id, _ := res["sessionId"].(string)
	if res["owner"] != "napplet" || id == "" {
		t.Fatalf("create: %v", res)
	}
	post(t, ci, map[string]any{"type": "media.state", "sessionId": id, "status": "playing", "position": 4})
	post(t, ci, map[string]any{"type": "media.capabilities", "sessionId": id, "actions": []string{"pause", "seek"}})
	post(t, ci, map[string]any{"type": "media.state", "sessionId": id, "status": "exploded"})
	// a round trip through the queue, so the fire-and-forget ones are in
	post(t, ci, map[string]any{"type": "media.session.create", "id": "sync", "owner": "napplet"})
	rec.wait(t, "media.session.create.result", 2)

	ci.nap.mu.Lock()
	ms := ci.nap.media[id]
	ci.nap.mu.Unlock()
	if ms == nil || ms.state["status"] != "playing" || !slices.Equal(ms.actions, []string{"pause", "seek"}) {
		t.Fatalf("session: %+v", ms)
	}
	if len(mh.reqs) != 0 {
		t.Fatal("a napplet-owned source was played")
	}
}

func toStrings(v any) []string {
	switch l := v.(type) {
	case []string:
		return l
	case []any:
		out := []string{}
		for _, x := range l {
			s, _ := x.(string)
			out = append(out, s)
		}
		return out
	}
	return nil
}
