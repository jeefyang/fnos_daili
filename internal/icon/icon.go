// Package icon normalizes an uploaded image into the two PNG sizes fnOS uses
// for application icons.
package icon

import (
	"bytes"
	"fmt"
	"image"
	stddraw "image/draw"
	_ "image/jpeg"
	"image/png"

	xdraw "golang.org/x/image/draw"
)

const (
	// MaxUploadBytes is the largest image the manager accepts from the browser.
	MaxUploadBytes = 2 << 20
	// MaxPixels rejects decompression bombs before decoding the full image.
	MaxPixels = 16 << 20
	// MinSide is the smallest source image; smaller images would only be blur.
	MinSide = 64
)

// Set is the pair of PNG icons embedded in an fpk.
type Set struct {
	Icon64  []byte
	Icon256 []byte
}

// Normalize center-crops the source to a square and produces 256px and 64px
// PNGs. It accepts PNG and JPEG, keeps alpha, and never upscales beyond 256px.
func Normalize(data []byte) (Set, error) {
	var zero Set
	if len(data) == 0 {
		return zero, fmt.Errorf("image is empty")
	}
	if len(data) > MaxUploadBytes {
		return zero, fmt.Errorf("image is larger than %d bytes", MaxUploadBytes)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return zero, fmt.Errorf("not a PNG or JPEG image: %w", err)
	}
	if format != "png" && format != "jpeg" {
		return zero, fmt.Errorf("unsupported image format %q; use PNG or JPEG", format)
	}
	if cfg.Width < MinSide || cfg.Height < MinSide {
		return zero, fmt.Errorf("image must be at least %dx%d, got %dx%d", MinSide, MinSide, cfg.Width, cfg.Height)
	}
	if int64(cfg.Width)*int64(cfg.Height) > MaxPixels {
		return zero, fmt.Errorf("image has too many pixels (max %d)", MaxPixels)
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return zero, fmt.Errorf("decode image: %w", err)
	}
	b := src.Bounds()
	side := b.Dx()
	if b.Dy() < side {
		side = b.Dy()
	}
	// Center-crop to a square so non-square uploads keep their middle.
	crop := image.NewNRGBA(image.Rect(0, 0, side, side))
	origin := image.Pt(b.Min.X+(b.Dx()-side)/2, b.Min.Y+(b.Dy()-side)/2)
	stddraw.Draw(crop, crop.Bounds(), src, origin, stddraw.Src)

	icon256 := image.NewNRGBA(image.Rect(0, 0, 256, 256))
	xdraw.CatmullRom.Scale(icon256, icon256.Bounds(), crop, crop.Bounds(), stddraw.Src, nil)
	icon64 := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	xdraw.CatmullRom.Scale(icon64, icon64.Bounds(), icon256, icon256.Bounds(), stddraw.Src, nil)

	out256, err := encodePNG(icon256)
	if err != nil {
		return zero, err
	}
	out64, err := encodePNG(icon64)
	if err != nil {
		return zero, err
	}
	return Set{Icon64: out64, Icon256: out256}, nil
}

func encodePNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	if err := enc.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("encode icon: %w", err)
	}
	return buf.Bytes(), nil
}
