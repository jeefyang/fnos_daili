package icon

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func testPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x), G: uint8(y), B: 120, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestNormalizePNG(t *testing.T) {
	set, err := Normalize(testPNG(t, 300, 200))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		data []byte
		want int
	}{{"64", set.Icon64, 64}, {"256", set.Icon256, 256}} {
		img, err := png.Decode(bytes.NewReader(tc.data))
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got := img.Bounds().Dx(); got != tc.want {
			t.Fatalf("%s width = %d, want %d", tc.name, got, tc.want)
		}
		if got := img.Bounds().Dy(); got != tc.want {
			t.Fatalf("%s height = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestNormalizeJPEG(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 128, 128))
	for y := 0; y < 128; y++ {
		for x := 0; x < 128; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 2), G: uint8(y * 2), B: 90, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := Normalize(buf.Bytes()); err != nil {
		t.Fatal(err)
	}
}

func TestNormalizeRejects(t *testing.T) {
	if _, err := Normalize(nil); err == nil {
		t.Fatal("empty image accepted")
	}
	if _, err := Normalize([]byte("not an image")); err == nil {
		t.Fatal("text accepted")
	}
	if _, err := Normalize(testPNG(t, 32, 32)); err == nil {
		t.Fatal("small image accepted")
	}
	if _, err := Normalize(bytes.Repeat([]byte{0}, MaxUploadBytes+1)); err == nil {
		t.Fatal("oversized image accepted")
	}
}
