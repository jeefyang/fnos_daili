package icon

import "image"

// mitchell is the Mitchell-Netravali cubic (B=C=1/3). Unlike CatmullRom it
// never overshoots, so upscaling a small site icon leaves no bright halo
// along the outline, and its two-pixel support turns the source pixel grid
// into a smooth ramp instead of hard stair steps.
type mitchell struct{}

const mitchellSupport = 2

func (mitchell) at(x float64) float64 {
	const b, c = 1.0 / 3.0, 1.0 / 3.0
	if x < 0 {
		x = -x
	}
	switch {
	case x < 1:
		return ((12-9*b-6*c)*x*x*x + (-18+12*b+6*c)*x*x + (6 - 2*b)) / 6
	case x < 2:
		return ((-b-6*c)*x*x*x + (6*b+30*c)*x*x + (-12*b-48*c)*x + (8*b + 24*c)) / 6
	}
	return 0
}

// resample converts src to w x h with the Mitchell kernel and works in
// premultiplied float RGBA, so a transparent border contributes no colour and
// no dark ring appears along the outline on either up- or downscaling.
func resample(src *image.NRGBA, w, h int) *image.NRGBA {
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	if w <= 0 || h <= 0 || sw <= 0 || sh <= 0 {
		return nil
	}

	// Work in premultiplied float RGBA so transparent source pixels do not
	// bleed their colour into the result.
	tmp := make([]float64, w*sh*4)
	rowWeights := make([][]float64, w)
	rowIndex := make([][]int, w)
	for x := 0; x < w; x++ {
		rowIndex[x], rowWeights[x] = sampleWeights(x, w, sw)
		for y := 0; y < sh; y++ {
			for _, ch := range []int{0, 1, 2, 3} {
				var sum float64
				for i, idx := range rowIndex[x] {
					v := float64(src.Pix[(y*sw+idx)*4+ch])
					if ch != 3 {
						v *= float64(src.Pix[(y*sw+idx)*4+3]) / 255
					}
					sum += v * rowWeights[x][i]
				}
				tmp[(y*w+x)*4+ch] = sum
			}
		}
	}

	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	colWeights := make([][]float64, h)
	colIndex := make([][]int, h)
	for y := 0; y < h; y++ {
		colIndex[y], colWeights[y] = sampleWeights(y, h, sh)
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var r, g, b, a float64
			for i, idx := range colIndex[y] {
				wgt := colWeights[y][i]
				r += tmp[(idx*w+x)*4+0] * wgt
				g += tmp[(idx*w+x)*4+1] * wgt
				b += tmp[(idx*w+x)*4+2] * wgt
				a += tmp[(idx*w+x)*4+3] * wgt
			}
			if a < 0 {
				a = 0
			}
			if a > 255 {
				a = 255
			}
			o := (y*w + x) * 4
			if a <= 0 {
				dst.Pix[o+0], dst.Pix[o+1], dst.Pix[o+2], dst.Pix[o+3] = 0, 0, 0, 0
				continue
			}
			scale := 255 / a
			dst.Pix[o+0] = clampByte(r * scale)
			dst.Pix[o+1] = clampByte(g * scale)
			dst.Pix[o+2] = clampByte(b * scale)
			dst.Pix[o+3] = clampByte(a)
		}
	}
	return dst
}

// sampleWeights returns the source indices and normalised kernel weights that
// produce destination sample i.
func sampleWeights(i, dstN, srcN int) ([]int, []float64) {
	center := (float64(i)+0.5)*float64(srcN)/float64(dstN) - 0.5
	left := int(center - mitchellSupport + 0.5)
	right := int(center+mitchellSupport-0.5) + 1
	if left < 0 {
		left = 0
	}
	if right > srcN {
		right = srcN
	}
	idx := make([]int, 0, right-left)
	w := make([]float64, 0, right-left)
	var sum float64
	var k mitchell
	for s := left; s < right; s++ {
		weight := k.at(center - float64(s))
		if weight == 0 {
			continue
		}
		idx = append(idx, s)
		w = append(w, weight)
		sum += weight
	}
	if sum == 0 {
		nearest := int(center + 0.5)
		if nearest < 0 {
			nearest = 0
		}
		if nearest >= srcN {
			nearest = srcN - 1
		}
		return []int{nearest}, []float64{1}
	}
	for n := range w {
		w[n] /= sum
	}
	return idx, w
}

func clampByte(v float64) uint8 {
	if v <= 0 {
		return 0
	}
	if v >= 255 {
		return 255
	}
	return uint8(v + 0.5)
}
