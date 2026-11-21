package backend

import (
	"sync"
	"testing"
	"time"
)

type recordingTransport struct {
	mu   sync.Mutex
	msgs []WireMsg
}

func (r *recordingTransport) Send(m WireMsg) {
	r.mu.Lock()
	r.msgs = append(r.msgs, m)
	r.mu.Unlock()
}

func (r *recordingTransport) Close() {}

func (r *recordingTransport) snapshot() []WireMsg {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]WireMsg(nil), r.msgs...)
}

func registerTestInstance(t *testing.T, id string, wasm, picker bool) (*Instance, *recordingTransport) {
	t.Helper()
	tr := &recordingTransport{}
	ci := &Instance{
		instance:  id,
		picker:    picker,
		transport: tr,
		gone:      make(chan struct{}),
	}
	if wasm {
		ci.napp.Kind = KindWasm
	}
	registerInstance(ci)
	t.Cleanup(func() {
		instancesMu.Lock()
		for i, c := range instances {
			if c == ci {
				instances = append(instances[:i], instances[i+1:]...)
				break
			}
		}
		instancesMu.Unlock()
		promptOverlays.Delete(id)
	})
	return ci, tr
}

// settlePrompt takes a test's prompt out of the queue for good, so one
// test's question never reaches another's answer.
func settlePrompt(t *testing.T, p *Prompt) {
	t.Helper()
	promptMu.Lock()
	p.done = true
	promptActive = nil
	promptQueue = nil
	promptMu.Unlock()
}

// answered waits out the answer a prompt ended up with.
func answered(t *testing.T, p *Prompt) Answer {
	t.Helper()
	select {
	case a := <-p.resp:
		return a
	case <-time.After(time.Second):
		t.Fatal("the prompt got no answer")
		return Answer{}
	}
}

// unanswered says the prompt is still waiting, and leaves it that way.
func unanswered(t *testing.T, p *Prompt) {
	t.Helper()
	select {
	case a := <-p.resp:
		t.Fatalf("the prompt was answered %+v when nothing should have reached it", a)
	default:
	}
}

func TestAnswerPromptWantsTheToken(t *testing.T) {
	p := newPrompt("notes", "notes wants to sign", PromptBody{}, nil)
	p.Instance = "abcd1234"
	p.key = RuleKey{Napp: "notes", Permission: PermSign}
	enqueuePrompt(p)
	defer settlePrompt(t, p)

	// a forged answer — a guessed id, no token — is a plain no, and it is
	// final: nothing reaches the caller that looks like an approval, the
	// waiting rpc is let go rather than left hanging, and nothing is
	// remembered for the asking napp (a forged denial must not file a
	// rule either).
	if AnswerPrompt(p.ID, "", Answer{OK: true, Scope: ScopeAlways}) {
		t.Fatal("an answer with no token was accepted")
	}
	if a := answered(t, p); a.OK || a.Scope != "" {
		t.Fatalf("a forged answer produced %+v, want a bare denial", a)
	}
	if rule, ok := lookupRule(RuleKey{Napp: "notes", Permission: PermSign}); ok {
		t.Fatalf("a forged answer filed a rule anyway: %+v", rule)
	}

	// one denial caps probing: the prompt is settled, a second guess —
	// even with the right token — finds nothing to answer
	if AnswerPrompt(p.ID, p.AnswerToken, Answer{OK: true, Scope: ScopeOnce}) {
		t.Fatal("the prompt outlived a forged answer")
	}
}

func TestShellAnswerIsScopedToItsWindow(t *testing.T) {
	_, wasmTr := registerTestInstance(t, "abcd1234", true, false)
	registerTestInstance(t, "deadbeef", true, false)

	p := newPrompt("notes", "notes wants to sign", PromptBody{}, nil)
	p.Instance = "abcd1234"
	enqueuePrompt(p)
	defer settlePrompt(t, p)

	// a trusted wasm shell receives the prompt overlay over its wire channel
	gotPrompt := false
	for _, m := range wasmTr.snapshot() {
		if m.T == "prompt" && m.Params != "" {
			gotPrompt = true
		}
	}
	if !gotPrompt {
		t.Fatal("wasm shell did not receive prompt wire message")
	}

	// another window's shell cannot answer this one's question, token and all
	if AnswerPromptFromShell(p.ID, p.AnswerToken, "deadbeef", Answer{OK: true, Scope: ScopeAlways}) {
		t.Fatal("an answer from another window was accepted")
	}
	unanswered(t, p)

	// its own wasm shell, with the token, can
	if !AnswerPromptFromShell(p.ID, p.AnswerToken, "abcd1234", Answer{OK: true, Scope: ScopeOnce}) {
		t.Fatal("the asking window's own answer was not accepted")
	}
	if a := answered(t, p); !a.OK {
		t.Fatalf("the answer came back wrong: %+v", a)
	}
}

func TestWebviewShellCannotAnswerOrReceivePrompts(t *testing.T) {
	_, webTr := registerTestInstance(t, "web12345", false, false)

	p := newPrompt("evil", "evil wants to sign", PromptBody{}, nil)
	p.Instance = "web12345"
	enqueuePrompt(p)
	defer settlePrompt(t, p)

	// a webview shell must never receive prompt payloads or tokens over the wire
	for _, m := range webTr.snapshot() {
		if m.T == "prompt" {
			t.Fatalf("webview shell received prompt wire message: %+v", m)
		}
	}

	// instead, the trusted launcher window renders the prompt
	if got := CurrentLauncherPrompt(); got == nil || got.ID != p.ID {
		t.Fatalf("CurrentLauncherPrompt() = %v, want prompt %d", got, p.ID)
	}

	// even if a webview shell somehow possessed the token, wire answers are refused
	if AnswerPromptFromShell(p.ID, p.AnswerToken, "web12345", Answer{OK: true, Scope: ScopeAlways}) {
		t.Fatal("webview shell was allowed to answer its own prompt over the wire")
	}
	unanswered(t, p)

	// the trusted launcher UI can still answer it directly
	if !AnswerPrompt(p.ID, p.AnswerToken, Answer{OK: true, Scope: ScopeOnce}) {
		t.Fatal("launcher answer for webview prompt was rejected")
	}
	if a := answered(t, p); !a.OK {
		t.Fatalf("expected approval from launcher, got %+v", a)
	}
}

func TestShellCannotAnswerTheLauncher(t *testing.T) {
	registerTestInstance(t, "abcd1234", true, false)

	// a launcher question has no window over it, so no shell gets to
	// answer it, whatever token it shows up with
	p := newPrompt("launcher", "Apply the configuration?", PromptBody{}, nil)
	enqueuePrompt(p)
	defer settlePrompt(t, p)

	if AnswerPromptFromShell(p.ID, p.AnswerToken, "abcd1234", Answer{OK: true, Scope: ScopeAlways}) {
		t.Fatal("a napp shell answered the launcher's question")
	}
	unanswered(t, p)
}

func TestEveryPromptHasItsOwnToken(t *testing.T) {
	a := newPrompt("one", "one wants to sign", PromptBody{}, nil)
	b := newPrompt("two", "two wants to sign", PromptBody{}, nil)
	if a.AnswerToken == "" || a.AnswerToken == b.AnswerToken {
		t.Fatal("prompts share answer tokens")
	}
}
