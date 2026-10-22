package main

import (
	"context"
	"image"
	"strings"
	"sync"
	"verdana/backend"

	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// The box in the header, before the theme toggle and "Log out", takes a pasted npub, nprofile, note,
// nevent or naddr (bare or as a nostr: link): a user opens their page, a
// napp its page, and any other event goes to a napp with view:<kind> (see
// backend.OpenCode). It sits small until focused, then takes the whole
// rest of the header.

var (
	openEd = widget.Editor{SingleLine: true, Submit: true}

	openMu      sync.Mutex
	openBusy    bool
	openErr     string
	openFailed  bool // openErr is new: the box is cleared to show it
	openPending *backend.Opened
)

// handleOpenBox runs the box's submits and Escape, and acts on whatever a
// finished lookup left behind. Called every frame on the Gio loop.
func handleOpenBox(gtx layout.Context) {
	for {
		ev, ok := gtx.Event(key.Filter{Focus: &openEd, Name: key.NameEscape})
		if !ok {
			break
		}
		if e, ok := ev.(key.Event); ok && e.State == key.Press {
			gtx.Execute(key.FocusCmd{})
		}
	}
	for {
		ev, ok := openEd.Update(gtx)
		if !ok {
			break
		}
		switch ev.(type) {
		case widget.SubmitEvent:
			submitCode(openEd.Text())
		case widget.ChangeEvent:
			// typing again clears the error (emptying the box to show it
			// is a change too, so that doesn't count)
			if openEd.Text() != "" {
				openMu.Lock()
				openErr = ""
				openMu.Unlock()
			}
		}
	}

	openMu.Lock()
	pending, failed := openPending, openFailed
	openPending, openFailed = nil, false
	openMu.Unlock()
	if failed {
		// the error shows as the hint of the emptied box
		openEd.SetText("")
	}
	if pending == nil {
		return
	}
	openEd.SetText("")
	gtx.Execute(key.FocusCmd{})
	switch pending.Kind {
	case backend.OpenedProfile:
		openProfileTab(pending.Pubkey)
	case backend.OpenedNapp:
		if pending.Napp != nil {
			openNappTab(*pending.Napp)
		}
	}
}

func submitCode(input string) {
	input = strings.TrimSpace(input)
	openMu.Lock()
	if input == "" || openBusy {
		openMu.Unlock()
		return
	}
	openBusy, openErr = true, ""
	openMu.Unlock()

	go func() {
		opened, err := backend.OpenCode(context.Background(), input)
		openMu.Lock()
		openBusy = false
		if err != nil {
			openErr, openFailed = err.Error(), true
		} else {
			openPending = &opened
		}
		openMu.Unlock()
		if gioWin != nil {
			gioWin.Invalidate()
		}
	}()
}

// layoutOpenBox draws the box right-aligned in whatever room it is given:
// narrow while unfocused, all of it while focused.
func layoutOpenBox(gtx layout.Context, th *material.Theme) layout.Dimensions {
	openMu.Lock()
	busy, errMsg := openBusy, openErr
	openMu.Unlock()

	focused := gtx.Focused(&openEd)
	width := gtx.Constraints.Max.X
	if !focused {
		width = min(width, gtx.Dp(unit.Dp(140)))
	}
	if width < gtx.Dp(unit.Dp(60)) {
		return layout.Dimensions{Size: image.Pt(gtx.Constraints.Min.X, 0)}
	}
	hint := "nostr:…"
	if focused {
		hint = "paste an npub, nprofile, nevent or naddr and press Enter"
	}
	if busy {
		hint = "opening…"
	} else if errMsg != "" {
		hint = errMsg
	}

	// fill the whole slot so what comes after stays pinned to the right
	gtx.Constraints.Min.X = gtx.Constraints.Max.X
	return layout.E.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = width
		gtx.Constraints.Max.X = width
		return editorBox(gtx, th, &openEd, hint)
	})
}
