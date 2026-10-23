package backend

import (
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/puzpuzpuz/xsync/v3"
)

// Prompts are the launcher's only synchronous conversation with the user: a
// napp asks for something sensitive (signing, encrypting, saving a file,
// copying to the clipboard, publishing) or fires an action several napps can
// handle, and the rpc blocks here until the GUI answers.
//
// Only one prompt shows at a time; the rest queue behind it. Nothing blocks
// forever: an unanswered prompt is denied after promptTimeout.
//
// A sensitive question is only asked when no rule has an answer for it (see
// permissions.go), and the answer can come back with a scope: for this prompt
// only, for this session, or always. Anything wider than that gets filed away
// by permissions.go and settles the next question of the same kind without a
// prompt.

const promptTimeout = 2 * time.Minute

// Scope is how long the user's answer to a prompt holds.
type Scope string

const (
	// ScopeOnce is this prompt only: nothing is remembered.
	ScopeOnce Scope = "once"

	// ScopeSession holds until the launcher quits, and is never written down.
	ScopeSession Scope = "session"

	// ScopeAlways holds across restarts, until the user takes it back.
	ScopeAlways Scope = "always"
)

// Answer is what the user chose on a prompt: yes or no, which option of a
// picker (ignored otherwise), how long that answer holds, and which of the
// prompt's items the user unticked (the relays a publish is not to go to).
// Skip only ever holds for this one prompt: a remembered answer is the yes
// or the no alone.
type Answer struct {
	OK    bool  `json:"ok"`
	Index int   `json:"index"`
	Scope Scope `json:"scope"`
	Skip  []int `json:"skip,omitempty"`
}

// kept is the items the user left ticked.
func (a Answer) kept(items []string) []string {
	out := make([]string, 0, len(items))
	for i, it := range items {
		if !slices.Contains(a.Skip, i) {
			out = append(out, it)
		}
	}
	return out
}

// PromptOption is one choice in a picker prompt.
type PromptOption struct {
	Label  string `json:"label"`
	Detail string `json:"detail"`
	Dev    bool   `json:"dev,omitempty"`

	// For the action picker: which napp (and, when it's already open, which
	// instance) this option routes to.
	NappID   string `json:"nappId"`
	Instance string `json:"instance"`

	// Suggested says the user has been choosing this napp for this action
	// before, and Uses is how many times, so a UI can put its habitual
	// handlers at the top in a color of their own. Set by the picker (see
	// usage.go); a UI never has to work it out.
	Suggested bool `json:"suggested,omitempty"`
	Uses      int  `json:"uses,omitempty"`

	// Name is the napp's own name, with no window in it, and Icon its
	// icon's bytes: what a picker window draws its grid of napps with (see
	// picker.go).
	Name string `json:"name,omitempty"`
	Icon []byte `json:"icon,omitempty"`
}

// PromptBody is what a prompt shows under its title, in this order.
type PromptBody struct {
	// Detail is the explanation under the title.
	Detail string `json:"detail"`

	// Warning is something that could hurt, to be shown loud: a follow list
	// about to lose most of itself.
	Warning string `json:"warning,omitempty"`

	// Items are what the detail ends by pointing at, a row each: the relays
	// an event is about to be published to. Each comes with a box, ticked to
	// start with, and the answer says which ones the user unticked.
	Items []string `json:"items,omitempty"`

	// Code is an optional monospace block with the payload at stake. With
	// a Summary it starts folded under that one line ("kind-1 · 3 tags").
	Code    string `json:"code"`
	Summary string `json:"summary,omitempty"`
}

// Prompt is a question the user has to answer before a napp can continue.
type Prompt struct {
	// ID identifies the prompt when answering it.
	ID int `json:"id"`

	// Title is the one-line question; the body says what is at stake.
	Title string `json:"title"`
	PromptBody

	// Options is empty for a plain approve/deny prompt.
	Options []PromptOption `json:"options"`

	// Remember says the answer can stick, so a GUI offers the scopes
	// alongside the yes and the no. False for a picker, whose answer is
	// only ever about this one dispatch.
	Remember bool `json:"remember"`

	// Napp is the napp that asked, for a GUI that wants to show it.
	Napp string `json:"napp"`

	// Instance is the window the prompt belongs over, when it was fired by
	// a running napp: that screen covers itself with the prompt until it is
	// answered. Empty for launcher-generated prompts (the GUI shows those
	// wherever it likes).
	Instance string `json:"instance"`

	// key is what an answer wider than this prompt is remembered under.
	// Zero for a picker.
	key  RuleKey
	resp chan Answer
	done bool
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

// AnswerPrompt is what a GUI calls when the user clicks: it remembers the
// answer when the user asked for it to stick, hands it to the waiting rpc and
// promotes the next queued prompt.
func AnswerPrompt(id int, ans Answer) {
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

	// before the rpc goes on, so the rule is already in place by the time
	// the next question of the same kind comes round
	if p.key.valid() {
		remember(p.key, Rule{Decision: decisionOf(ans.OK)}, ans.Scope)
	}

	select {
	case p.resp <- ans:
	default:
	}
	if host != nil {
		host.PromptsChanged()
	}
	syncPromptOverlays()
}

func (p *Prompt) wait() Answer {
	return p.waitOr(nil, nil)
}

// waitOr is wait, also calling then (once) if more is closed while the
// prompt is still up, to change the prompt meanwhile.
func (p *Prompt) waitOr(more chan struct{}, then func()) Answer {
	timeout := time.NewTimer(promptTimeout)
	defer timeout.Stop()
	for {
		select {
		case a := <-p.resp:
			return a
		case <-more:
			more = nil
			then()
		case <-timeout.C:
			// unanswered is a plain no: nothing is remembered for it
			AnswerPrompt(p.ID, Answer{})
			return Answer{}
		}
	}
}

// updatePrompt changes a prompt that is still waiting and shows it again.
// The prompt is replaced by an edited copy rather than changed in place, as
// GUIs read the one they were handed without a lock.
func updatePrompt(id int, edit func(*Prompt)) {
	promptMu.Lock()
	var slot **Prompt
	if promptActive != nil && promptActive.ID == id {
		slot = &promptActive
	} else {
		for i := range promptQueue {
			if promptQueue[i].ID == id {
				slot = &promptQueue[i]
				break
			}
		}
	}
	if slot == nil || (*slot).done {
		promptMu.Unlock()
		return
	}
	edited := **slot
	edit(&edited)
	*slot = &edited
	instance := edited.Instance
	promptMu.Unlock()

	// same id, so the overlay has to be told it changed
	if instance != "" {
		promptOverlays.Delete(instance)
	}
	if host != nil {
		host.PromptsChanged()
	}
	syncPromptOverlays()
}

func newPrompt(napp, title string, body PromptBody, options []PromptOption) *Prompt {
	return &Prompt{
		ID:         int(promptSerial.Add(1)),
		Napp:       napp,
		Title:      title,
		PromptBody: body,
		Options:    options,
		resp:       make(chan Answer, 1),
	}
}

// askApproval answers whether a napp may do something, for perm: a rule that
// already covers it decides right away, and only a question nothing has an
// answer to becomes a prompt. It is what makes signEvent, nip04/nip44,
// saveFile, copyText, napp.link and publish "sensitive".
func askApproval(ci *Instance, perm Permission, title string, body PromptBody) bool {
	return askApprovalFor(ci, perm, title, func() PromptBody { return body }).OK
}

// askApprovalFor is askApproval with a body that is only worked out when
// there is a prompt to show it on (no rule answered for it), answering with
// the whole Answer: a rule's has nothing unticked.
func askApprovalFor(ci *Instance, perm Permission, title string, body func() PromptBody) Answer {
	name := "A napp"
	nappID := ""
	if ci != nil {
		nappID = ci.napp.ID
		if ci.napp.Label() != "" {
			name = ci.napp.Label()
		}
	}

	// a napp with no id asked the launcher itself, and the rules file those
	// under the empty napp, same as an installed configuration that is about
	// the launcher rather than about one napp.
	key := RuleKey{Napp: nappID, Permission: perm}
	if rule, ok := lookupRule(key); ok {
		log.Info().Str("napp", name).Str("ask", title).
			Str("rule", string(rule.Decision)).Msg("approval answered by the rules")
		return Answer{OK: rule.Decision.granted()}
	}

	p := newPrompt(name, name+" wants to "+title, body(), nil)
	p.key = key
	p.Remember = true
	if ci != nil {
		p.Instance = ci.instance
	}
	log.Info().Str("napp", name).Str("ask", title).Msg("asking the user for approval")
	enqueuePrompt(p)
	answer := p.wait()
	log.Info().Str("napp", name).Str("ask", title).Bool("granted", answer.OK).
		Str("scope", string(answer.Scope)).Msg("approval answered")
	return answer
}

// askActionHandler asks which napp should handle an action when more than one
// can. Open windows come first — routing into one keeps the user's state.
//
// The question is asked in a picker window of its own, and that window comes
// back with the answer: whoever routes the action either opens the napp that
// was picked in it or closes it. A platform with no picker windows has no way
// to ask, so the action goes nowhere.
//
// For a raw "view", viewing is the search for the event's kind: the picker
// starts with the "view" napps and, when the kind turns up, also lists the
// napps viewing that kind, after the options already shown so that what the
// user may be about to click stays where it is.
func askActionHandler(caller *Instance, action string, payload json.RawMessage, candidates []Napp, open []*Instance, viewing *viewLookup) (PromptOption, *Instance, bool) {
	callerName := "launcher"
	callerID := ""
	if caller != nil {
		callerName = caller.napp.Label()
		callerID = caller.napp.ID
	}

	picker := openPicker()
	if picker == nil {
		return PromptOption{}, nil, false
	}

	options := handlerOptions(candidates, open)

	// the napps the user keeps sending this action to come first, so the
	// habitual answer is the one under their finger; everything else is left
	// in the order it arrived in
	sortHandlerOptions(options, callerID, action)

	code := ""
	if len(payload) != 0 && string(payload) != "null" {
		code = preview(string(payload), 500)
	}

	p := newPrompt(callerName, "Open “"+action+"” with…", PromptBody{Code: code}, options)
	p.Instance = picker.instance
	// closing the picker is saying no
	go func() {
		<-picker.gone
		AnswerPrompt(p.ID, Answer{})
	}()
	log.Info().Str("action", action).Int("options", len(options)).
		Msg("asking the user to pick a handler")
	enqueuePrompt(p)

	var more chan struct{}
	if viewing != nil {
		more = viewing.done
	}
	answer := p.waitOr(more, func() {
		r := viewing.res
		if r == nil {
			return
		}
		added := handlerOptions(r.candidates, r.open)
		added = slices.DeleteFunc(added, func(o PromptOption) bool {
			return slices.ContainsFunc(options, func(have PromptOption) bool {
				return have.NappID == o.NappID && have.Instance == o.Instance
			})
		})
		sortHandlerOptions(added, callerID, action)
		options = append(options, added...)
		log.Info().Str("action", action).Str("as", r.name).Int("added", len(added)).
			Msg("the picker takes in the napps viewing the event's kind")
		all := slices.Clone(options)
		updatePrompt(p.ID, func(p *Prompt) {
			p.Options = all
			p.Code = preview(string(r.payload), 500)
		})
	})
	if !answer.OK || answer.Index < 0 || answer.Index >= len(options) {
		return PromptOption{}, picker, false
	}
	return options[answer.Index], picker, true
}

// handlerOptions are the picker's options for these handlers: open windows
// first — routing into one keeps the user's state — and dev napps before the
// rest in each.
func handlerOptions(candidates []Napp, open []*Instance) []PromptOption {
	options := make([]PromptOption, 0, len(candidates)+len(open))
	for _, dev := range []bool{true, false} {
		for _, ci := range open {
			if strings.HasPrefix(ci.napp.ID, "dev~") != dev {
				continue
			}
			options = append(options, PromptOption{
				Label:    ci.napp.Label() + " - window " + ci.instance,
				NappID:   ci.napp.ID,
				Instance: ci.instance,
				Dev:      dev,
				Name:     ci.napp.Label(),
				Icon:     pickerIcon(ci.napp),
			})
		}
		for _, n := range candidates {
			if strings.HasPrefix(n.ID, "dev~") != dev {
				continue
			}
			options = append(options, PromptOption{
				Label:  n.Label(),
				Detail: n.ID,
				NappID: n.ID,
				Dev:    dev,
				Name:   n.Label(),
				Icon:   pickerIcon(n),
			})
		}
	}
	return options
}

// ─── showing prompts over the window that asked ──────────────────
//
// A prompt fired by a running napp belongs over that napp's own screen: the
// covered window keeps it up until the answer comes back, and the launcher
// chrome only shows prompts it generated itself.

// promptOverlays tracks the instances currently showing a prompt overlay, and
// which prompt each shows, so each screen gets exactly one, stale ones come
// down when the prompt is answered (or times out) and the next prompt queued
// for the same window replaces the one just answered.
var promptOverlays = xsync.NewMapOf[string, int]()

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

	for inst := range promptOverlays.Range {
		if _, ok := targets[inst]; !ok {
			hide = append(hide, inst)
			promptOverlays.Delete(inst)
		}
	}
	for inst, p := range targets {
		if shown, ok := promptOverlays.Load(inst); !ok || shown != p.ID {
			promptOverlays.Store(inst, p.ID)
			showList = append(showList, p)
		}
	}

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
			promptOverlays.Delete(p.Instance)
		}
	}
}

// handlePromptAnswer is what a shell sends up when its overlay was clicked.
func (ci *Instance) handlePromptAnswer(m WireMsg) {
	var a Answer
	_ = json.Unmarshal([]byte(m.Params), &a)
	AnswerPrompt(m.ID, a)
}

// preview trims an arbitrary payload into something a dialog can show.
func preview(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}
