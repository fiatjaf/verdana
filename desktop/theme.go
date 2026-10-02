package main

import (
	"encoding/json"
	"fmt"
	"image/color"
	"math"
	"sync"
	"verdana/backend"

	"gioui.org/widget/material"
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
	devBg      color.NRGBA
	devFg      color.NRGBA

	// suggestBg/suggestFg mark the action-handler options the user has been
	// choosing already, as opposed to the rest of the list. It has to read
	// as neither of the other two: not accent (which means "already open")
	// and not dev (which means "a napp you are working on").
	suggestBg color.NRGBA
	suggestFg color.NRGBA

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
	devBg:      rgb(0xffe5b4),
	devFg:      rgb(0x704000),
	suggestBg:  rgb(0xd8efdc),
	suggestFg:  rgb(0x14532d),
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
	devBg:      rgb(0x5a3b1a),
	devFg:      rgb(0xffd79a),
	suggestBg:  rgb(0x1e3a2a),
	suggestFg:  rgb(0xb6e3c1),
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
	themeMu         sync.RWMutex
	curTheme        = lightPalette
	systemThemeMu   sync.RWMutex
	systemTheme     systemAppearance
	activeThemeMode string
)

func paletteByName(name string) themePalette {
	if name == "dark" {
		return darkPalette
	}
	return lightPalette
}

func currentTheme() themePalette {
	themeMu.RLock()
	defer themeMu.RUnlock()
	return curTheme
}

func appliedThemeMode() string {
	themeMu.RLock()
	defer themeMu.RUnlock()
	return activeThemeMode
}

type systemAppearance struct {
	dark      bool
	accent    color.NRGBA
	hasAccent bool
}

func resolvedPalette(mode string, appearance systemAppearance) themePalette {
	name := mode
	if mode == backend.ThemeSystem {
		if appearance.dark {
			name = backend.ThemeDark
		} else {
			name = backend.ThemeLight
		}
	}
	p := paletteByName(name)
	if mode == backend.ThemeSystem && appearance.hasAccent {
		p.contrastBg = appearance.accent
		p.contrastFg = readableText(appearance.accent)
	}
	return p
}

func readableText(bg color.NRGBA) color.NRGBA {
	linear := func(component uint8) float64 {
		value := float64(component) / 255
		if value <= 0.04045 {
			return value / 12.92
		}
		return math.Pow((value+0.055)/1.055, 2.4)
	}
	luma := 0.2126*linear(bg.R) + 0.7152*linear(bg.G) + 0.0722*linear(bg.B)
	// Black has better WCAG contrast than white above this luminance.
	if luma > 0.179 {
		return rgb(0x000000)
	}
	return rgb(0xffffff)
}

func applyThemeMode() {
	mode := backend.ThemeMode()
	systemThemeMu.RLock()
	appearance := systemTheme
	systemThemeMu.RUnlock()
	p := resolvedPalette(mode, appearance)

	themeMu.Lock()
	changed := curTheme != p
	curTheme = p
	activeThemeMode = mode
	themeMu.Unlock()
	if w := managerWindow(); changed && w != nil {
		w.Invalidate()
	}
	backend.SetTheme(p.name, p.varsJSON())
}

// startThemeController resolves the persisted preference before the first
// frame and then keeps system mode synchronized with the OS.
func startThemeController() func() {
	initial, changes, stop := watchSystemAppearance()
	systemThemeMu.Lock()
	systemTheme = initial
	systemThemeMu.Unlock()
	applyThemeMode()
	go func() {
		for appearance := range changes {
			systemThemeMu.Lock()
			systemTheme = appearance
			systemThemeMu.Unlock()
			if backend.ThemeMode() == backend.ThemeSystem {
				applyThemeMode()
			}
		}
	}()
	return stop
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
		"surface":      cssHex(p.bg),
		"surface-alt":  cssHex(p.card),
		"text":         cssHex(p.fg),
		"text-muted":   cssHex(p.subtle),
		"text-faint":   cssHex(p.muted),
		"border":       cssHex(p.border),
		"chip":         cssHex(p.chipBg),
		"chip-text":    cssHex(p.chipFg),
		"accent":       cssHex(p.contrastBg),
		"accent-text":  cssHex(p.contrastFg),
		"dev":          cssHex(p.devBg),
		"dev-text":     cssHex(p.devFg),
		"suggest":      cssHex(p.suggestBg),
		"suggest-text": cssHex(p.suggestFg),
		"danger":       cssHex(p.danger),
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

func setThemeMode(mode string) {
	backend.SetThemeMode(mode)
	applyThemeMode()
}

func toggleTheme() {
	switch backend.ThemeMode() {
	case backend.ThemeSystem:
		setThemeMode(backend.ThemeLight)
	case backend.ThemeLight:
		setThemeMode(backend.ThemeDark)
	default:
		setThemeMode(backend.ThemeSystem)
	}
}
