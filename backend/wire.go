package backend

import "encoding/json"

// WireMsg is the whole protocol between the backend and a napp's shell (the
// webview child process on desktop, the WebView tab on Android). It travels
// as one JSON object per message, and both directions use the same shape.
//
// Backend → shell:
//
//	{t:"resp",   id, result|error}   the answer to an rpc
//	{t:"eval",   code}               run this in the napp's page
//	{t:"action", id, method, params, idx?}  dispatch an action
//	{t:"theme",  method:<name>, params:<vars json>}  theme changed
//	{t:"prompt", params:<prompt json>}   show a prompt overlaying this
//	                                     window ("" = take the overlay down)
//	{t:"close"}                      close this window
//
// Shell → backend (all through HandleWireMessage):
//
//	{t:"rpc", id, method, params}    a window.nostr/nostrdb/napp call
//	{t:"promptAnswer", id, params:{"ok","index"}}  the user answered
//	                                 the prompt shown over this window
type WireMsg struct {
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

// JSON is the message as one line of JSON, for platforms that carry it as a
// string (Android passes it through JNI, the desktop child through a pipe).
func (m WireMsg) JSON() string {
	b, err := json.Marshal(m)
	if err != nil {
		return `{"t":"noop"}`
	}
	return string(b)
}

// ParseWireMsg reads a message a shell sent up.
func ParseWireMsg(raw string) (WireMsg, error) {
	var m WireMsg
	err := json.Unmarshal([]byte(raw), &m)
	return m, err
}
