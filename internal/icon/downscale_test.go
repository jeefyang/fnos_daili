package icon

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// TestNormalizeDownscaleKeepsEdgeColour guards the premultiplied resampler: a
// large icon with a transparent border must not pick up the transparent
// pixels' black RGB, which used to leave a dark ring around the artwork.
func TestNormalizeDownscaleKeepsEdgeColour(t *testing.T) {
	const side = 512
	src := image.NewNRGBA(image.Rect(0, 0, side, side))
	for y := 0; y < side; y++ {
		for x := 0; x < side; x++ {
			dx, dy := float64(x)-side/2+0.5, float64(y)-side/2+0.5
			if dx*dx+dy*dy <= (side/2-8)*(side/2-8) {
				src.SetNRGBA(x, y, color.NRGBA{255, 255, 255, 255})
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, src); err != nil {
		t.Fatal(err)
	}
	set, err := Normalize(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(set.Icon256))
	if err != nil {
		t.Fatal(err)
	}
	nrgba := img.(*image.NRGBA)
	edge := 0
	for y := 0; y < 256; y++ {
		for x := 0; x < 256; x++ {
			c := nrgba.NRGBAAt(x, y)
			if c.A < 64 || c.A > 200 {
				continue
			}
			edge++
			if c.R < 200 {
				t.Fatalf("edge pixel (%d,%d) picked up dark RGB: %+v", x, y, c)
			}
		}
	}
	if edge == 0 {
		t.Fatal("expected a soft outline with partially transparent pixels")
	}
}
