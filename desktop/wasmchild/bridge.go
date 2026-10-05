package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"sync"

	"verdana/backend/wasm"
)

// This is bridge.js for a napp that has no page: everything a web napp
// reaches through window.nostr, window.nostrdb and window.napp, a wasm napp
// reaches through one import, napp_call(method, params), with the same
// method names and the same JSON the bridge's rpcs carry. Most calls go up
// to the launcher unchanged. The ones bridge.js answers inside the page
// (nip19, fx, localStorage reads, the instance, the theme) are answered here,
// so they cost no round trip.
//
// Every answer, local or not, comes back the same way: queued as a message
// and handed to the module between two frames. A call never re-enters the
// module, which is what lets napp_call be called from anywhere — inside a
// frame, inside an event, inside another message.

// wireMsg is backend.WireMsg, restated so this binary does not have to link
// the whole backend just for one struct (desktop/child does the same).
type wireMsg struct {
	T      string          `json:"t"`
	ID     int             `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params string          `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
	Code   string          `json:"code,omitempty"`
	Idx    *int            `json:"idx,omitempty"`
}

// message is one thing waiting to be handed to the module through
// napp_receive.
type message struct {
	kind int32
	id   int32
	data []byte
}

type themeState struct {
	Name string          `json:"name"`
	Vars json.RawMessage `json:"vars"`
}

type bridge struct {
	outMu sync.Mutex
	out   *json.Encoder

	mu     sync.Mutex
	serial int32
	inbox  []message
	wake   func()

	// feeds are the feed calls still open, by call id (which is also the
	// callbackId the launcher knows them by). A batch for a feed the napp
	// already closed is dropped instead of delivered.
	feeds map[int32]bool

	// subs are the napp.subscribe calls still open, the same way.
	subs map[int32]bool

	theme  themeState
	prompt *promptView

	store *store
}

func newBridge(out io.Writer) *bridge {
	b := &bridge{
		out:   json.NewEncoder(out),
		feeds: make(map[int32]bool),
		subs:  make(map[int32]bool),
		store: loadStore(os.Getenv("VERDANA_NAPP_STORAGE_FILE")),
		theme: themeState{Name: os.Getenv("VERDANA_THEME"), Vars: json.RawMessage(os.Getenv("VERDANA_THEME_VARS"))},
	}
	if b.theme.Name == "" {
		b.theme.Name = "light"
	}
	if !json.Valid(b.theme.Vars) {
		b.theme.Vars = json.RawMessage("{}")
	}
	return b
}

// setWake is how the window asks to be woken when there is something for the
// module. Anything queued before it existed is waiting already.
func (b *bridge) setWake(fn func()) {
	b.mu.Lock()
	b.wake = fn
	pending := len(b.inbox) > 0 || b.prompt != nil
	b.mu.Unlock()
	if pending {
		fn()
	}
}

func (b *bridge) push(m message) {
	b.mu.Lock()
	b.inbox = append(b.inbox, m)
	wake := b.wake
	b.mu.Unlock()
	if wake != nil {
		wake()
	}
}

// drain hands over everything queued for the module, in order.
func (b *bridge) drain() []message {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := b.inbox
	b.inbox = nil
	return out
}

func (b *bridge) send(m wireMsg) {
	b.outMu.Lock()
	defer b.outMu.Unlock()
	if err := b.out.Encode(m); err != nil {
		log.Debug().Err(err).Msg("could not write to the launcher")
	}
}

// answer queues the result of call id for the module.
func (b *bridge) answer(id int32, result any, err error) {
	if err != nil {
		b.push(message{kind: wasm.MsgError, id: id, data: []byte(err.Error())})
		return
	}
	raw, merr := json.Marshal(result)
	if merr != nil {
		b.push(message{kind: wasm.MsgError, id: id, data: []byte(merr.Error())})
		return
	}
	b.push(message{kind: wasm.MsgResult, id: id, data: raw})
}

// ─── napp → launcher ─────────────────────────────────────────────

// call is the body of napp_call: it returns the call's id right away and
// answers later.
func (b *bridge) call(method string, params []byte) int32 {
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

	case strings.HasPrefix(method, "napp.feeds.") && method != "napp.feeds.cancel":
		// The call's own id is the feed's callbackId: batches come back
		// under the id the napp already has.
		var p map[string]json.RawMessage
		if err := json.Unmarshal(params, &p); err != nil || p == nil {
			b.answer(id, nil, errors.New(method+": params must be an object"))
			return id
		}
		p["callbackId"], _ = json.Marshal(id)
		params, _ = json.Marshal(p)
		b.mu.Lock()
		b.feeds[id] = true
		b.mu.Unlock()

	case method == "napp.subscribe":
		// like a feed: the call's own id is the subscription's callbackId
		var p map[string]json.RawMessage
		if err := json.Unmarshal(params, &p); err != nil || p == nil {
			b.answer(id, nil, errors.New(method+": params must be {relays, filter, label?, maxEoseTimeout?}"))
			return id
		}
		p["callbackId"], _ = json.Marshal(id)
		params, _ = json.Marshal(p)
		b.mu.Lock()
		b.subs[id] = true
		b.mu.Unlock()

	case method == "napp.feeds.cancel", method == "napp.unsubscribe":
		var p struct {
			CallbackID int32 `json:"callbackId"`
		}
		json.Unmarshal(params, &p)
		b.mu.Lock()
		delete(b.feeds, p.CallbackID)
		delete(b.subs, p.CallbackID)
		b.mu.Unlock()
	}

	b.send(wireMsg{T: "rpc", ID: int(id), Method: method, Params: string(params)})
	return id
}

// local answers the calls bridge.js answers without asking the launcher. ok
// is false for everything that has to go up.
func (b *bridge) local(id int32, method string, params []byte) (result any, err error, ok bool) {
	switch method {
	case "napp.instance":
		return meta.Instance, nil, true

	case "napp.theme":
		b.mu.Lock()
		defer b.mu.Unlock()
		return b.theme, nil, true

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

// handleWire takes one message from the launcher. It returns false when the
// window should go away.
func (b *bridge) handleWire(m wireMsg) bool {
	switch m.T {
	case "close":
		return false

	case "resp":
		if m.Error != "" {
			b.push(message{kind: wasm.MsgError, id: int32(m.ID), data: []byte(m.Error)})
			return true
		}
		result := []byte(m.Result)
		if len(result) == 0 {
			result = []byte("null")
		}
		b.push(message{kind: wasm.MsgResult, id: int32(m.ID), data: result})

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
		b.push(message{kind: wasm.MsgAction, id: int32(m.ID), data: raw})

	case "feed":
		b.mu.Lock()
		open := b.feeds[int32(m.ID)]
		b.mu.Unlock()
		if open {
			b.push(message{kind: wasm.MsgFeed, id: int32(m.ID), data: []byte(m.Params)})
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
			b.push(message{kind: wasm.MsgSubscription, id: int32(m.ID), data: []byte(m.Params)})
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
		b.push(message{kind: wasm.MsgStorage, data: []byte(m.Params)})

	case "theme":
		vars := json.RawMessage(m.Params)
		if !json.Valid(vars) {
			vars = json.RawMessage("{}")
		}
		b.mu.Lock()
		b.theme = themeState{Name: m.Method, Vars: vars}
		raw, _ := json.Marshal(b.theme)
		b.mu.Unlock()
		b.push(message{kind: wasm.MsgTheme, data: raw})

	case "prompt":
		var pv *promptView
		if strings.TrimSpace(m.Params) != "" {
			pv = new(promptView)
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

func (b *bridge) themeName() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.theme.Name
}

// currentPrompt is the prompt covering the window, if any.
func (b *bridge) currentPrompt() *promptView {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.prompt
}

// answerPrompt sends the user's answer up and takes the overlay down; if the
// launcher has another prompt for this window it sends it right back.
func (b *bridge) answerPrompt(id int, ok bool, index int, scope string) {
	raw, _ := json.Marshal(map[string]any{"ok": ok, "index": index, "scope": scope})
	b.send(wireMsg{T: "promptAnswer", ID: id, Params: string(raw)})
	b.mu.Lock()
	if b.prompt != nil && b.prompt.ID == id {
		b.prompt = nil
	}
	b.mu.Unlock()
}
