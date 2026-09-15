package main

import (
	"strings"
	"time"
)

// Prompts are the launcher's only synchronous conversation with the user: a
// napp asks for something sensitive (signing, encrypting, saving a file,
// copying to the clipboard, publishing) or fires an action several napps can
// handle, and the rpc blocks here until the Gio window answers.
//
// Only one prompt shows at a time; the rest queue behind it. Nothing blocks
// forever: an unanswered prompt is denied after promptTimeout.

const promptTimeout = 2 * time.Minute

type promptOption struct {
	label  string
	detail string

	// for the action picker: which napp (and, when it's already open, which
	// instance) this option routes to.
	nappID   string
	instance string
}

type prompt struct {
	// title is the one-line question, detail the explanation under it and
	// code an optional monospace block previewing the payload at stake.
	title  string
	detail string
	code   string

	// options is empty for a plain approve/deny prompt.
	options []promptOption

	resp chan promptAnswer
	done bool
}

type promptAnswer struct {
	ok    bool
	index int
}

// enqueuePrompt shows the prompt now, or queues it behind the active one.
func enqueuePrompt(p *prompt) {
	ui.mu.Lock()
	if ui.prompt == nil {
		ui.prompt = p
	} else {
		ui.promptQueue = append(ui.promptQueue, p)
	}
	ui.mu.Unlock()
	if gioWin != nil {
		gioWin.Invalidate()
	}
}

// answerPrompt is called from the frame code when the user clicks: it hands
// the answer to the waiting rpc and promotes the next queued prompt.
func answerPrompt(p *prompt, ok bool, index int) {
	ui.mu.Lock()
	if p.done {
		ui.mu.Unlock()
		return
	}
	p.done = true
	if ui.prompt == p {
		if len(ui.promptQueue) > 0 {
			ui.prompt = ui.promptQueue[0]
			ui.promptQueue = ui.promptQueue[1:]
		} else {
			ui.prompt = nil
		}
	} else {
		for i, q := range ui.promptQueue {
			if q == p {
				ui.promptQueue = append(ui.promptQueue[:i], ui.promptQueue[i+1:]...)
				break
			}
		}
	}
	ui.mu.Unlock()

	select {
	case p.resp <- promptAnswer{ok: ok, index: index}:
	default:
	}
	if gioWin != nil {
		gioWin.Invalidate()
	}
}

func (p *prompt) wait() promptAnswer {
	select {
	case a := <-p.resp:
		return a
	case <-time.After(promptTimeout):
		answerPrompt(p, false, 0)
		return promptAnswer{ok: false}
	}
}

// askApproval blocks until the user approves or denies. It is what makes
// signEvent, nip04/nip44, saveFile, copyText and publish "sensitive".
func askApproval(ci *childInfo, title, detail, code string) bool {
	name := "A napp"
	if ci != nil {
		if ci.napp.Name != "" {
			name = ci.napp.Name
		} else if ci.napp.ID != "" {
			name = ci.napp.ID
		}
	}
	p := &prompt{
		title:  name + " wants to " + title,
		detail: detail,
		code:   code,
		resp:   make(chan promptAnswer, 1),
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
func askActionHandler(callerName, action string, candidates []Napp, open []*childInfo) (promptOption, bool) {
	options := make([]promptOption, 0, len(candidates)+len(open))
	for _, ci := range open {
		label := ci.napp.Name
		if label == "" {
			label = ci.napp.ID
		}
		options = append(options, promptOption{
			label:    label + " (open)",
			detail:   "instance " + ci.instance,
			nappID:   ci.napp.ID,
			instance: ci.instance,
		})
	}
	for _, n := range candidates {
		label := n.Name
		if label == "" {
			label = n.ID
		}
		options = append(options, promptOption{
			label:  label,
			detail: n.Description,
			nappID: n.ID,
		})
	}

	p := &prompt{
		title:   "Open “" + action + "” with…",
		detail:  "Fired by " + callerName + ".",
		options: options,
		resp:    make(chan promptAnswer, 1),
	}
	log.Info().Str("action", action).Int("options", len(options)).Msg("asking the user to pick a handler")
	enqueuePrompt(p)
	answer := p.wait()
	if !answer.ok || answer.index < 0 || answer.index >= len(options) {
		return promptOption{}, false
	}
	return options[answer.index], true
}

// preview trims an arbitrary payload into something a dialog can show.
func preview(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}
