package main

import (
	"context"
	"encoding/json"
	"io"
	"os/exec"
	"sync"

	"fiatjaf.com/nostr"
)

type NappPath struct {
	Path   string `json:"path"`
	Sha256 string `json:"sha256"`
}

type Napp struct {
	ID          string          `json:"id"`
	D           string          `json:"d"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Icon        string          `json:"icon"`
	Author      nostr.PubKey    `json:"author"`
	Actions     []string        `json:"actions"`
	Requires    []string        `json:"requires"`
	Singleton   bool            `json:"singleton"`
	CreatedAt   nostr.Timestamp `json:"created_at"`
	Paths       []NappPath      `json:"paths"`
	Servers     []string        `json:"servers"`
}

type AppState struct {
	ClientKey      nostr.SecretKey `json:"client_key"`
	Login          string          `json:"login"`
	Relays         []string        `json:"relays"`
	InstalledNapps map[string]Napp `json:"installed_napps"`

	// Theme is "light" or "dark": what the launcher draws with and what
	// every napp window is told to track.
	Theme string `json:"theme"`
}

type uiState struct {
	mu        sync.Mutex
	phase     string
	tab       int
	loginErr  string
	profName  string
	profPic   string
	fetchErr  string
	fetching  bool
	discovery []Napp
	installed []Napp
	busy      map[string]bool

	// prompt is the approval/picker dialog currently taking over the window,
	// promptQueue the ones waiting behind it (a napp can ask for several
	// things at once, and several napps can ask at the same time).
	prompt      *prompt
	promptQueue []*prompt

	// clipboard holds texts that napp.utils.copyText asked for: only a Gio
	// frame can execute clipboard.WriteCmd, so the rpc parks them here and
	// the next frame drains them.
	clipboard []string
}

type openReq struct {
	napp Napp
	dir  string

	// reply, when set, receives the started instance (or the error).
	reply chan launchResult
}

type launchResult struct {
	ci  *childInfo
	err error
}

type actionRequest struct {
	name    string
	payload json.RawMessage
}

type wireMsg struct {
	T      string          `json:"t"`
	ID     int             `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params string          `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
	Code   string          `json:"code,omitempty"`

	// Idx is the index of the handler registered by registerAction() that
	// should answer an action dispatch. Absent when the napp registered the
	// pattern without a handler (it only wants the popstate event).
	Idx *int `json:"idx,omitempty"`
}

type childInfo struct {
	// instance is what the napp sees as window.napp.instance: a serial,
	// unique per window — or the napp's own id when it declares `singleton`.
	instance string
	napp     Napp

	cmd    *exec.Cmd
	enc    *json.Encoder
	encMu  sync.Mutex
	stdout io.ReadCloser

	// gone is closed when the window is gone, so nothing waits on a dead
	// child (an action dispatch, say) longer than it has to.
	gone chan struct{}

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
	lastAction *actionRequest

	// dispatches maps a dispatch id to the channel waiting for the napp's
	// answer (bridge.js replies with the napp.dispatchResult rpc).
	dispMu     sync.Mutex
	dispSerial int
	dispatches map[int]chan wireMsg
}
