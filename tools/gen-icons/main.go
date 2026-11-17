// Command gen-icons generates the default proxy application icons.
//
// Run from the repository root:
//
//	go run ./tools/gen-icons
package main

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
)

const supersample = 4

func main() {
	outDir := filepath.Join("internal", "assets", "icons")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fail(err)
	}
	for _, size := range []int{64, 256} {
		img := render(size)
		name := filepath.Join(outDir, fmt.Sprintf("icon-%d.png", size))
		f, err := os.Create(name)
		if err != nil {
			fail(err)
		}
		enc := png.Encoder{CompressionLevel: png.BestCompression}
		if err := enc.Encode(f, img); err != nil {
			f.Close()
			fail(err)
		}
		if err := f.Close(); err != nil {
			fail(err)
		}
		fmt.Println(name)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "gen-icons:", err)
	os.Exit(1)
}

// render draws the icon at supersample scale and averages it down. The
// artwork fills the whole square: rounding is a presentation concern, so the
// desktop and the manager preview apply it with CSS while the fpk ships a plain
// square PNG that carries no transparent or black margin of its own.
func render(size int) *image.NRGBA {
	w := size * supersample
	hi := image.NewNRGBA(image.Rect(0, 0, w, w))
	for py := 0; py < w; py++ {
		for px := 0; px < w; px++ {
			u := (float64(px) + 0.5) / float64(w)
			v := (float64(py) + 0.5) / float64(w)
			c := background(u, v)
			if inArrowRight(u, v) {
				c = blend(c, color.NRGBA{255, 255, 255, 255}, 0.96)
			}
			if inArrowLeft(u, v) {
				c = blend(c, color.NRGBA{94, 234, 212, 255}, 0.96)
			}
			hi.SetNRGBA(px, py, c)
		}
	}
	return downsample(hi, size)
}

func background(u, v float64) color.NRGBA {
	t := (u + v) / 2
	r := lerp(10, 20, t)
	g := lerp(28, 76, t)
	b := lerp(54, 116, t)
	// A soft highlight in the upper-left keeps the icon from looking flat.
	d := math.Hypot(u-0.28, v-0.22)
	if d < 0.55 {
		k := (0.55 - d) / 0.55 * 0.18
		r += (255 - r) * k * 0.20
		g += (255 - g) * k * 0.20
		b += (255 - b) * k * 0.20
	}
	return color.NRGBA{uint8(r), uint8(g), uint8(b), 255}
}

func inArrowRight(u, v float64) bool {
	if u >= 0.22 && u <= 0.69 && math.Abs(v-0.39) <= 0.055 {
		return true
	}
	return u >= 0.61 && u <= 0.82 && math.Abs(v-0.39) <= (0.82-u)*0.86
}

func inArrowLeft(u, v float64) bool {
	if u >= 0.31 && u <= 0.78 && math.Abs(v-0.61) <= 0.055 {
		return true
	}
	return u >= 0.18 && u <= 0.39 && math.Abs(v-0.61) <= (u-0.18)*0.86
}

func blend(dst, src color.NRGBA, a float64) color.NRGBA {
	return color.NRGBA{
		uint8(float64(dst.R)*(1-a) + float64(src.R)*a),
		uint8(float64(dst.G)*(1-a) + float64(src.G)*a),
		uint8(float64(dst.B)*(1-a) + float64(src.B)*a),
		255,
	}
}

func lerp(a, b, t float64) float64 {
	return a + (b-a)*t
}

// downsample averages the supersampled canvas so the outlined arrows keep a
// soft edge instead of a staircase.
func downsample(src *image.NRGBA, size int) *image.NRGBA {
	dst := image.NewNRGBA(image.Rect(0, 0, size, size))
	n := uint64(supersample * supersample)
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var sa, sr, sg, sb uint64
			for dy := 0; dy < supersample; dy++ {
				for dx := 0; dx < supersample; dx++ {
					i := src.PixOffset(x*supersample+dx, y*supersample+dy)
					a := uint64(src.Pix[i+3])
					sa += a
					sr += uint64(src.Pix[i]) * a
					sg += uint64(src.Pix[i+1]) * a
					sb += uint64(src.Pix[i+2]) * a
				}
			}
			if sa == 0 {
				continue
			}
			dst.SetNRGBA(x, y, color.NRGBA{
				R: uint8(sr / sa),
				G: uint8(sg / sa),
				B: uint8(sb / sa),
				A: uint8(sa / n),
			})
		}
	}
	return dst
}
