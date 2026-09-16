package backend

import (
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Prompts are the launcher's only synchronous conversation with the user: a
// napp asks for something sensitive (signing, encrypting, saving a file,
// copying to the clipboard, publishing) or fires an action several napps can
// handle, and the rpc blocks here until the GUI answers.
//
// Only one prompt shows at a time; the rest queue behind it. Nothing blocks
// forever: an unanswered prompt is denied after promptTimeout.

const promptTimeout = 2 * time.Minute

// PromptOption is one choice in a picker prompt.
type PromptOption struct {
	Label  string `json:"label"`
	Detail string `json:"detail"`

	// For the action picker: which napp (and, when it's already open, which
	// instance) this option routes to.
	NappID   string `json:"nappId"`
	Instance string `json:"instance"`
}

// Prompt is a question the user has to answer before a napp can continue.
type Prompt struct {
	// ID identifies the prompt when answering it.
	ID int `json:"id"`

	// Title is the one-line question, Detail the explanation under it and
	// Code an optional monospace block previewing the payload at stake.
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Code   string `json:"code"`

	// Options is empty for a plain approve/deny prompt.
	Options []PromptOption `json:"options"`

	// Napp is the napp that asked, for a GUI that wants to show it.
	Napp string `json:"napp"`

	// Instance is the window the prompt belongs over, when it was fired by
	// a running napp: that screen covers itself with the prompt until it is
	// answered. Empty for launcher-generated prompts (the GUI shows those
	// wherever it likes).
	Instance string `json:"instance"`

	resp chan promptAnswer
	done bool
}

type promptAnswer struct {
	ok    bool
	index int
}

var (
	promptMu     sync.Mutex
	promptActive *Prompt
	promptQueue  []*Prompt
	promptSerial atomic.Int64
)

// CurrentPrompt is the prompt the user should be answering, or nil.
func CurrentPrompt() *Prompt {
	promptMu.Lock()
	defer promptMu.Unlock()
	return promptActive
}

// PendingPrompts is how many are waiting behind the current one.
func PendingPrompts() int {
	promptMu.Lock()
	defer promptMu.Unlock()
	return len(promptQueue)
}

// enqueuePrompt shows the prompt now, or queues it behind the active one.
func enqueuePrompt(p *Prompt) {
	promptMu.Lock()
	if promptActive == nil {
		promptActive = p
	} else {
		promptQueue = append(promptQueue, p)
	}
	promptMu.Unlock()
	if host != nil {
		host.PromptsChanged()
	}
	syncPromptOverlays()
}

// AnswerPrompt is what a GUI calls when the user clicks: it hands the answer
// to the waiting rpc and promotes the next queued prompt. index is the chosen
// option for a picker prompt and ignored otherwise.
func AnswerPrompt(id int, ok bool, index int) {
	promptMu.Lock()
	var p *Prompt
	if promptActive != nil && promptActive.ID == id {
		p = promptActive
	} else {
		for _, q := range promptQueue {
			if q.ID == id {
				p = q
				break
			}
		}
	}
	if p == nil || p.done {
		promptMu.Unlock()
		return
	}
	p.done = true

	if promptActive == p {
		if len(promptQueue) > 0 {
			promptActive = promptQueue[0]
			promptQueue = promptQueue[1:]
		} else {
			promptActive = nil
		}
	} else {
		for i, q := range promptQueue {
			if q == p {
				promptQueue = append(promptQueue[:i], promptQueue[i+1:]...)
				break
			}
		}
	}
	promptMu.Unlock()

	select {
	case p.resp <- promptAnswer{ok: ok, index: index}:
	default:
	}
	if host != nil {
		host.PromptsChanged()
	}
	syncPromptOverlays()
}

func (p *Prompt) wait() promptAnswer {
	select {
	case a := <-p.resp:
		return a
	case <-time.After(promptTimeout):
		AnswerPrompt(p.ID, false, 0)
		return promptAnswer{ok: false}
	}
}

func newPrompt(napp, title, detail, code string, options []PromptOption) *Prompt {
	return &Prompt{
		ID:      int(promptSerial.Add(1)),
		Napp:    napp,
		Title:   title,
		Detail:  detail,
		Code:    code,
		Options: options,
		resp:    make(chan promptAnswer, 1),
	}
}

// askApproval blocks until the user approves or denies. It is what makes
// signEvent, nip04/nip44, saveFile, copyText and publish "sensitive".
func askApproval(ci *Instance, title, detail, code string) bool {
	name := "A napp"
	if ci != nil && ci.napp.Label() != "" {
		name = ci.napp.Label()
	}
	p := newPrompt(name, name+" wants to "+title, detail, code, nil)
	if ci != nil {
		p.Instance = ci.instance
	}
	log.Info().Str("napp", name).Str("ask", title).Msg("asking the user for approval")
	enqueuePrompt(p)
	answer := p.wait()
	log.Info().Str("napp", name).Str("ask", title).Bool("granted", answer.ok).
		Msg("approval answered")
	return answer.ok
}

// askActionHandler asks which napp should handle an action when more than one
// can. Open windows come first — routing into one keeps the user's state.
func askActionHandler(caller *Instance, action string, candidates []Napp, open []*Instance) (PromptOption, bool) {
	callerName := "launcher"
	if caller != nil {
		callerName = caller.napp.Label()
	}
	options := make([]PromptOption, 0, len(candidates)+len(open))
	for _, ci := range open {
		options = append(options, PromptOption{
			Label:    ci.napp.Label() + " (open)",
			Detail:   "instance " + ci.instance,
			NappID:   ci.napp.ID,
			Instance: ci.instance,
		})
	}
	for _, n := range candidates {
		options = append(options, PromptOption{
			Label:  n.Label(),
			Detail: n.Description,
			NappID: n.ID,
		})
	}

	p := newPrompt(callerName, "Open “"+action+"” with…", "Fired by "+callerName+".", "", options)
	if caller != nil {
		p.Instance = caller.instance
	}
	log.Info().Str("action", action).Int("options", len(options)).Msg("asking the user to pick a handler")
	enqueuePrompt(p)
	answer := p.wait()
	if !answer.ok || answer.index < 0 || answer.index >= len(options) {
		return PromptOption{}, false
	}
	return options[answer.index], true
}

// ─── showing prompts over the window that asked ──────────────────
//
// A prompt fired by a running napp belongs over that napp's own screen: the
// covered window keeps it up until the answer comes back, and the launcher
// chrome only shows prompts it generated itself.

// promptOverlays tracks the instances currently showing a prompt overlay, so
// each screen gets exactly one and stale ones come down when the prompt is
// answered (or times out).
var (
	promptOverlayMu sync.Mutex
	promptOverlays  = map[string]bool{}
)

// syncPromptOverlays makes what each napp window shows match the prompt
// state: one overlay per window with a pending prompt, none elsewhere.
// Safe to call after any change to the prompt state.
func syncPromptOverlays() {
	promptMu.Lock()
	targets := make(map[string]*Prompt)
	if promptActive != nil && promptActive.Instance != "" {
		targets[promptActive.Instance] = promptActive
	}
	for _, q := range promptQueue {
		if q.Instance != "" {
			if _, ok := targets[q.Instance]; !ok {
				targets[q.Instance] = q
			}
		}
	}
	promptMu.Unlock()

	var hide []string
	var showList []*Prompt

	promptOverlayMu.Lock()
	for inst := range promptOverlays {
		if _, ok := targets[inst]; !ok {
			hide = append(hide, inst)
			delete(promptOverlays, inst)
		}
	}
	for inst, p := range targets {
		if !promptOverlays[inst] {
			showList = append(showList, p)
			promptOverlays[inst] = true
		}
	}
	promptOverlayMu.Unlock()

	for _, inst := range hide {
		if ci := lookupInstance(inst); ci != nil {
			ci.send(WireMsg{T: "prompt"})
		}
	}
	for _, p := range showList {
		raw, err := json.Marshal(p)
		if err != nil {
			continue
		}
		if ci := lookupInstance(p.Instance); ci != nil {
			ci.send(WireMsg{T: "prompt", Params: string(raw)})
		} else {
			// the window is already gone: never mark it as covered
			promptOverlayMu.Lock()
			delete(promptOverlays, p.Instance)
			promptOverlayMu.Unlock()
		}
	}
}

// handlePromptAnswer is what a shell sends up when its overlay was clicked.
func (ci *Instance) handlePromptAnswer(m WireMsg) {
	var a struct {
		OK    bool `json:"ok"`
		Index int  `json:"index"`
	}
	_ = json.Unmarshal([]byte(m.Params), &a)
	AnswerPrompt(m.ID, a.OK, a.Index)
}

// preview trims an arbitrary payload into something a dialog can show.
func preview(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}
