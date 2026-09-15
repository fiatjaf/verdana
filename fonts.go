package main

import (
	_ "embed"

	"gioui.org/font"
	"gioui.org/font/gofont"
	"gioui.org/font/opentype"
	"gioui.org/text"
)

var (
	//go:embed assets/v.TTF
	vfontTTF []byte
	//go:embed assets/vb.ttf
	vfontBoldTTF []byte
	//go:embed assets/vi.ttf
	vfontItalicTTF []byte
)

// fontCollection parses the embedded font files and registers them
func fontCollection() []text.FontFace {
	coll := gofont.Collection()
	add := func(src []byte, f font.Font) bool {
		face, err := opentype.Parse(src)
		if err != nil {
			log.Warn().Err(err).Msg("failed to parse font file")
			return false
		}
		coll = append(coll, text.FontFace{Face: face, Font: f})
		return true
	}

	add(vfontTTF, font.Font{Typeface: "vFont"})
	add(vfontBoldTTF, font.Font{Typeface: "vFont", Weight: font.Bold})
	add(vfontItalicTTF, font.Font{Typeface: "vFont", Style: font.Italic})
	return coll
}
