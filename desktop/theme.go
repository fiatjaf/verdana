package main

import (
	"encoding/json"
	"fmt"
	"image/color"
	"sync"

	"gioui.org/widget/material"

	"verdana/backend"
)

// The launcher has one theme at a time and every napp tracks it: the Gio
// palette below draws the launcher itself, and the same colors travel to each
// napp window as the CSS tokens behavior.md documents (`data-theme` on <html>
// plus `--surface`/`--text` & friends on :root).
//
// Delivering them is the backend's job — it hands them to a window as it
// opens and pushes a `theme` message to the open ones — so all this file does
// is own the colors and tell the backend which ones are current.

type themePalette struct {
	name string

	// Gio's own four
	bg         color.NRGBA
	fg         color.NRGBA
	contrastBg color.NRGBA
	contrastFg color.NRGBA

	// the rest of what layout.go paints with
	card     color.NRGBA
	chipBg   color.NRGBA
	chipFg   color.NRGBA
	border   color.NRGBA
	codeBg   color.NRGBA
	codeFg   color.NRGBA
	subtle   color.NRGBA
	muted    color.NRGBA
	danger   color.NRGBA
	imageBg  color.NRGBA
	inputHnt color.NRGBA
}

func rgb(v uint32) color.NRGBA {
	return color.NRGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 0xff}
}

var lightPalette = themePalette{
	name:       "light",
	bg:         rgb(0xffffff),
	fg:         rgb(0x000000),
	contrastBg: rgb(0x3f51b5),
	contrastFg: rgb(0xffffff),
	card:       rgb(0xf2f2f2),
	chipBg:     rgb(0xe8e8e8),
	chipFg:     rgb(0x333333),
	border:     rgb(0xcccccc),
	codeBg:     rgb(0xf0f0f0),
	codeFg:     rgb(0x333333),
	subtle:     rgb(0x666666),
	muted:      rgb(0x999999),
	danger:     rgb(0xcc2222),
	imageBg:    rgb(0xdddddd),
	inputHnt:   rgb(0x999999),
}

var darkPalette = themePalette{
	name:       "dark",
	bg:         rgb(0x17181b),
	fg:         rgb(0xe8e8ea),
	contrastBg: rgb(0x5c6bc0),
	contrastFg: rgb(0xffffff),
	card:       rgb(0x23252b),
	chipBg:     rgb(0x2b2e35),
	chipFg:     rgb(0xd8d8dc),
	border:     rgb(0x3a3d45),
	codeBg:     rgb(0x21232a),
	codeFg:     rgb(0xcfd2d8),
	subtle:     rgb(0xa0a4ad),
	muted:      rgb(0x7d818a),
	danger:     rgb(0xff6b6b),
	imageBg:    rgb(0x33363d),
	inputHnt:   rgb(0x6d717a),
}

var (
	themeMu  sync.Mutex
	curTheme = lightPalette
)

func paletteByName(name string) themePalette {
	if name == "dark" {
		return darkPalette
	}
	return lightPalette
}

func currentTheme() themePalette {
	themeMu.Lock()
	defer themeMu.Unlock()
	return curTheme
}

// applyStoredTheme picks up the theme the user last chose. Called once at
// startup, before the first frame and before any napp is launched, so napps
// start already themed.
func applyStoredTheme() {
	p := paletteByName(backend.ThemeName())

	themeMu.Lock()
	curTheme = p
	themeMu.Unlock()

	backend.SetTheme(p.name, p.varsJSON())
}

// apply hands the palette to Gio. The frame loop calls this on every frame,
// so a theme switch shows up without touching the *material.Theme from
// another goroutine.
func (p themePalette) apply(th *material.Theme) {
	th.Palette = material.Palette{
		Bg:         p.bg,
		Fg:         p.fg,
		ContrastBg: p.contrastBg,
		ContrastFg: p.contrastFg,
	}
}

func cssHex(c color.NRGBA) string {
	return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
}

// vars are the CSS custom properties (minus the leading `--`) that napps get
// on :root, so a napp painting with them tracks the launcher for free.
func (p themePalette) vars() map[string]string {
	return map[string]string{
		"surface":     cssHex(p.bg),
		"surface-alt": cssHex(p.card),
		"text":        cssHex(p.fg),
		"text-muted":  cssHex(p.subtle),
		"text-faint":  cssHex(p.muted),
		"border":      cssHex(p.border),
		"accent":      cssHex(p.contrastBg),
		"accent-text": cssHex(p.contrastFg),
		"danger":      cssHex(p.danger),
	}
}

// varsJSON is the tokens as the backend carries them.
func (p themePalette) varsJSON() string {
	varsJSON, err := json.Marshal(p.vars())
	if err != nil {
		return "{}"
	}
	return string(varsJSON)
}

// setTheme switches the launcher's theme. The backend persists it and pushes
// it to every open napp.
func setTheme(name string) {
	p := paletteByName(name)

	themeMu.Lock()
	if curTheme.name == p.name {
		themeMu.Unlock()
		return
	}
	curTheme = p
	themeMu.Unlock()

	if gioWin != nil {
		gioWin.Invalidate()
	}
	backend.SetTheme(p.name, p.varsJSON())
}

func toggleTheme() {
	if currentTheme().name == "dark" {
		setTheme("light")
	} else {
		setTheme("dark")
	}
}
