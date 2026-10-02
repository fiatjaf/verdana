package main

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"

	"github.com/gogpu/systray"
	"verdana/backend"
)

func newTray() *systray.SystemTray {
	icon := trayIcon()
	menu := systray.NewMenu()
	menu.Add("Open Verdana", showManager)
	menu.Add("Settings", func() {
		if err := backend.OpenLauncherSettings(); err != nil {
			log.Warn().Err(err).Msg("could not open settings from tray")
		}
	})
	var autostartItem *systray.MenuItem
	autostartItem = menu.AddCheckbox("Launch at login", autostartEnabled(), func() {
		enabled := !autostartItem.IsChecked()
		if err := (gioHost{}).SetAutostart(enabled); err != nil {
			log.Warn().Err(err).Msg("could not change launch at login")
			return
		}
		autostartItem.SetChecked(enabled)
	})
	menu.AddSeparator()
	menu.Add("Quit Verdana", quitDesktop)

	tray := systray.New().
		SetIcon(icon).
		SetTemplateIcon(trayTemplateIcon()).
		SetAppName(APP_TITLE).
		SetTooltip(APP_TITLE).
		SetMenu(menu).
		OnClick(showManager).
		Show()
	return tray
}

// trayIcon generates a small high-contrast V without adding another asset
// pipeline. macOS uses its alpha as a template icon; Windows and Linux use the
// green and white pixels directly.
func trayIcon() []byte {
	return drawTrayIcon(false)
}

func trayTemplateIcon() []byte {
	return drawTrayIcon(true)
}

func drawTrayIcon(template bool) []byte {
	const size = 32
	im := image.NewNRGBA(image.Rect(0, 0, size, size))
	if !template {
		draw.Draw(im, im.Bounds(), &image.Uniform{C: color.NRGBA{R: 36, G: 128, B: 92, A: 255}}, image.Point{}, draw.Src)
	}
	white := color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	for y := 7; y < 25; y++ {
		x := 7 + (y-7)/2
		for n := 0; n < 3; n++ {
			im.SetNRGBA(x+n, y, white)
			im.SetNRGBA(size-1-x-n, y, white)
		}
	}
	var out bytes.Buffer
	_ = png.Encode(&out, im)
	return out.Bytes()
}
