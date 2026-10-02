package backend

import (
	"bytes"
	"image"
	"image/color"
	"image/png"

	"rsc.io/qr"
)

// qrQuietZone is the blank border, in modules, a scanner needs around a code.
const qrQuietZone = 4

// QRCode renders text as a QR code, one pixel per module with a white quiet
// zone around it: scale it up with nearest-neighbour filtering to draw it.
//
// (qr.Code.Image would do, but it draws the code unscaled in a corner of an
// image sized for the scaled code and its quiet zone.)
func QRCode(text string) (*image.Gray, error) {
	code, err := qr.Encode(text, qr.M)
	if err != nil {
		return nil, err
	}
	n := code.Size + 2*qrQuietZone
	img := image.NewGray(image.Rect(0, 0, n, n))
	for i := range img.Pix {
		img.Pix[i] = 0xff
	}
	for y := 0; y < code.Size; y++ {
		for x := 0; x < code.Size; x++ {
			if code.Black(x, y) {
				img.SetGray(x+qrQuietZone, y+qrQuietZone, color.Gray{})
			}
		}
	}
	return img, nil
}

// QRCodePNG is QRCode as a PNG with every module scale pixels wide, for a UI
// that wants an image file.
func QRCodePNG(text string, scale int) ([]byte, error) {
	small, err := QRCode(text)
	if err != nil {
		return nil, err
	}
	scale = max(scale, 1)
	n := small.Rect.Dx()
	img := image.NewGray(image.Rect(0, 0, n*scale, n*scale))
	for y := 0; y < n*scale; y++ {
		for x := 0; x < n*scale; x++ {
			img.Pix[y*img.Stride+x] = small.Pix[(y/scale)*small.Stride+x/scale]
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
