// Package icon normalizes an uploaded image into the two PNG sizes fnOS uses
// for application icons.
package icon

import (
	"bytes"
	"fmt"
	"image"
	stddraw "image/draw"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"

	_ "golang.org/x/image/bmp"
	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const (
	// MaxUploadBytes is the largest image the manager accepts from the browser.
	MaxUploadBytes = 2 << 20
	// MaxPixels rejects decompression bombs before decoding the full image.
	MaxPixels = 16 << 20
	// MinSide is the smallest source image; smaller images would only be blur.
	MinSide = 64
	// MinFaviconSide is the smallest site icon we accept from a fetch; site
	// icons are often 32x32, so they are upscaled instead of rejected.
	MinFaviconSide = 16
)

// Set is the pair of PNG icons embedded in an fpk.
type Set struct {
	Icon64  []byte
	Icon256 []byte
}

// Normalize center-crops the source to a square and produces 256px and 64px
// PNGs. It accepts PNG, JPEG, GIF, BMP, WebP and ICO, converts everything to
// PNG, keeps alpha, and never upscales beyond 256px.
func Normalize(data []byte) (Set, error) {
	return normalize(data, MinSide, true)
}

// NormalizeFavicon is Normalize for a site icon fetched from a target: it also
// accepts ICO and allows sources down to 16x16, upscaling them.
func NormalizeFavicon(data []byte) (Set, error) {
	return normalize(data, MinFaviconSide, true)
}

func normalize(data []byte, minSide int, allowICO bool) (Set, error) {
	var zero Set
	if len(data) == 0 {
		return zero, fmt.Errorf("image is empty")
	}
	if len(data) > MaxUploadBytes {
		return zero, fmt.Errorf("image is larger than %d bytes", MaxUploadBytes)
	}
	src, width, height, err := decodeImage(data, allowICO)
	if err != nil {
		return zero, err
	}
	// ICO frames are routinely 32x32 even when the artwork is fine, so an
	// uploaded .ico is held to the same floor as a fetched site icon.
	if allowICO && isICO(data) {
		minSide = MinFaviconSide
	}
	if width < minSide || height < minSide {
		return zero, fmt.Errorf("image must be at least %dx%d, got %dx%d", minSide, minSide, width, height)
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

	// Downscales keep bicubic detail. Upscales (16/32px site icons) go through
	// Mitchell: CatmullRom overshoots into bright squares along the outline,
	// and bilinear keeps the source pixel grid as hard stair steps.
	var icon256 *image.NRGBA
	if side < 256 {
		icon256 = resampleUpscale(crop, 256, 256)
	} else {
		icon256 = image.NewNRGBA(image.Rect(0, 0, 256, 256))
		xdraw.CatmullRom.Scale(icon256, icon256.Bounds(), crop, crop.Bounds(), stddraw.Src, nil)
	}
	snapAlpha(icon256)
	icon64 := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	xdraw.CatmullRom.Scale(icon64, icon64.Bounds(), icon256, icon256.Bounds(), stddraw.Src, nil)
	snapAlpha(icon64)

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

// snapAlpha pulls near-transparent and near-opaque pixels to the extremes.
// Upscaling a small favicon leaves a fringe of alpha 1..7 pixels that reads as
// a grey halo on the fnOS desktop; the RGB under those pixels is meaningless,
// so dropping them sharpens the outline without touching the artwork.
func snapAlpha(img *image.NRGBA) {
	for i := 3; i < len(img.Pix); i += 4 {
		switch a := img.Pix[i]; {
		case a < 8:
			img.Pix[i] = 0
		case a > 247:
			img.Pix[i] = 255
		}
	}
}

func decodeImage(data []byte, allowICO bool) (image.Image, int, int, error) {
	if allowICO && isICO(data) {
		img, err := decodeICO(data)
		if err != nil {
			return nil, 0, 0, err
		}
		b := img.Bounds()
		return img, b.Dx(), b.Dy(), nil
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		if hint := formatHint(data); hint != "" {
			return nil, 0, 0, fmt.Errorf("not a PNG, JPEG, GIF, BMP, WebP, or ICO image: detected %s", hint)
		}
		return nil, 0, 0, fmt.Errorf("not a PNG, JPEG, GIF, BMP, WebP, or ICO image: %w", err)
	}
	if !supportedFormat(format) {
		return nil, 0, 0, fmt.Errorf("unsupported image format %q; use PNG, JPEG, GIF, BMP, WebP, or ICO", format)
	}
	if int64(cfg.Width)*int64(cfg.Height) > MaxPixels {
		return nil, 0, 0, fmt.Errorf("image has too many pixels (max %d)", MaxPixels)
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, 0, 0, fmt.Errorf("decode image: %w", err)
	}
	return src, cfg.Width, cfg.Height, nil
}

// supportedFormat lists the still-image formats the standard library and
// golang.org/x/image register at init; every one of them is converted to PNG.
func supportedFormat(format string) bool {
	switch format {
	case "png", "jpeg", "gif", "bmp", "webp":
		return true
	}
	return false
}

// formatHint names the container we saw when no decoder claimed the bytes, so
// the UI can say "this is an SVG/AVIF" instead of a bare unknown-format error.
func formatHint(data []byte) string {
	switch {
	case isICO(data):
		return "ICO"
	case len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return "WebP"
	case bytes.HasPrefix(bytes.TrimSpace(data), []byte("<svg")), bytes.Contains(data[:min(len(data), 512)], []byte("<svg")):
		return "SVG (unsupported)"
	case len(data) >= 12 && string(data[4:8]) == "ftyp":
		return "AVIF/HEIC (unsupported)"
	}
	return ""
}

func encodePNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	if err := enc.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("encode icon: %w", err)
	}
	return buf.Bytes(), nil
}
