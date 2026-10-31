package main

import (
	"image"

	"verdana/backend"

	"gioui.org/io/pointer"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// The unhandled-action notice: an action was fired with no napp to take it
// (see backend.State.UnhandledAction), so the launcher offers to look for
// one in the Explore tab, filtered down to the napps that handle it.

var (
	unhandledFindBtn    widget.Clickable
	unhandledDismissBtn widget.Clickable

	// unhandledRaised is the action the window was last raised for, so it
	// comes to the front once per notice rather than every frame.
	unhandledRaised string
)

// handleUnhandled reacts to the notice's buttons, and brings the launcher
// to the front when a new notice shows up: the action may have been fired
// from a napp window, or by a link, with the launcher behind them.
func handleUnhandled(gtx layout.Context, st backend.State) {
	action := st.UnhandledAction
	if action != "" && action != unhandledRaised && gioWin != nil {
		gioWin.Perform(system.ActionRaise)
	}
	unhandledRaised = action

	if unhandledFindBtn.Clicked(gtx) && action != "" {
		nappsMode = modeDiscovery
		setTab(tabDiscovery)
		filterEd.SetText(backend.ActionQuery(action))
		backend.DismissUnhandledAction()
	}
	if unhandledDismissBtn.Clicked(gtx) {
		backend.DismissUnhandledAction()
	}
}

// layoutUnhandled draws the notice, or nothing when there is none.
func layoutUnhandled(gtx layout.Context, th *material.Theme, st backend.State) layout.Dimensions {
	if st.UnhandledAction == "" {
		return layout.Dimensions{}
	}
	p := currentTheme()
	return layout.Inset{Bottom: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		macro := op.Record(gtx.Ops)
		dims := layout.UniformInset(unit.Dp(10)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					l := material.Body2(th, "No installed napp handles “"+st.UnhandledAction+"”.")
					l.Color = p.suggestFg
					return l.Layout(gtx)
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					pointer.CursorPointer.Add(gtx.Ops)
					b := material.Button(th, &unhandledFindBtn, "Find one in Explore")
					b.TextSize = unit.Sp(13)
					b.Inset = layout.UniformInset(unit.Dp(8))
					return withTooltip(gtx, &unhandledFindBtn, "List the napps published on the relays that handle this action", b.Layout)
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					pointer.CursorPointer.Add(gtx.Ops)
					b := material.Button(th, &unhandledDismissBtn, "✕")
					b.Background = p.chipBg
					b.Color = p.chipFg
					b.TextSize = unit.Sp(13)
					b.Inset = layout.UniformInset(unit.Dp(8))
					return withTooltip(gtx, &unhandledDismissBtn, "Dismiss", b.Layout)
				}),
			)
		})
		call := macro.Stop()
		bg := clip.RRect{Rect: image.Rectangle{Max: dims.Size}, NW: 8, NE: 8, SW: 8, SE: 8}
		stack := bg.Push(gtx.Ops)
		paint.Fill(gtx.Ops, p.suggestBg)
		stack.Pop()
		call.Add(gtx.Ops)
		return dims
	})
}
