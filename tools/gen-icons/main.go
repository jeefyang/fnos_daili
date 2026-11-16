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

func render(size int) *image.RGBA {
	w := size * supersample
	hi := image.NewRGBA(image.Rect(0, 0, w, w))
	for py := 0; py < w; py++ {
		for px := 0; px < w; px++ {
			u := (float64(px) + 0.5) / float64(w)
			v := (float64(py) + 0.5) / float64(w)
			if !inRoundedSquare(u, v, 0.47, 0.20) {
				continue
			}
			c := background(u, v)
			if inArrowRight(u, v) {
				c = blend(c, color.RGBA{255, 255, 255, 255}, 0.96)
			}
			if inArrowLeft(u, v) {
				c = blend(c, color.RGBA{94, 234, 212, 255}, 0.96)
			}
			hi.SetRGBA(px, py, c)
		}
	}
	return downsample(hi, size)
}

func inRoundedSquare(u, v, half, radius float64) bool {
	x := math.Abs(u-0.5) - (half - radius)
	y := math.Abs(v-0.5) - (half - radius)
	if x <= 0 || y <= 0 {
		return math.Abs(u-0.5) <= half && math.Abs(v-0.5) <= half
	}
	return x*x+y*y <= radius*radius
}

func background(u, v float64) color.RGBA {
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
	return color.RGBA{uint8(r), uint8(g), uint8(b), 255}
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

func blend(dst, src color.RGBA, a float64) color.RGBA {
	return color.RGBA{
		uint8(float64(dst.R)*(1-a) + float64(src.R)*a),
		uint8(float64(dst.G)*(1-a) + float64(src.G)*a),
		uint8(float64(dst.B)*(1-a) + float64(src.B)*a),
		255,
	}
}

func lerp(a, b, t float64) float64 {
	return a + (b-a)*t
}

func downsample(src *image.RGBA, size int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var r, g, b uint32
			for dy := 0; dy < supersample; dy++ {
				for dx := 0; dx < supersample; dx++ {
					c := src.RGBAAt(x*supersample+dx, y*supersample+dy)
					r += uint32(c.R)
					g += uint32(c.G)
					b += uint32(c.B)
				}
			}
			n := uint32(supersample * supersample)
			dst.SetRGBA(x, y, color.RGBA{uint8(r / n), uint8(g / n), uint8(b / n), 255})
		}
	}
	return dst
}
