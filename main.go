package main

import (
	"image"
	"image/color"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"gioui.org/app"
	"gioui.org/font"
	"gioui.org/font/gofont"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
	"github.com/abemedia/go-webview"
	_ "github.com/abemedia/go-webview/embedded"
)

type Napp struct {
	ID          string
	Name        string
	Description string
}

var napps = []Napp{
	{ID: "hello-world", Name: "Hello World", Description: "Simple hello world demo"},
	{ID: "counter", Name: "Counter", Description: "Increment and decrement counter"},
	{ID: "chat", Name: "Chat", Description: "Anonymous chat room"},
	{ID: "draw", Name: "Drawing Board", Description: "Collaborative drawing canvas"},
	{ID: "notes", Name: "Notes", Description: "Shared sticky notes"},
}

type openReq struct {
	napp Napp
	dir  string
}

var openReqCh = make(chan openReq)

var (
	mu       sync.Mutex
	children []webview.WebView
)

func main() {
	go gioMain()
	go webviewServer()
	app.Main()
}

func gioMain() {
	var nappList widget.List
	nappList.Axis = layout.Vertical

	installBtns := make([]widget.Clickable, len(napps))

	th := material.NewTheme()
	th.Shaper = text.NewShaper(text.WithCollection(gofont.Collection()))

	w := new(app.Window)
	w.Option(app.Title("Verdana"), app.Size(unit.Dp(520), unit.Dp(500)))

	var ops op.Ops
	for {
		switch e := w.Event(); e := e.(type) {
		case app.FrameEvent:
			gtx := app.NewContext(&ops, e)

			for i := range installBtns {
				if installBtns[i].Clicked(gtx) {
					napp := napps[i]
					dir, err := app.DataDir()
					if err == nil {
						appDir := filepath.Join(dir, "napps", napp.ID)
						os.MkdirAll(appDir, 0755)
						openReqCh <- openReq{napp: napp, dir: appDir}
					}
				}
			}

			layout.Inset{
				Top:    unit.Dp(16),
				Bottom: unit.Dp(16),
				Left:   unit.Dp(16),
				Right:  unit.Dp(16),
			}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return material.List(th, &nappList).Layout(gtx, len(napps), func(gtx layout.Context, i int) layout.Dimensions {
					return renderCard(gtx, th, &installBtns[i], napps[i])
				})
			})

			e.Frame(gtx.Ops)

		case app.DestroyEvent:
			return
		}
	}
}

func renderCard(gtx layout.Context, th *material.Theme, btn *widget.Clickable, napp Napp) layout.Dimensions {
	return layout.Inset{
		Bottom: unit.Dp(8),
	}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		sz := gtx.Constraints.Max
		defer clip.RRect{
			Rect: image.Rectangle{Max: sz},
			NW:   8, NE: 8, SW: 8, SE: 8,
		}.Push(gtx.Ops).Pop()
		paint.Fill(gtx.Ops, color.NRGBA{R: 0xf0, G: 0xf0, B: 0xf0, A: 0xff})

		return layout.Inset{
			Top: unit.Dp(12), Bottom: unit.Dp(12),
			Left: unit.Dp(16), Right: unit.Dp(16),
		}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							label := material.H6(th, napp.Name)
							label.Font.Weight = font.Bold
							return label.Layout(gtx)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							pointer.CursorPointer.Add(gtx.Ops)
							b := material.Button(th, btn, "Install")
							b.TextSize = unit.Sp(12)
							b.Inset = layout.UniformInset(unit.Dp(6))
							return b.Layout(gtx)
						}),
					)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						label := material.Body2(th, napp.Description)
						label.Color = color.NRGBA{R: 0x66, G: 0x66, B: 0x66, A: 0xff}
						return label.Layout(gtx)
					})
				}),
			)
		})
	})
}

func webviewServer() {
	runtime.LockOSThread()

	req := <-openReqCh

	master := webview.New(false)
	master.SetTitle(req.napp.Name)
	master.SetSize(600, 450, webview.HintNone)
	master.SetHtml(nappHTML(req.napp))

	mu.Lock()
	children = append(children, master)
	mu.Unlock()

	done := make(chan struct{})
	go func() {
		for {
			select {
			case req := <-openReqCh:
				master.Dispatch(func() {
					w := webview.New(false)
					w.SetTitle(req.napp.Name)
					w.SetSize(600, 450, webview.HintNone)
					w.SetHtml(nappHTML(req.napp))
					mu.Lock()
					children = append(children, w)
					mu.Unlock()
				})
			case <-done:
				return
			}
		}
	}()

	master.Run()
	close(done)

	mu.Lock()
	for _, c := range children {
		if c != master {
			c.Destroy()
		}
	}
	children = nil
	mu.Unlock()

	master.Destroy()
}

func nappHTML(napp Napp) string {
	return `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<style>
body { font-family: sans-serif; padding: 2em; background: #fafafa; }
h1 { color: #333; }
p { color: #666; line-height: 1.5; }
pre { background: #eee; padding: 1em; border-radius: 4px; }
</style>
</head>
<body>
<h1>` + napp.Name + `</h1>
<p>` + napp.Description + `</p>
<pre>App ID: ` + napp.ID + `</pre>
<p>Placeholder content. Real napp loading TBD.</p>
</body>
</html>`
}
