package backend

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
	"sync"
	"sync/atomic"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/sdk"
)

// An Instance is one open napp window, whatever a window happens to be on
// this platform: the backend only ever talks to it through its Transport.

type Instance struct {
	// instance is what the napp sees as window.napp.instance: a serial,
	// unique per window — or the napp's own id when it declares `singleton`.
	instance string
	number   int
	napp     Napp

	sendMu    sync.Mutex
	transport Transport
	queued    []WireMsg

	// gone is closed when the window is gone, so nothing waits on a dead
	// window (an action dispatch, say) longer than it has to.
	gone     chan struct{}
	goneOnce sync.Once

	// subs maps a feed callbackId to the canceller of its subscription.
	subs  map[int]context.CancelFunc
	subMu sync.Mutex

	// actions maps a registerAction() pattern to its handler index (-1 when
	// the napp registered no handler and only listens on popstate).
	// changed is closed and replaced on every registration, so waiters can
	// block until the napp they just launched is ready for their action.
	actionsMu sync.Mutex
	actions   map[string]int
	changed   chan struct{}

	// lastAction is the action the window is currently showing, either
	// dispatched by the host or pushed by the napp itself via history.
	lastAction atomic.Pointer[actionRequest]
	replaying  atomic.Bool

	// dispatches maps a dispatch id to the channel waiting for the napp's
	// answer (bridge.js replies with the napp.dispatchResult rpc).
	dispMu     sync.Mutex
	dispSerial int
	dispatches map[int]chan WireMsg
}

type actionRequest struct {
	name    string
	payload json.RawMessage
}

// windowRecord is what the launcher remembers about a window it has opened
// this run: its instance, the napp it runs, and the actions dispatched into it
// so a window closed and reopened lands back where it was. Session state, not
// persisted: nothing here survives the launcher quitting.
type windowRecord struct {
	Instance string
	NappID   string
	Actions  []recordedAction
}

type recordedAction struct {
	Name    string
	Payload json.RawMessage
}

// ID is the instance id the napp knows itself by.
func (ci *Instance) ID() string { return ci.instance }

// Napp is what this window is running.
func (ci *Instance) Napp() Napp { return ci.napp }

var (
	instancesMu    sync.Mutex
	instances      []*Instance
	instanceSerial atomic.Int64
	windowSerial   atomic.Int64

	// windows is every window opened this run, keyed by instance: the open
	// ones plus the closed ones still listed for reopening. A window is
	// closed when its instance is gone from instances.
	windowsMu sync.Mutex
	windows   = make(map[string]windowRecord)
)

// ─── registry ────────────────────────────────────────────────────

func registerInstance(ci *Instance) {
	instancesMu.Lock()
	instances = append(instances, ci)
	instancesMu.Unlock()
	notifyState()
}

func lookupInstance(instance string) *Instance {
	instancesMu.Lock()
	defer instancesMu.Unlock()
	for _, ci := range instances {
		if ci.instance == instance {
			return ci
		}
	}
	return nil
}

func allInstances() []*Instance {
	instancesMu.Lock()
	defer instancesMu.Unlock()
	return append([]*Instance(nil), instances...)
}

// runningForNapp returns the open instances of a given napp.
func runningForNapp(nappID string) []*Instance {
	instancesMu.Lock()
	defer instancesMu.Unlock()
	out := make([]*Instance, 0, 1)
	for _, ci := range instances {
		if ci.napp.ID == nappID {
			out = append(out, ci)
		}
	}
	return out
}

// OpenWindows lists the open napp instances, for a window list or a tab
// switcher.
func OpenWindows() []WindowInfo {
	open := allInstances()
	out := make([]WindowInfo, 0, len(open))
	for _, ci := range open {
		info := WindowInfo{
			Instance: ci.instance,
			NappID:   ci.napp.ID,
			Name:     ci.napp.Label(),
			Open:     true,
		}
		if last := ci.lastAction.Load(); last != nil {
			info.Action = last.name
		}
		out = append(out, info)
	}
	return out
}

func ManagedWindows() []WindowInfo {
	active := OpenWindows()
	byID := make(map[string]WindowInfo, len(active))
	for _, w := range active {
		byID[w.Instance] = w
	}
	var closed []WindowInfo
	for _, rec := range windowRecords() {
		if _, ok := byID[rec.Instance]; ok {
			continue
		}
		napp, ok := InstalledNapp(rec.NappID)
		if !ok {
			continue
		}
		info := WindowInfo{Instance: rec.Instance, NappID: rec.NappID, Name: napp.Label(), Open: false}
		if len(rec.Actions) > 0 {
			info.Action = rec.Actions[len(rec.Actions)-1].Name
		}
		closed = append(closed, info)
	}
	// windowRecords walks a map: sort the closed ones or they jump around
	// between frames and the list flickers.
	sort.Slice(closed, func(i, j int) bool {
		if closed[i].Name != closed[j].Name {
			return closed[i].Name < closed[j].Name
		}
		return closed[i].Instance < closed[j].Instance
	})
	return append(active, closed...)
}

// nextInstanceID is the napp's own id when it is a singleton (stable across
// launches, as behavior.md promises) and a serial otherwise.
func nextInstanceID(n Napp) string {
	if n.Singleton {
		return n.ID
	}
	return strconv.FormatInt(instanceSerial.Add(1), 10)
}

// ─── talking to a window ─────────────────────────────────────────

// send hands a message to the window, holding on to it if the platform hasn't
// attached the transport yet (an Android WebView is created asynchronously).
func (ci *Instance) send(m WireMsg) {
	ci.sendMu.Lock()
	if ci.transport == nil {
		ci.queued = append(ci.queued, m)
		ci.sendMu.Unlock()
		return
	}
	t := ci.transport
	ci.sendMu.Unlock()
	t.Send(m)
}

func (ci *Instance) attach(t Transport) {
	ci.sendMu.Lock()
	ci.transport = t
	queued := ci.queued
	ci.queued = nil
	ci.sendMu.Unlock()
	for _, m := range queued {
		t.Send(m)
	}
}

func (ci *Instance) eval(code string) {
	ci.send(WireMsg{T: "eval", Code: code})
}

// Close asks the window to go away (the header ×, napp.close(), the launcher
// closing a tab).
func (ci *Instance) Close() {
	ci.sendMu.Lock()
	t := ci.transport
	ci.sendMu.Unlock()
	if t != nil {
		t.Close()
	}
}

// CloseWindow closes an instance by id.
func CloseWindow(instance string) {
	if ci := lookupInstance(instance); ci != nil {
		ci.Close()
	}
}

// CloseAllWindows closes every open napp, for a launcher shutting down.
func CloseAllWindows() {
	open := allInstances()
	for _, ci := range open {
		ci.Close()
	}
	log.Info().Int("count", len(open)).Msg("closed all napp windows")
}

// ─── platform callbacks ──────────────────────────────────────────

// HandleWireMessage takes what a napp's shell sent up, as raw JSON. Platforms
// carrying the protocol as strings (Android over JNI) use this.
func HandleWireMessage(instance string, raw string) {
	m, err := ParseWireMsg(raw)
	if err != nil {
		log.Warn().Str("instance", instance).Err(err).Msg("unreadable message from napp")
		return
	}
	HandleMessage(instance, m)
}

// HandleMessage takes a parsed message from a napp's shell.
func HandleMessage(instance string, m WireMsg) {
	ci := lookupInstance(instance)
	if ci == nil {
		log.Warn().Str("instance", instance).Str("t", m.T).Msg("message for an unknown window")
		return
	}
	switch m.T {
	case "promptAnswer":
		// the answer of the prompt overlaying this window. Answered
		// inline, not in a goroutine: it mutates the prompt state and
		// quicky; the window sends nothing else worth racing on.
		ci.handlePromptAnswer(m)
	case "rpc":
		go ci.handleRPC(m)
	default:
		log.Debug().Str("instance", instance).Str("t", m.T).Msg("ignoring message from napp")
	}
}

func (ci *Instance) handleRPC(m WireMsg) {
	result, err := bridgeRPC(ci)(m.Method, m.Params)
	resp := WireMsg{T: "resp", ID: m.ID}
	if err != nil {
		log.Warn().Str("method", m.Method).Err(err).Msg("napp rpc error")
		resp.Error = err.Error()
	} else if raw, mErr := json.Marshal(result); mErr != nil {
		log.Error().Str("method", m.Method).Err(mErr).Msg("napp rpc marshal error")
		resp.Error = mErr.Error()
	} else {
		resp.Result = raw
	}
	ci.send(resp)
}

// WindowClosed is what a platform calls once a napp's window is really gone.
// It unblocks everything that was waiting on that window.
func WindowClosed(instance string) {
	ci := lookupInstance(instance)
	if ci == nil {
		return
	}

	ci.subMu.Lock()
	for _, cancel := range ci.subs {
		cancel()
	}
	ci.subs = make(map[int]context.CancelFunc)
	ci.subMu.Unlock()

	ci.goneOnce.Do(func() { close(ci.gone) })

	instancesMu.Lock()
	for i, c := range instances {
		if c == ci {
			instances = append(instances[:i], instances[i+1:]...)
			break
		}
	}
	instancesMu.Unlock()
	log.Info().Str("instance", ci.instance).Str("napp", ci.napp.ID).Msg("napp window closed")
	notifyState()
}

// ─── registered actions ──────────────────────────────────────────

func (ci *Instance) registerAction(pattern string, idx int) {
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
func (ci *Instance) handlerFor(name string) (int, bool) {
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
func (ci *Instance) waitForHandler(ctx context.Context, name string) (int, bool) {
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

// Launch opens a napp window without waiting for it (what a launcher button
// does). Failures show up as the launcher's error.
func Launch(napp Napp) {
	// a launch the user asked for is what the installed list is ordered by
	markLaunched(napp.ID)

	go func() {
		if _, err := launch(context.Background(), napp); err != nil {
			log.Error().Err(err).Str("napp", napp.ID).Msg("launch failed")
			SetFetchErr("launch failed: " + err.Error())
		}
	}()
}

// LaunchByID opens an installed napp by id.
func LaunchByID(id string) {
	if n, ok := InstalledNapp(id); ok {
		Launch(n)
		return
	}
	SetFetchErr("napp " + id + " is not installed")
}

// launch opens a napp window and returns its instance. A singleton that is
// already open is surfaced instead of opened again.
func launch(ctx context.Context, napp Napp) (*Instance, error) {
	return launchWithInstance(ctx, napp, "")
}

func launchWithInstance(ctx context.Context, napp Napp, requestedInstance string) (*Instance, error) {
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
	pageURL := ""
	if d := devLookup(id); d != nil {
		// dev napps live in memory, not on disk: the shell navigates to
		// the throwaway server (folder napps) or the dev server (url napps)
		pageURL = d.pageURL()
		if pageURL == "" {
			return nil, fmt.Errorf("dev napp %s has nowhere to run", id)
		}
	} else {
		if err := os.MkdirAll(appDir, 0755); err != nil {
			return nil, err
		}
		if _, err := os.Stat(filepath.Join(appDir, "index.html")); err != nil {
			return nil, fmt.Errorf("napp %s is not installed", id)
		}
	}

	themeName, themeVars := Theme()
	winW, winH := napp.WindowSize()
	instance := requestedInstance
	if instance == "" {
		instance = nextInstanceID(napp)
	}
	ci := &Instance{
		instance:   instance,
		number:     int(windowSerial.Add(1)),
		napp:       napp,
		subs:       make(map[int]context.CancelFunc),
		actions:    make(map[string]int),
		changed:    make(chan struct{}),
		dispatches: make(map[int]chan WireMsg),
		gone:       make(chan struct{}),
	}

	// registered before the window exists, so a napp that starts talking
	// immediately is never talking to nobody
	registerInstance(ci)

	log.Info().Str("napp", id).Str("name", napp.Name).Str("instance", ci.instance).
		Msg("launch napp")

	transport, err := host.OpenWindow(WindowSpec{
		Instance:    ci.instance,
		Number:      ci.number,
		NappID:      napp.ID,
		Name:        napp.Label(),
		Description: napp.Description,
		Dir:         appDir,
		URL:         pageURL,
		Requires:    napp.Requires,
		Theme:       themeName,
		ThemeVars:   themeVars,
		Width:       winW,
		Height:      winH,
	})
	if err != nil {
		WindowClosed(ci.instance)
		return nil, err
	}
	ci.attach(transport)
	rememberWindow(ci)
	return ci, nil
}

// ─── window records ───────────────────────────────────────────────

func windowRecords() []windowRecord {
	windowsMu.Lock()
	defer windowsMu.Unlock()
	out := make([]windowRecord, 0, len(windows))
	for _, w := range windows {
		w.Actions = append([]recordedAction(nil), w.Actions...)
		out = append(out, w)
	}
	return out
}

func lookupWindow(instance string) *windowRecord {
	windowsMu.Lock()
	defer windowsMu.Unlock()
	w, ok := windows[instance]
	if !ok {
		return nil
	}
	w.Actions = append([]recordedAction(nil), w.Actions...)
	return &w
}

func putWindow(w windowRecord) {
	windowsMu.Lock()
	windows[w.Instance] = w
	windowsMu.Unlock()
}

// rememberWindow starts the record of a window that just came up, keeping the
// actions a previous window with the same instance id had reached (a singleton
// reopened under its own id).
func rememberWindow(ci *Instance) {
	w := windowRecord{Instance: ci.instance, NappID: ci.napp.ID}
	if old := lookupWindow(ci.instance); old != nil {
		w.Actions = old.Actions
	}
	putWindow(w)
}

func ReopenWindow(instance string) {
	for _, rec := range windowRecords() {
		if rec.Instance != instance {
			continue
		}
		napp, ok := InstalledNapp(rec.NappID)
		if !ok {
			return
		}
		go func(rec windowRecord, napp Napp) {
			ci, err := launchWithInstance(context.Background(), napp, rec.Instance)
			if err != nil {
				log.Warn().Err(err).Str("instance", rec.Instance).Msg("could not reopen napp window")
				return
			}
			replayActions(ci, rec)
		}(rec, napp)
		return
	}
}

// replayActions puts a reopened window back where the one that closed had
// navigated to, by re-dispatching the actions it was last sent.
func replayActions(ci *Instance, rec windowRecord) {
	ci.replaying.Store(true)
	defer ci.replaying.Store(false)
	for _, action := range rec.Actions {
		if _, err := dispatchToInstance(context.Background(), ci, &actionRequest{name: action.Name, payload: action.Payload}); err != nil {
			log.Warn().Err(err).Str("instance", rec.Instance).Msg("could not replay napp action")
			break
		}
	}
}

func recordAction(ci *Instance, req *actionRequest) {
	if ci.replaying.Load() {
		return
	}
	w := lookupWindow(ci.instance)
	if w == nil {
		w = &windowRecord{Instance: ci.instance, NappID: ci.napp.ID}
	}
	w.Actions = append(w.Actions, recordedAction{Name: req.name, Payload: append(json.RawMessage(nil), req.payload...)})
	putWindow(*w)
}

// ─── action dispatch ─────────────────────────────────────────────

type actionOptions struct {
	Instance  string `json:"instance"`
	Auxiliary bool   `json:"auxiliary"`
}

// RunAction fires an action from outside any napp (a launcher shortcut, a
// shared link), with the same routing napps get.
func RunAction(ctx context.Context, name string, payload string) (any, error) {
	var raw json.RawMessage
	if strings.TrimSpace(payload) != "" {
		raw = json.RawMessage(payload)
	}
	return runNappAction(ctx, nil, name, raw, actionOptions{})
}

// runNappAction is the whole of napp.action(): find (or open) a window that
// handles `name` and hand it the payload, answering with whatever the
// handler returned.
func runNappAction(
	ctx context.Context,
	caller *Instance,
	name string,
	payload json.RawMessage,
	opts actionOptions,
) (any, error) {
	if name == "" {
		return nil, errors.New("napp.action: action name is required")
	}

	callerName := "launcher"
	if caller != nil {
		callerName = caller.napp.Label()
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
		ci, err := launch(ctx, candidates[0])
		if err != nil {
			return nil, err
		}
		return dispatchToInstance(ctx, ci, req)
	}

	choice, ok := askActionHandler(caller, name, payload, candidates, open)
	if !ok {
		return nil, errors.New("action handler selection cancelled")
	}
	if choice.Instance != "" {
		ci := lookupInstance(choice.Instance)
		if ci == nil {
			return nil, fmt.Errorf("instance %q is gone", choice.Instance)
		}
		return dispatchToInstance(ctx, ci, req)
	}
	for _, n := range candidates {
		if n.ID == choice.NappID {
			ci, err := launch(ctx, n)
			if err != nil {
				return nil, err
			}
			return dispatchToInstance(ctx, ci, req)
		}
	}
	return nil, fmt.Errorf("napp %q is gone", choice.NappID)
}

// findHandlersForAction lists the installed and dev napps declaring this action
// plus the already-open windows among them. "view" declared by a napp matches
// every "view:<number>" dispatch.
func findHandlersForAction(name string) ([]Napp, []*Instance) {
	candidates := make([]Napp, 0, 2)
	for _, n := range installedNapps() {
		if n.Handles(name) {
			candidates = append(candidates, n)
		}
	}
	for _, n := range DevNapps() {
		if n.Handles(name) {
			candidates = append(candidates, n)
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].Name < candidates[j].Name })

	open := make([]*Instance, 0, 2)
	for _, n := range candidates {
		open = append(open, runningForNapp(n.ID)...)
	}
	return candidates, open
}

// dispatchToInstance sends an action to an open window and waits for the
// handler's result. view:<kind> payloads are resolved to full events first,
// as napps registering a specific kind are promised a resolved event.
func dispatchToInstance(ctx context.Context, ci *Instance, req *actionRequest) (any, error) {
	payload := req.payload
	if strings.HasPrefix(req.name, "view:") {
		if resolved, ok := resolveViewPayload(ctx, payload); ok {
			payload = resolved
		} else {
			return nil, fmt.Errorf("stopped routing of %s: couldn't find the event", req.name)
		}
	}
	recordAction(ci, &actionRequest{name: req.name, payload: payload})

	waitCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	idx, ok := ci.waitForHandler(waitCtx, req.name)
	cancel()
	if !ok {
		// The napp may only be listening on popstate: still deliver it, but
		// there is no result to wait for.
		ci.sendAction(-1, req.name, payload, nil)
		return nil, nil
	}
	ci.lastAction.Store(&actionRequest{name: req.name, payload: payload})
	notifyState()

	if idx < 0 {
		ci.sendAction(-1, req.name, payload, nil)
		return nil, nil
	}

	ci.dispMu.Lock()
	if ci.dispatches == nil {
		ci.dispatches = make(map[int]chan WireMsg)
	}
	ci.dispSerial++
	id := ci.dispSerial
	ch := make(chan WireMsg, 1)
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

// sendAction asks the shell to run the dispatch inside its webview.
func (ci *Instance) sendAction(id int, name string, payload json.RawMessage, idx *int) {
	if len(payload) == 0 {
		payload = json.RawMessage("null")
	}
	ci.send(WireMsg{
		T:      "action",
		ID:     id,
		Method: name,
		Params: string(payload),
		Idx:    idx,
	})
}

// settleDispatch resolves the waiter for a dispatch the napp just answered.
func (ci *Instance) settleDispatch(id int, result json.RawMessage, errMsg string) {
	ci.dispMu.Lock()
	ch := ci.dispatches[id]
	ci.dispMu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- WireMsg{Result: result, Error: errMsg}:
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
