package icon

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"sort"
)

// icoMaxSide bounds a single ICO frame; real icons top out at 256.
const icoMaxSide = 1024

type icoFrame struct {
	width    int
	height   int
	bitCount int
	offset   int
	size     int
}

func isICO(data []byte) bool {
	return len(data) >= 6 && data[0] == 0 && data[1] == 0 && data[2] == 1 && data[3] == 0
}

func decodeICO(data []byte) (image.Image, error) {
	frames, err := icoFrames(data)
	if err != nil {
		return nil, err
	}
	var firstErr error
	for _, f := range frames {
		img, err := decodeICOFrame(data, f)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		return img, nil
	}
	if firstErr != nil {
		return nil, firstErr
	}
	return nil, fmt.Errorf("ICO has no readable frames")
}

// icoFrames parses the directory and orders frames by area, then bit depth, so
// the largest available rendition wins.
func icoFrames(data []byte) ([]icoFrame, error) {
	if !isICO(data) {
		return nil, fmt.Errorf("not an ICO file")
	}
	count := int(binary.LittleEndian.Uint16(data[4:6]))
	if count == 0 {
		return nil, fmt.Errorf("ICO has no frames")
	}
	if len(data) < 6+16*count {
		return nil, fmt.Errorf("ICO directory is truncated")
	}
	frames := make([]icoFrame, 0, count)
	for i := 0; i < count; i++ {
		entry := data[6+16*i : 6+16*(i+1)]
		w, h := int(entry[0]), int(entry[1])
		if w == 0 {
			w = 256
		}
		if h == 0 {
			h = 256
		}
		bitCount := int(binary.LittleEndian.Uint16(entry[6:8]))
		size := int(binary.LittleEndian.Uint32(entry[8:12]))
		offset := int(binary.LittleEndian.Uint32(entry[12:16]))
		if w > icoMaxSide || h > icoMaxSide || w <= 0 || h <= 0 {
			continue
		}
		if offset < 0 || size <= 0 || offset+size > len(data) {
			continue
		}
		if w*h > MaxPixels {
			continue
		}
		frames = append(frames, icoFrame{width: w, height: h, bitCount: bitCount, offset: offset, size: size})
	}
	if len(frames) == 0 {
		return nil, fmt.Errorf("ICO has no usable frames")
	}
	sort.SliceStable(frames, func(i, j int) bool {
		ai, aj := frames[i].width*frames[i].height, frames[j].width*frames[j].height
		if ai != aj {
			return ai > aj
		}
		return frames[i].bitCount > frames[j].bitCount
	})
	return frames, nil
}

func decodeICOFrame(data []byte, f icoFrame) (image.Image, error) {
	payload := data[f.offset : f.offset+f.size]
	if len(payload) >= 8 && bytes.Equal(payload[:8], []byte("\x89PNG\r\n\x1a\n")) {
		img, err := png.Decode(bytes.NewReader(payload))
		if err != nil {
			return nil, fmt.Errorf("ICO PNG frame: %w", err)
		}
		return img, nil
	}
	return decodeICOBitmap(payload, f)
}

// decodeICOBitmap decodes the uncompressed BMP variant used inside ICO files:
// a BITMAPINFOHEADER whose height covers the XOR image plus the AND mask.
func decodeICOBitmap(payload []byte, f icoFrame) (image.Image, error) {
	if len(payload) < 40 {
		return nil, fmt.Errorf("ICO bitmap header is truncated")
	}
	headerSize := int(binary.LittleEndian.Uint32(payload[0:4]))
	if headerSize < 40 || headerSize > len(payload) {
		return nil, fmt.Errorf("unsupported ICO bitmap header size %d", headerSize)
	}
	width := int(int32(binary.LittleEndian.Uint32(payload[4:8])))
	height := int(int32(binary.LittleEndian.Uint32(payload[8:12]))) / 2
	planes := binary.LittleEndian.Uint16(payload[12:14])
	bitCount := int(binary.LittleEndian.Uint16(payload[14:16]))
	compression := binary.LittleEndian.Uint32(payload[16:20])
	clrUsed := int(binary.LittleEndian.Uint32(payload[32:36]))
	if width <= 0 || height <= 0 || width > icoMaxSide || height > icoMaxSide {
		return nil, fmt.Errorf("invalid ICO bitmap size %dx%d", width, height)
	}
	if planes != 1 {
		return nil, fmt.Errorf("invalid ICO bitmap plane count %d", planes)
	}
	if compression != 0 && compression != 3 {
		return nil, fmt.Errorf("unsupported ICO bitmap compression %d", compression)
	}
	offset := headerSize
	if compression == 3 {
		offset += 12
	}
	var palette []color.NRGBA
	if bitCount <= 8 {
		count := clrUsed
		if count == 0 {
			count = 1 << uint(bitCount)
		}
		if offset+count*4 > len(payload) {
			return nil, fmt.Errorf("ICO palette is truncated")
		}
		palette = make([]color.NRGBA, count)
		for i := 0; i < count; i++ {
			p := payload[offset+i*4:]
			palette[i] = color.NRGBA{R: p[2], G: p[1], B: p[0], A: 255}
		}
		offset += count * 4
	}
	if bitCount != 1 && bitCount != 4 && bitCount != 8 && bitCount != 24 && bitCount != 32 {
		return nil, fmt.Errorf("unsupported ICO bitmap depth %d", bitCount)
	}
	rowSize := ((bitCount*width + 31) / 32) * 4
	if offset+rowSize*height > len(payload) {
		return nil, fmt.Errorf("ICO bitmap pixels are truncated")
	}
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	opaque := true
	for y := 0; y < height; y++ {
		row := payload[offset+(height-1-y)*rowSize:]
		for x := 0; x < width; x++ {
			var c color.NRGBA
			switch bitCount {
			case 32:
				p := row[x*4:]
				c = color.NRGBA{R: p[2], G: p[1], B: p[0], A: p[3]}
				if p[3] != 0 {
					opaque = false
				}
			case 24:
				p := row[x*3:]
				c = color.NRGBA{R: p[2], G: p[1], B: p[0], A: 255}
			case 8:
				idx := int(row[x])
				c = paletteAt(palette, idx)
			case 4:
				b := row[x/2]
				idx := int(b >> 4)
				if x%2 == 1 {
					idx = int(b & 0x0f)
				}
				c = paletteAt(palette, idx)
			case 1:
				b := row[x/8]
				idx := int(b>>(7-uint(x%8))) & 1
				c = paletteAt(palette, idx)
			}
			img.SetNRGBA(x, y, c)
		}
	}
	if bitCount == 32 && opaque {
		// Some editors leave the alpha channel at zero; treat that as opaque.
		for i := 3; i < len(img.Pix); i += 4 {
			img.Pix[i] = 255
		}
	}
	return img, nil
}

func paletteAt(palette []color.NRGBA, idx int) color.NRGBA {
	if idx < 0 || idx >= len(palette) {
		return color.NRGBA{A: 255}
	}
	return palette[idx]
}
