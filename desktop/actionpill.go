package main

import (
	"bytes"
	"encoding/json"
	"image"
	"strconv"
	"strings"

	"verdana/backend"

	"gioui.org/font"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget/material"
)

// pillDesc is what an action pill says: the action name, then whatever of
// the payload is worth showing, which for the common actions (see NAPP.md) is
// a readable summary and for everything else the JSON, cut short. When pubkey
// is set the pill ends with that user's name (and picture, with avatar).
type pillDesc struct {
	typ    string
	text   string
	pubkey string
	avatar bool
}

func describeAction(a backend.ShortcutAction) pillDesc {
	d := pillDesc{typ: a.Type}
	if len(a.Payload) == 0 {
		return d
	}

	switch {
	case a.Type == "profile":
		if pk, ok := payloadString(a.Payload); ok && isHexPubkey(pk) {
			d.pubkey = pk
			d.avatar = true
			return d
		}
	case a.Type == "view" || strings.HasPrefix(a.Type, "view:"):
		// a resolved event, or (only for a plain view) a nevent/naddr
		if ev, ok := payloadEvent(a.Payload); ok {
			// the kind is said in the text already
			d.typ = "view"
			d.text = "kind " + strconv.Itoa(ev.Kind)
			if isHexPubkey(ev.PubKey) {
				d.text += " from"
				d.pubkey = ev.PubKey
			}
			return d
		}
		if s, ok := payloadString(a.Payload); ok {
			d.text = truncate(strings.TrimPrefix(s, "nostr:"), 24)
			return d
		}
	case a.Type == "wiki-term":
		if s, ok := payloadField(a.Payload, "term"); ok {
			d.text = truncate(s, 32)
			return d
		}
	case a.Type == "hashtag":
		if s, ok := payloadField(a.Payload, "value"); ok {
			d.text = truncate(s, 32)
			return d
		}
	case a.Type == "user-search":
		if s, ok := payloadString(a.Payload); ok {
			d.text = truncate(s, 32)
			return d
		}
	case a.Type == "relay":
		var urls []string
		if json.Unmarshal(a.Payload, &urls) != nil {
			if s, ok := payloadString(a.Payload); ok {
				urls = []string{s}
			}
		}
		if len(urls) > 0 {
			for i, u := range urls {
				urls[i] = strings.TrimPrefix(strings.TrimPrefix(u, "wss://"), "ws://")
			}
			d.text = truncate(strings.Join(urls, ", "), 40)
			return d
		}
	}

	d.text = truncate(string(a.Payload), 40)
	return d
}

func payloadString(raw json.RawMessage) (string, bool) {
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return "", false
	}
	return s, true
}

// payloadField reads a string field of an object payload, or takes a bare
// string payload as that field.
func payloadField(raw json.RawMessage, field string) (string, bool) {
	if s, ok := payloadString(raw); ok {
		return s, true
	}
	var obj map[string]any
	if json.Unmarshal(raw, &obj) != nil {
		return "", false
	}
	s, ok := obj[field].(string)
	return s, ok
}

type pillEvent struct {
	Kind   int    `json:"kind"`
	PubKey string `json:"pubkey"`
}

// payloadEvent reads an event payload, also when it came as the event's JSON
// in a string.
func payloadEvent(raw json.RawMessage) (pillEvent, bool) {
	if s, ok := payloadString(raw); ok {
		raw = json.RawMessage(s)
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return pillEvent{}, false
	}
	var ev struct {
		Kind   *int   `json:"kind"`
		PubKey string `json:"pubkey"`
	}
	if json.Unmarshal(raw, &ev) != nil || ev.Kind == nil {
		return pillEvent{}, false
	}
	return pillEvent{Kind: *ev.Kind, PubKey: ev.PubKey}, true
}

func isHexPubkey(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// pillUser is the name and picture to show for a pubkey, fetching the
// profile in the background the first time it is asked for.
func pillUser(pubkey string) (string, string) {
	if p := cachedProfile(pubkey); p != nil && p.ShortName != "" {
		return truncate(p.ShortName, 20), p.Picture
	}
	ensureProfile(pubkey)
	return pubkey[:8] + "…", ""
}

// historyPills is a window's action log for the Windows tab: one pill per
// action it was sent, oldest first, so a row says where the window has been
// and not only where it is.
func historyPills(th *material.Theme, actions []backend.ShortcutAction) []layout.Widget {
	out := make([]layout.Widget, 0, len(actions))
	for i, a := range actions {
		// the same action twice in a row says nothing a single one doesn't
		if i > 0 && actions[i-1].Type == a.Type && bytes.Equal(actions[i-1].Payload, a.Payload) {
			continue
		}
		d := describeAction(a)
		out = append(out, func(gtx layout.Context) layout.Dimensions {
			return actionPill(gtx, th, d, nil)
		})
	}
	return out
}

// actionPill draws one action as a pill, with trailing (a remove button,
// say) at its end when given.
func actionPill(gtx layout.Context, th *material.Theme, d pillDesc, trailing layout.Widget) layout.Dimensions {
	p := currentTheme()
	caption := func(s string, bold bool) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			l := material.Caption(th, s)
			l.Color = p.chipFg
			l.MaxLines = 1
			if bold {
				l.Font.Weight = font.Bold
			}
			return l.Layout(gtx)
		}
	}
	gap := func(w layout.Widget) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(4)}.Layout(gtx, w)
		})
	}

	macro := op.Record(gtx.Ops)
	dims := layout.Inset{
		Top: unit.Dp(2), Bottom: unit.Dp(2),
		Left: unit.Dp(6), Right: unit.Dp(6),
	}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		typ := d.typ
		if d.text != "" || d.pubkey != "" {
			typ += ":"
		}
		children := []layout.FlexChild{layout.Rigid(caption(typ, true))}
		if d.text != "" {
			children = append(children, gap(caption(d.text, false)))
		}
		if d.pubkey != "" {
			name, pic := pillUser(d.pubkey)
			if d.avatar {
				children = append(children, gap(func(gtx layout.Context) layout.Dimensions {
					return avatar(gtx, pic, 14)
				}))
			}
			children = append(children, gap(caption(name, false)))
		}
		if trailing != nil {
			children = append(children, gap(trailing))
		}
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx, children...)
	})
	call := macro.Stop()
	r := gtx.Dp(unit.Dp(8))
	bg := clip.RRect{Rect: image.Rectangle{Max: dims.Size}, NW: r, NE: r, SW: r, SE: r}
	defer bg.Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, p.chipBg)
	call.Add(gtx.Ops)
	return dims
}

// flow lays widgets out left to right, wrapping onto a new line whenever the
// next one wouldn't fit in the width left.
func flow(gtx layout.Context, gap unit.Dp, children []layout.Widget) layout.Dimensions {
	maxW := gtx.Constraints.Max.X
	g := gtx.Dp(gap)
	cgtx := gtx
	cgtx.Constraints.Min = image.Point{}
	x, y, lineH, width := 0, 0, 0, 0
	for _, w := range children {
		macro := op.Record(gtx.Ops)
		dims := w(cgtx)
		call := macro.Stop()
		if x > 0 && x+dims.Size.X > maxW {
			x = 0
			y += lineH + g
			lineH = 0
		}
		st := op.Offset(image.Pt(x, y)).Push(gtx.Ops)
		call.Add(gtx.Ops)
		st.Pop()
		width = max(width, x+dims.Size.X)
		lineH = max(lineH, dims.Size.Y)
		x += dims.Size.X + g
	}
	return layout.Dimensions{Size: image.Pt(width, y+lineH)}
}

// removablePills is a bundle entry's actions in the editor: one pill each,
// in order, every one with a × that takes it out of the bundle.
func removablePills(th *material.Theme, e *bundleEditEntry) []layout.Widget {
	out := make([]layout.Widget, 0, len(e.actions))
	for i, a := range e.actions {
		d := describeAction(a)
		btn := &e.removeBtns[i]
		out = append(out, func(gtx layout.Context) layout.Dimensions {
			return actionPill(gtx, th, d, func(gtx layout.Context) layout.Dimensions {
				pointer.CursorPointer.Add(gtx.Ops)
				return withTooltip(gtx, btn, "Take this action out of the bundle", func(gtx layout.Context) layout.Dimensions {
					return btn.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						l := material.Caption(th, "×")
						l.Color = currentTheme().muted
						l.Font.Weight = font.Bold
						return l.Layout(gtx)
					})
				})
			})
		})
	}
	return out
}
