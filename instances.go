package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/sdk"
)

var instanceSerial atomic.Int64

// ─── registry ────────────────────────────────────────────────────

// registerInstance puts a freshly started child in the registry. mu guards
// both `children` and the instance lookup.
func registerInstance(ci *childInfo) {
	mu.Lock()
	children = append(children, ci)
	mu.Unlock()
}

func lookupInstance(instance string) *childInfo {
	mu.Lock()
	defer mu.Unlock()
	for _, ci := range children {
		if ci.instance == instance {
			return ci
		}
	}
	return nil
}

// runningForNapp returns the open instances of a given napp.
func runningForNapp(nappID string) []*childInfo {
	mu.Lock()
	defer mu.Unlock()
	out := make([]*childInfo, 0, 1)
	for _, ci := range children {
		if ci.napp.ID == nappID {
			out = append(out, ci)
		}
	}
	return out
}

// nextInstanceID is the napp's own id when it is a singleton (stable across
// launches, as behavior.md promises) and a serial otherwise.
func nextInstanceID(n Napp) string {
	if n.Singleton {
		return n.ID
	}
	return strconv.FormatInt(instanceSerial.Add(1), 10)
}

// ─── registered actions ──────────────────────────────────────────

func (ci *childInfo) registerAction(pattern string, idx int) {
	ci.actionsMu.Lock()
	if ci.actions == nil {
		ci.actions = make(map[string]int)
	}
	ci.actions[pattern] = idx
	if ci.changed != nil {
		close(ci.changed)
	}
	ci.changed = make(chan struct{})
	ci.actionsMu.Unlock()
	log.Debug().Str("instance", ci.instance).Str("pattern", pattern).Int("idx", idx).
		Msg("napp registered action")
}

// handlerFor returns the handler index registered for an action name, using
// the one special case of the pattern language: "view" matches every
// "view:<number>". The second result is false when nothing matches (yet).
func (ci *childInfo) handlerFor(name string) (int, bool) {
	ci.actionsMu.Lock()
	defer ci.actionsMu.Unlock()
	if idx, ok := ci.actions[name]; ok {
		return idx, true
	}
	if strings.HasPrefix(name, "view:") {
		if idx, ok := ci.actions["view"]; ok {
			return idx, true
		}
	}
	return 0, false
}

// waitForHandler blocks until the napp has registered a handler for this
// action name (it may still be booting) or the context is done.
func (ci *childInfo) waitForHandler(ctx context.Context, name string) (int, bool) {
	for {
		ci.actionsMu.Lock()
		if ci.changed == nil {
			ci.changed = make(chan struct{})
		}
		changed := ci.changed
		ci.actionsMu.Unlock()

		if idx, ok := ci.handlerFor(name); ok {
			return idx, true
		}

		select {
		case <-changed:
		case <-ci.gone:
			return 0, false
		case <-ctx.Done():
			return 0, false
		}
	}
}

// ─── launching ───────────────────────────────────────────────────

// launchNapp opens a napp window without waiting for it (used by the UI).
func launchNapp(napp Napp) {
	go func() {
		if _, err := launchNappSync(context.Background(), napp); err != nil {
			log.Error().Err(err).Str("napp", napp.ID).Msg("launch failed")
			setFetchErr("launch failed: " + err.Error())
		}
	}()
}

// launchNappSync opens a napp window and returns its instance. A singleton
// that is already open is surfaced instead of opened again.
func launchNappSync(ctx context.Context, napp Napp) (*childInfo, error) {
	id := napp.ID
	if id == "" {
		return nil, errors.New("napp has no id")
	}

	if napp.Singleton {
		if ci := lookupInstance(id); ci != nil {
			log.Info().Str("napp", id).Msg("singleton already open, reusing it")
			return ci, nil
		}
	}

	appDir := nappBaseDir(id)
	if err := os.MkdirAll(appDir, 0755); err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(appDir, "index.html")); err != nil {
		return nil, fmt.Errorf("napp %s is not installed", id)
	}

	log.Info().Str("napp", id).Str("name", napp.Name).Msg("launch napp")
	reply := make(chan launchResult, 1)
	select {
	case openReqCh <- openReq{napp: napp, dir: appDir, reply: reply}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case res := <-reply:
		return res.ci, res.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// ─── action dispatch ─────────────────────────────────────────────

type actionOptions struct {
	Instance  string `json:"instance"`
	Auxiliary bool   `json:"auxiliary"`
}

// runNappAction is the whole of napp.action(): find (or open) a window that
// handles `name` and hand it the payload, answering with whatever the
// handler returned.
func runNappAction(
	ctx context.Context,
	caller *childInfo,
	name string,
	payload json.RawMessage,
	opts actionOptions,
) (any, error) {
	if name == "" {
		return nil, errors.New("napp.action: action name is required")
	}

	callerName := "launcher"
	if caller != nil {
		callerName = caller.napp.Name
		if callerName == "" {
			callerName = caller.napp.ID
		}
	}

	req := &actionRequest{name: name, payload: payload}

	// an explicit instance skips every choice: route it straight there
	if opts.Instance != "" {
		ci := lookupInstance(opts.Instance)
		if ci == nil {
			return nil, fmt.Errorf("no open instance %q", opts.Instance)
		}
		log.Info().Str("from", callerName).Str("action", name).
			Str("instance", ci.instance).Msg("dispatching action to instance")
		return dispatchToInstance(ctx, ci, req)
	}

	candidates, open := findHandlersForAction(name)
	if len(candidates) == 0 && len(open) == 0 {
		return nil, fmt.Errorf("no installed napp handles %q", name)
	}

	// exactly one possibility: no need to bother the user
	if len(candidates)+len(open) == 1 {
		if len(open) == 1 {
			log.Info().Str("from", callerName).Str("action", name).
				Str("instance", open[0].instance).Msg("dispatching action")
			return dispatchToInstance(ctx, open[0], req)
		}
		log.Info().Str("from", callerName).Str("action", name).
			Str("napp", candidates[0].ID).Msg("launching napp for action")
		ci, err := launchNappSync(ctx, candidates[0])
		if err != nil {
			return nil, err
		}
		return dispatchToInstance(ctx, ci, req)
	}

	choice, ok := askActionHandler(callerName, name, candidates, open)
	if !ok {
		return nil, errors.New("action handler selection cancelled")
	}
	if choice.instance != "" {
		ci := lookupInstance(choice.instance)
		if ci == nil {
			return nil, fmt.Errorf("instance %q is gone", choice.instance)
		}
		return dispatchToInstance(ctx, ci, req)
	}
	for _, n := range candidates {
		if n.ID == choice.nappID {
			ci, err := launchNappSync(ctx, n)
			if err != nil {
				return nil, err
			}
			return dispatchToInstance(ctx, ci, req)
		}
	}
	return nil, fmt.Errorf("napp %q is gone", choice.nappID)
}

// findHandlersForAction lists the installed napps declaring this action plus
// the already-open windows among them. "view" declared by a napp matches
// every "view:<number>" dispatch.
func findHandlersForAction(name string) ([]Napp, []*childInfo) {
	stateMu.Lock()
	all := make([]Napp, 0, len(state.InstalledNapps))
	for _, n := range state.InstalledNapps {
		all = append(all, n)
	}
	stateMu.Unlock()

	candidates := make([]Napp, 0, 2)
	for _, n := range all {
		for _, a := range n.Actions {
			if a == name || (a == "view" && strings.HasPrefix(name, "view:")) {
				candidates = append(candidates, n)
				break
			}
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].Name < candidates[j].Name })

	open := make([]*childInfo, 0, 2)
	for _, n := range candidates {
		open = append(open, runningForNapp(n.ID)...)
	}
	return candidates, open
}

// dispatchToInstance sends an action to an open window and waits for the
// handler's result. view:<kind> payloads are resolved to full events first,
// as napps registering a specific kind are promised a resolved event.
func dispatchToInstance(ctx context.Context, ci *childInfo, req *actionRequest) (any, error) {
	payload := req.payload
	if strings.HasPrefix(req.name, "view:") {
		if resolved, ok := resolveViewPayload(ctx, payload); ok {
			payload = resolved
		} else {
			return nil, fmt.Errorf("stopped routing of %s: couldn't find the event", req.name)
		}
	}

	waitCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	idx, ok := ci.waitForHandler(waitCtx, req.name)
	cancel()
	if !ok {
		// The napp may only be listening on popstate: still deliver it, but
		// there is no result to wait for.
		ci.sendAction(-1, req.name, payload, nil)
		return nil, nil
	}

	ci.lastAction = &actionRequest{name: req.name, payload: payload}

	if idx < 0 {
		ci.sendAction(-1, req.name, payload, nil)
		return nil, nil
	}

	ci.dispMu.Lock()
	if ci.dispatches == nil {
		ci.dispatches = make(map[int]chan wireMsg)
	}
	ci.dispSerial++
	id := ci.dispSerial
	ch := make(chan wireMsg, 1)
	ci.dispatches[id] = ch
	ci.dispMu.Unlock()

	defer func() {
		ci.dispMu.Lock()
		delete(ci.dispatches, id)
		ci.dispMu.Unlock()
	}()

	ci.sendAction(id, req.name, payload, &idx)

	select {
	case <-ci.gone:
		return nil, errors.New("the napp window closed before answering")
	case resp := <-ch:
		if resp.Error != "" {
			return nil, errors.New(resp.Error)
		}
		if len(resp.Result) == 0 {
			return nil, nil
		}
		var out any
		if err := json.Unmarshal(resp.Result, &out); err != nil {
			return nil, nil
		}
		return out, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(60 * time.Second):
		return nil, errors.New("action timed out")
	}
}

// sendAction asks the child to run the dispatch inside its webview.
func (ci *childInfo) sendAction(id int, name string, payload json.RawMessage, idx *int) {
	if len(payload) == 0 {
		payload = json.RawMessage("null")
	}
	ci.send(wireMsg{
		T:      "action",
		ID:     id,
		Method: name,
		Params: string(payload),
		Idx:    idx,
	})
}

// settleDispatch resolves the waiter for a dispatch the napp just answered.
func (ci *childInfo) settleDispatch(id int, result json.RawMessage, errMsg string) {
	ci.dispMu.Lock()
	ch := ci.dispatches[id]
	ci.dispMu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- wireMsg{Result: result, Error: errMsg}:
	default:
	}
}

// resolveViewPayload turns a string payload for a view:<kind> action into the
// event it points at: either the event itself as JSON (a napp handing over
// what it had) or a nip19 code / event id to fetch. Non-string payloads (the
// event object already) pass through untouched.
func resolveViewPayload(ctx context.Context, payload json.RawMessage) (json.RawMessage, bool) {
	trimmed := strings.TrimSpace(string(payload))
	if !strings.HasPrefix(trimmed, `"`) {
		// already an object (or nothing we can resolve): hand it over as is
		return payload, len(trimmed) > 0 && trimmed != "null"
	}

	var code string
	if err := json.Unmarshal(payload, &code); err != nil {
		return payload, false
	}
	code = strings.TrimSpace(code)
	if code == "" {
		return payload, false
	}

	// leniently accept the event serialized as a JSON string
	if strings.HasPrefix(code, "{") {
		var evt nostr.Event
		if err := json.Unmarshal([]byte(code), &evt); err == nil && evt.ID != nostr.ZeroID {
			out, err := json.Marshal(evt)
			if err == nil {
				return out, true
			}
		}
		return payload, false
	}

	if sys == nil {
		return payload, false
	}
	fetchCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	evt, _, err := sys.FetchSpecificEventFromInput(fetchCtx, code,
		sdk.FetchSpecificEventParameters{SaveToLocalStore: true})
	if err != nil || evt == nil {
		return payload, false
	}
	out, err := json.Marshal(*evt)
	if err != nil {
		return payload, false
	}
	return out, true
}
