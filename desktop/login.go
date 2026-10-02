package main

import (
	"image"
	"image/color"
	"io"
	"strings"
	"verdana/backend"

	"gioui.org/font"
	"gioui.org/io/clipboard"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// loginScreen is the login phase's widgets: the nsec / bunker:// field and,
// behind "Connect signer", the nostrconnect QR code a signer scans with the
// relay it points to. Which of the two shows follows the backend: the QR
// view is up while a nostrconnect uri is on offer.
type loginScreen struct {
	list       widget.List
	ed         widget.Editor
	btn        widget.Clickable
	connectBtn widget.Clickable
	backBtn    widget.Clickable
	relayEd    widget.Editor
	relayBtn   widget.Clickable
	copyBtn    widget.Clickable

	// qrURI is the uri qr was drawn from; relaySeen the relay last put in
	// relayEd, so a new one from the backend replaces what is typed there.
	qrURI     string
	qr        paint.ImageOp
	relaySeen string
}

func newLoginScreen() *loginScreen {
	s := &loginScreen{}
	s.list.Axis = layout.Vertical
	s.ed.SingleLine = true
	s.ed.Submit = true
	s.relayEd.SingleLine = true
	s.relayEd.Submit = true
	return s
}

// update handles the screen's input and follows the backend's state.
func (s *loginScreen) update(gtx layout.Context, st backend.State) {
	submitted := func(ed *widget.Editor) bool {
		for {
			ev, ok := ed.Update(gtx)
			if !ok {
				return false
			}
			if _, ok := ev.(widget.SubmitEvent); ok {
				return true
			}
		}
	}

	if submitted(&s.ed) || s.btn.Clicked(gtx) {
		if input := strings.TrimSpace(s.ed.Text()); input != "" {
			go backend.Login(input)
		}
	}
	if s.connectBtn.Clicked(gtx) {
		go backend.StartNostrConnect()
	}
	if s.backBtn.Clicked(gtx) {
		go backend.CancelNostrConnect()
	}
	if submitted(&s.relayEd) || s.relayBtn.Clicked(gtx) {
		go backend.SetNostrConnectRelay(s.relayEd.Text())
	}
	if s.copyBtn.Clicked(gtx) && st.NostrConnectURI != "" {
		gtx.Execute(clipboard.WriteCmd{
			Type: "application/text",
			Data: io.NopCloser(strings.NewReader(st.NostrConnectURI)),
		})
	}

	if st.NostrConnectRelay != s.relaySeen {
		s.relaySeen = st.NostrConnectRelay
		s.relayEd.SetText(st.NostrConnectRelay)
	}
	if st.NostrConnectURI != s.qrURI {
		s.qrURI = st.NostrConnectURI
		s.qr = paint.ImageOp{}
		if img, err := backend.QRCode(s.qrURI); err == nil && s.qrURI != "" {
			s.qr = paint.NewImageOp(img)
			s.qr.Filter = paint.FilterNearest
		}
	}
}

func (s *loginScreen) layout(gtx layout.Context, th *material.Theme, st backend.State) layout.Dimensions {
	s.update(gtx, st)

	subtle := func(text string) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			l := emph(material.Body2(th, text))
			l.Color = currentTheme().subtle
			return l.Layout(gtx)
		}
	}
	space := func(dp unit.Dp) layout.Widget { return layout.Spacer{Height: dp}.Layout }
	chip := func(btn *widget.Clickable, label string) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			pointer.CursorPointer.Add(gtx.Ops)
			t := currentTheme()
			b := material.Button(th, btn, label)
			b.Background = t.chipBg
			b.Color = t.chipFg
			b.TextSize = unit.Sp(13)
			b.Inset = layout.UniformInset(unit.Dp(8))
			return b.Layout(gtx)
		}
	}

	title := func(text string) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			t := material.H5(th, text)
			t.Font.Weight = font.Bold
			return t.Layout(gtx)
		}
	}
	loginErr := func(gtx layout.Context) layout.Dimensions {
		if st.LoginErr == "" {
			return layout.Dimensions{}
		}
		return layout.Inset{Top: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			l := material.Body2(th, st.LoginErr)
			l.Color = currentTheme().danger
			return l.Layout(gtx)
		})
	}

	var rows []layout.Widget
	if st.NostrConnectURI == "" {
		rows = []layout.Widget{
			title("Log in to Verdana"),
			space(8),
			subtle("Paste your nsec or a bunker:// URL"),
			space(12),
			func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return editorBox(gtx, th, &s.ed, "nsec1... or bunker://...")
			},
			space(12),
			func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						pointer.CursorPointer.Add(gtx.Ops)
						return material.Button(th, &s.btn, "Log in").Layout(gtx)
					}),
					layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout),
					layout.Rigid(subtle("or")),
					layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout),
					layout.Rigid(chip(&s.connectBtn, "Connect signer")),
				)
			},
			loginErr,
		}
	} else {
		rows = []layout.Widget{
			title("Connect a signer"),
			space(8),
			subtle("Scan with your signer app (Amber, nsec.app, Primal…)"),
			space(12),
			s.layoutQR,
			space(8),
			chip(&s.copyBtn, "Copy connect link"),
			space(16),
			subtle("Relay"),
			space(6),
			func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(unit.Dp(420)))
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return editorBox(gtx, th, &s.relayEd, "wss://…")
					}),
					layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
					layout.Rigid(chip(&s.relayBtn, "Change")),
				)
			},
			loginErr,
			space(24),
			chip(&s.backBtn, "Back"),
		}
	}
	return material.List(th, &s.list).Layout(gtx, len(rows), func(gtx layout.Context, i int) layout.Dimensions {
		gtx.Constraints.Min.X = 0 // buttons keep their own width
		return rows[i](gtx)
	})
}

// layoutQR draws the QR code on white, whatever the theme: scanners want
// dark modules on a light background.
func (s *loginScreen) layoutQR(gtx layout.Context) layout.Dimensions {
	side := min(gtx.Dp(unit.Dp(232)), gtx.Constraints.Max.X)
	size := image.Pt(side, side)
	rect := clip.UniformRRect(image.Rectangle{Max: size}, gtx.Dp(unit.Dp(8)))
	paint.FillShape(gtx.Ops, color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}, rect.Op(gtx.Ops))
	if s.qr.Size() != (image.Point{}) {
		gtx.Constraints = layout.Exact(size)
		widget.Image{Src: s.qr, Fit: widget.Contain, Position: layout.Center}.Layout(gtx)
	}
	return layout.Dimensions{Size: size}
}
