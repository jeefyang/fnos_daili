package icon

import (
	"image"
	"image/color"
	"testing"
)

func solidNRGBA(w, h int, c color.NRGBA) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, c)
		}
	}
	return img
}

func TestResampleUpscaleKeepsFlatColour(t *testing.T) {
	want := color.NRGBA{R: 0x2a, G: 0x7f, B: 0xc4, A: 0xff}
	out := resample(solidNRGBA(4, 4, want), 64, 64)
	if out == nil {
		t.Fatal("resample returned nil")
	}
	if got := out.Bounds().Dx(); got != 64 {
		t.Fatalf("width = %d, want 64", got)
	}
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			r, g, b, a := out.At(x, y).RGBA()
			if uint8(r>>8) != want.R || uint8(g>>8) != want.G || uint8(b>>8) != want.B || uint8(a>>8) != want.A {
				t.Fatalf("pixel (%d,%d) = %v, want %v", x, y, out.At(x, y), want)
			}
		}
	}
}

func TestResampleUpscaleDoesNotOvershoot(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			c := color.NRGBA{A: 255}
			if x >= 4 {
				c = color.NRGBA{R: 255, A: 255}
			}
			src.SetNRGBA(x, y, c)
		}
	}
	out := resample(src, 128, 128)
	if out == nil {
		t.Fatal("resample returned nil")
	}
	for y := 0; y < 128; y++ {
		for x := 0; x < 128; x++ {
			c := out.NRGBAAt(x, y)
			if c.A != 255 {
				t.Fatalf("alpha at (%d,%d) = %d, want 255", x, y, c.A)
			}
			if c.R < 0 || c.G != 0 || c.B != 0 {
				t.Fatalf("green/blue at (%d,%d) = %d/%d, want 0", x, y, c.G, c.B)
			}
		}
	}
}

func TestResampleRejectsEmptyTarget(t *testing.T) {
	if out := resample(solidNRGBA(8, 8, color.NRGBA{A: 255}), 0, 4); out != nil {
		t.Fatal("resample should refuse an empty target")
	}
}
