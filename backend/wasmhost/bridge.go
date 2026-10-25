package wasmhost

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"verdana/backend/wasm"
)

// This is bridge.js for a napp that has no page: everything a web napp
// reaches through window.nostr, window.nostrdb and window.napp, a wasm napp
// reaches through one import, napp_call(method, params), with the same
// method names and the same JSON the bridge's rpcs carry. Most calls go up
// to the launcher unchanged. The ones bridge.js answers inside the page
// (nip19, fx, localStorage reads, the instance, the theme, the time) are
// answered here, so they cost no round trip.
//
// Every answer, local or not, comes back the same way: queued as a message
// and handed to the module between two frames. A call never re-enters the
// module, which is what lets napp_call be called from anywhere — inside a
// frame, inside an event, inside another message.

// WireMsg is backend.WireMsg, restated so a shell using this package does
// not have to link the whole backend just for one struct (desktop/child does
// the same).
type WireMsg struct {
	T      string          `json:"t"`
	ID     int             `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params string          `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
	Code   string          `json:"code,omitempty"`
	Idx    *int            `json:"idx,omitempty"`
}

// Message is one thing waiting to be handed to the module through
// napp_receive (see Napp.Deliver).
type Message struct {
	Kind int32
	ID   int32
	Data []byte
}

// Prompt and PromptOption mirror backend.Prompt as a shell drawing the
// overlay needs to see it.
type Prompt struct {
	ID       int            `json:"id"`
	Title    string         `json:"title"`
	Detail   string         `json:"detail"`
	Warning  string         `json:"warning"`
	Items    []string       `json:"items"`
	Code     string         `json:"code"`
	Summary  string         `json:"summary"`
	Options  []PromptOption `json:"options"`
	Remember bool           `json:"remember"`

	// Countdown is, for a picker, how many milliseconds it waits before
	// picking its Default option by itself.
	Countdown int `json:"countdown"`
}

type PromptOption struct {
	Label     string `json:"label"`
	Detail    string `json:"detail"`
	Instance  string `json:"instance"`
	Dev       bool   `json:"dev"`
	Suggested bool   `json:"suggested"`

	// Name and Icon are only there for a picker window (see backend's
	// picker.go): the napp's plain name, and its icon's bytes.
	Name string `json:"name"`
	Icon []byte `json:"icon"`

	// Default is the action's configured default handler, put first.
	Default bool `json:"default"`
}

type themeState struct {
	Name string          `json:"name"`
	Vars json.RawMessage `json:"vars"`
}

// Config is what a bridge needs to know about its window.
type Config struct {
	// Instance is window.napp.instance.
	Instance string

	// Storage is the napp's storage as the launcher has it, a JSON object
	// of strings; anything else starts empty.
	Storage []byte

	// Theme and ThemeVars are the launcher's theme when the window opened.
	Theme     string
	ThemeVars string

	// Send takes every wire message going up to the launcher. Calls to it
	// are serialized.
	Send func(WireMsg)
}

// Bridge is one window's bridge to the launcher: napp_call goes out through
// it, and everything the launcher sends comes in through it, to be handed to
// the module between frames.
type Bridge struct {
	outMu sync.Mutex
	out   func(WireMsg)

	instance string

	mu     sync.Mutex
	serial int32
	inbox  []Message
	wake   func()

	// storeSubs are the nostrdb.subscribe calls still open, by call id
	// (which is also the callbackId the launcher knows them by). An event
	// for one the napp already closed is dropped instead of delivered.
	storeSubs map[int32]bool

	// subs are the napp.subscribe and napp.outbox calls still open, the
	// same way.
	subs map[int32]bool

	theme  themeState
	prompt *Prompt

	store *store
}

func NewBridge(cfg Config) *Bridge {
	b := &Bridge{
		out:       cfg.Send,
		instance:  cfg.Instance,
		storeSubs: make(map[int32]bool),
		subs:      make(map[int32]bool),
		store:     loadStore(cfg.Storage),
		theme:     themeState{Name: cfg.Theme, Vars: json.RawMessage(cfg.ThemeVars)},
	}
	if b.theme.Name == "" {
		b.theme.Name = "light"
	}
	if !json.Valid(b.theme.Vars) {
		b.theme.Vars = json.RawMessage("{}")
	}
	return b
}

// LoadStorage replaces the napp's storage, for a picker window that just
// became the window of a napp and so only now knows whose storage it is. It
// must come before the module runs: nothing guards the storage against a
// module reading it.
func (b *Bridge) LoadStorage(raw []byte) {
	b.store = loadStore(raw)
}

// SetWake is how the window asks to be woken when there is something for the
// module. Anything queued before it existed is waiting already.
func (b *Bridge) SetWake(fn func()) {
	b.mu.Lock()
	b.wake = fn
	pending := len(b.inbox) > 0 || b.prompt != nil
	b.mu.Unlock()
	if pending {
		fn()
	}
}

func (b *Bridge) push(m Message) {
	b.mu.Lock()
	b.inbox = append(b.inbox, m)
	wake := b.wake
	b.mu.Unlock()
	if wake != nil {
		wake()
	}
}

// Drain hands over everything queued for the module, in order.
func (b *Bridge) Drain() []Message {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := b.inbox
	b.inbox = nil
	return out
}

func (b *Bridge) send(m WireMsg) {
	if b.out == nil {
		return
	}
	b.outMu.Lock()
	defer b.outMu.Unlock()
	b.out(m)
}

// answer queues the result of call id for the module.
func (b *Bridge) answer(id int32, result any, err error) {
	if err != nil {
		b.push(Message{Kind: wasm.MsgError, ID: id, Data: []byte(err.Error())})
		return
	}
	raw, merr := json.Marshal(result)
	if merr != nil {
		b.push(Message{Kind: wasm.MsgError, ID: id, Data: []byte(merr.Error())})
		return
	}
	b.push(Message{Kind: wasm.MsgResult, ID: id, Data: raw})
}

// ─── napp → launcher ─────────────────────────────────────────────

// Call is the body of napp_call: it returns the call's id right away and
// answers later.
func (b *Bridge) Call(method string, params []byte) int32 {
	b.mu.Lock()
	b.serial++
	id := b.serial
	b.mu.Unlock()

	if len(bytes.TrimSpace(params)) == 0 {
		params = []byte("null")
	}
	if !json.Valid(params) {
		b.answer(id, nil, errors.New(method+": params are not valid JSON"))
		return id
	}

	if b.httpCall(id, method, params) {
		return id
	}

	if result, err, ok := b.local(id, method, params); ok {
		b.answer(id, result, err)
		return id
	}

	switch {
	case method == "napp.registerAction":
		// bridge.js sends the index of the handler function it kept; a
		// module has no function to keep, so idx is whatever number it
		// wants handed back with the action, 0 by default. -1 says it
		// wants to hear about the action but will not answer it.
		var p map[string]json.RawMessage
		if err := json.Unmarshal(params, &p); err != nil || p == nil {
			b.answer(id, nil, errors.New("napp.registerAction: params must be {pattern, idx?}"))
			return id
		}
		if _, ok := p["idx"]; !ok {
			p["idx"] = json.RawMessage("0")
		}
		params, _ = json.Marshal(p)

	case method == "nostrdb.subscribe", method == "napp.subscribe", method == "napp.outbox":
		// The call's own id is the subscription's callbackId: what it
		// brings comes back under the id the napp already has.
		var p map[string]json.RawMessage
		if err := json.Unmarshal(params, &p); err != nil || p == nil {
			b.answer(id, nil, errors.New(method+": params must be an object"))
			return id
		}
		p["callbackId"], _ = json.Marshal(id)
		params, _ = json.Marshal(p)
		b.mu.Lock()
		if method == "nostrdb.subscribe" {
			b.storeSubs[id] = true
		} else {
			b.subs[id] = true
		}
		b.mu.Unlock()

	case method == "signEvent":
		// A wasm napp has no wall clock to stamp an event with, so a
		// template without created_at is stamped now.
		var p map[string]json.RawMessage
		if json.Unmarshal(params, &p) == nil && p != nil {
			var at float64
			json.Unmarshal(p["created_at"], &at)
			if at == 0 {
				p["created_at"], _ = json.Marshal(time.Now().Unix())
				params, _ = json.Marshal(p)
			}
		}

	case method == "nostrdb.unsubscribe", method == "napp.unsubscribe":
		var p struct {
			CallbackID int32 `json:"callbackId"`
		}
		json.Unmarshal(params, &p)
		b.mu.Lock()
		delete(b.storeSubs, p.CallbackID)
		delete(b.subs, p.CallbackID)
		b.mu.Unlock()
	}

	b.send(WireMsg{T: "rpc", ID: int(id), Method: method, Params: string(params)})
	return id
}

// local answers the calls bridge.js answers without asking the launcher. ok
// is false for everything that has to go up.
func (b *Bridge) local(id int32, method string, params []byte) (result any, err error, ok bool) {
	switch method {
	case "napp.instance":
		return b.instance, nil, true

	case "napp.theme":
		b.mu.Lock()
		defer b.mu.Unlock()
		return b.theme, nil, true

	case "napp.now":
		// a wasm napp has no wall clock: napp_now_ms counts from the window
		// opening
		return time.Now().Unix(), nil, true

	case "nostrdb.supports":
		return []string{}, nil, true

	// localStorage: reads come from this window's copy of the store, which
	// the launcher seeded and keeps in sync with the napp's other windows.
	// Writes land here first (so a read right after sees them, and the
	// quota is enforced where the napp can be told) and then go up to be
	// persisted, exactly as the shim in bridge.js does it.
	case "napp.storageGet":
		var key string
		if err := json.Unmarshal(params, &key); err != nil {
			var p struct {
				Key string `json:"key"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return nil, errors.New("napp.storageGet: params must be a key"), true
			}
			key = p.Key
		}
		if v, found := b.store.get(key); found {
			return v, nil, true
		}
		return nil, nil, true

	case "napp.storageKeys":
		return b.store.keys(), nil, true

	case "napp.storageSet":
		var p struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, errors.New("napp.storageSet: params must be {key, value}"), true
		}
		if err := b.store.set(p.Key, p.Value); err != nil {
			return nil, err, true
		}
		return nil, nil, false

	case "napp.storageRemove":
		var p struct {
			Key string `json:"key"`
		}
		json.Unmarshal(params, &p)
		b.store.remove(p.Key)
		return nil, nil, false

	case "napp.storageClear":
		b.store.clear()
		return nil, nil, false
	}

	if fn, found := pureMethods[method]; found {
		result, err := fn(params)
		return result, err, true
	}
	return nil, nil, false
}

// ─── launcher → napp ─────────────────────────────────────────────

// HandleWire takes one message from the launcher. It returns false when the
// window should go away.
func (b *Bridge) HandleWire(m WireMsg) bool {
	switch m.T {
	case "close":
		return false

	case "resp":
		if m.Error != "" {
			b.push(Message{Kind: wasm.MsgError, ID: int32(m.ID), Data: []byte(m.Error)})
			return true
		}
		result := []byte(m.Result)
		if len(result) == 0 {
			result = []byte("null")
		}
		b.push(Message{Kind: wasm.MsgResult, ID: int32(m.ID), Data: result})

	case "action":
		idx := -1
		if m.Idx != nil {
			idx = *m.Idx
		}
		payload := json.RawMessage(m.Params)
		if len(bytes.TrimSpace(payload)) == 0 || !json.Valid(payload) {
			payload = json.RawMessage("null")
		}
		raw, _ := json.Marshal(struct {
			Name    string          `json:"name"`
			Payload json.RawMessage `json:"payload"`
			Idx     int             `json:"idx"`
		}{m.Method, payload, idx})
		b.push(Message{Kind: wasm.MsgAction, ID: int32(m.ID), Data: raw})

	case "stored":
		b.mu.Lock()
		open := b.storeSubs[int32(m.ID)]
		b.mu.Unlock()
		if open {
			b.push(Message{Kind: wasm.MsgStored, ID: int32(m.ID), Data: []byte(m.Params)})
		}

	case "subscription":
		var p struct {
			Type string `json:"type"`
		}
		json.Unmarshal([]byte(m.Params), &p)
		b.mu.Lock()
		open := b.subs[int32(m.ID)]
		if p.Type == "closed" {
			delete(b.subs, int32(m.ID))
		}
		b.mu.Unlock()
		if open {
			b.push(Message{Kind: wasm.MsgSubscription, ID: int32(m.ID), Data: []byte(m.Params)})
		}

	case "storage":
		var p struct {
			Op    string `json:"op"`
			Key   string `json:"key"`
			Value string `json:"value"`
		}
		if err := json.Unmarshal([]byte(m.Params), &p); err != nil {
			return true
		}
		b.store.apply(p.Op, p.Key, p.Value)
		b.push(Message{Kind: wasm.MsgStorage, Data: []byte(m.Params)})

	case "theme":
		vars := json.RawMessage(m.Params)
		if !json.Valid(vars) {
			vars = json.RawMessage("{}")
		}
		b.mu.Lock()
		b.theme = themeState{Name: m.Method, Vars: vars}
		raw, _ := json.Marshal(b.theme)
		b.mu.Unlock()
		b.push(Message{Kind: wasm.MsgTheme, Data: raw})

	case "prompt":
		var pv *Prompt
		if strings.TrimSpace(m.Params) != "" {
			pv = new(Prompt)
			if err := json.Unmarshal([]byte(m.Params), pv); err != nil {
				log.Warn().Err(err).Msg("unreadable prompt from launcher")
				return true
			}
		}
		b.mu.Lock()
		b.prompt = pv
		wake := b.wake
		b.mu.Unlock()
		if wake != nil {
			wake()
		}
	}
	return true
}

func (b *Bridge) ThemeName() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.theme.Name
}

// CurrentPrompt is the prompt covering the window, if any.
func (b *Bridge) CurrentPrompt() *Prompt {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.prompt
}

// AnswerPrompt sends the user's answer up and takes the overlay down; if the
// launcher has another prompt for this window it sends it right back. skip
// is the indexes of the items (relays) the user unticked.
func (b *Bridge) AnswerPrompt(id int, ok bool, index int, scope string, skip []int) {
	raw, _ := json.Marshal(map[string]any{"ok": ok, "index": index, "scope": scope, "skip": skip})
	b.send(WireMsg{T: "promptAnswer", ID: id, Params: string(raw)})
	b.mu.Lock()
	if b.prompt != nil && b.prompt.ID == id {
		b.prompt = nil
	}
	b.mu.Unlock()
}
