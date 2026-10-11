package main

import (
	"gioui.org/layout"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"fiatjaf.com/verdana/desktop/tooltip"
)

// tooltips is the launcher window's tooltip layer (see the tooltip package):
// main.go frames every launcher frame with trackPointer and drawTooltip.
var tooltips = tooltip.New()

func trackPointer(gtx layout.Context, w layout.Widget) layout.Dimensions {
	return tooltips.Track(gtx, w)
}

// drawTooltip draws the tooltip on the theme's inverted colors.
func drawTooltip(gtx layout.Context, th *material.Theme) {
	p := currentTheme()
	tooltips.Draw(gtx, th, p.fg, p.bg)
}

// withTooltip is a tooltip on a button of the launcher window.
func withTooltip(gtx layout.Context, btn *widget.Clickable, tip string, w layout.Widget) layout.Dimensions {
	return tooltips.With(gtx, btn, tip, w)
}
