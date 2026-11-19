package backend

import (
	"testing"
	"time"
)

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
	enqueuePrompt(p)
	defer settlePrompt(t, p)

	// a forged answer — a guessed id, no token — settles nothing
	if AnswerPrompt(p.ID, "", Answer{OK: true, Scope: ScopeAlways}) {
		t.Fatal("an answer with no token was accepted")
	}
	unanswered(t, p)

	if AnswerPrompt(p.ID, "0000000000000000000000000000000", Answer{OK: true, Scope: ScopeAlways}) {
		t.Fatal("an answer with a wrong token was accepted")
	}
	unanswered(t, p)

	// the prompt's own token goes through
	if !AnswerPrompt(p.ID, p.AnswerToken, Answer{OK: true, Scope: ScopeOnce}) {
		t.Fatal("the prompt's own token was not accepted")
	}
	if a := answered(t, p); !a.OK || a.Scope != ScopeOnce {
		t.Fatalf("the answer came back wrong: %+v", a)
	}
}

func TestShellAnswerIsScopedToItsWindow(t *testing.T) {
	p := newPrompt("notes", "notes wants to sign", PromptBody{}, nil)
	p.Instance = "abcd1234"
	enqueuePrompt(p)
	defer settlePrompt(t, p)

	// another window's shell cannot answer this one's question, token and all
	if AnswerPromptFromShell(p.ID, p.AnswerToken, "deadbeef", Answer{OK: true, Scope: ScopeAlways}) {
		t.Fatal("an answer from another window was accepted")
	}
	unanswered(t, p)

	// its own shell, with the token, can
	if !AnswerPromptFromShell(p.ID, p.AnswerToken, "abcd1234", Answer{OK: true, Scope: ScopeOnce}) {
		t.Fatal("the asking window's own answer was not accepted")
	}
	if a := answered(t, p); !a.OK {
		t.Fatalf("the answer came back wrong: %+v", a)
	}
}

func TestShellCannotAnswerTheLauncher(t *testing.T) {
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
