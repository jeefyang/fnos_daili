package icon

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func icoEntry(w, h, bitCount int, offset, size int) []byte {
	e := make([]byte, 16)
	if w == 256 {
		e[0] = 0
	} else {
		e[0] = byte(w)
	}
	if h == 256 {
		e[1] = 0
	} else {
		e[1] = byte(h)
	}
	binary.LittleEndian.PutUint16(e[4:6], 1)
	binary.LittleEndian.PutUint16(e[6:8], uint16(bitCount))
	binary.LittleEndian.PutUint32(e[8:12], uint32(size))
	binary.LittleEndian.PutUint32(e[12:16], uint32(offset))
	return e
}

func icoHeader(count int) []byte {
	h := make([]byte, 6)
	binary.LittleEndian.PutUint16(h[2:4], 1)
	binary.LittleEndian.PutUint16(h[4:6], uint16(count))
	return h
}

// bmp32Payload builds a 32-bit BMP frame with the ICO doubled-height header
// and a trailing AND mask, which is what most .ico files in the wild look like.
func bmp32Payload(t *testing.T, w, h int, alpha byte) []byte {
	t.Helper()
	rowSize := ((32*w + 31) / 32) * 4
	maskRow := ((w + 31) / 32) * 4
	payload := make([]byte, 40+rowSize*h+maskRow*h)
	binary.LittleEndian.PutUint32(payload[0:4], 40)
	binary.LittleEndian.PutUint32(payload[4:8], uint32(w))
	binary.LittleEndian.PutUint32(payload[8:12], uint32(2*h))
	binary.LittleEndian.PutUint16(payload[12:14], 1)
	binary.LittleEndian.PutUint16(payload[14:16], 32)
	for y := 0; y < h; y++ {
		row := payload[40+(h-1-y)*rowSize:]
		for x := 0; x < w; x++ {
			p := row[x*4:]
			p[0] = byte(x * 3)
			p[1] = byte(y * 3)
			p[2] = 200
			p[3] = alpha
		}
	}
	return payload
}

func bmp32ICO(t *testing.T, w, h int, alpha byte) []byte {
	t.Helper()
	return wrapICO(t, []byte{byte(w % 256), byte(h % 256)}, bmp32Payload(t, w, h, alpha))
}

func pngICO(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 30, G: byte(x), B: byte(y), A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return wrapICO(t, []byte{byte(w % 256), byte(h % 256)}, buf.Bytes())
}

func wrapICO(t *testing.T, dims []byte, payload []byte) []byte {
	t.Helper()
	header := icoHeader(1)
	entry := icoEntry(int(dims[0]), int(dims[1]), 32, 6+16, len(payload))
	var buf bytes.Buffer
	buf.Write(header)
	buf.Write(entry)
	buf.Write(payload)
	return buf.Bytes()
}

func TestNormalizeFaviconUpscalesSmallPNG(t *testing.T) {
	set, err := NormalizeFavicon(testPNG(t, 32, 32))
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(set.Icon256))
	if err != nil {
		t.Fatal(err)
	}
	if got := img.Bounds().Dx(); got != 256 {
		t.Fatalf("width = %d, want 256", got)
	}
}

func TestNormalizeFaviconDecodesICO(t *testing.T) {
	set, err := NormalizeFavicon(bmp32ICO(t, 48, 48, 255))
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Icon64) == 0 || len(set.Icon256) == 0 {
		t.Fatal("empty icon set")
	}
}

func TestNormalizeFaviconDecodesPNGFrame(t *testing.T) {
	if _, err := NormalizeFavicon(pngICO(t, 256, 256)); err != nil {
		t.Fatal(err)
	}
}

func TestNormalizeFaviconPicksLargestFrame(t *testing.T) {
	small := bmp32Payload(t, 16, 16, 255)
	big := bmp32Payload(t, 128, 128, 255)
	header := icoHeader(2)
	entrySmall := icoEntry(16, 16, 32, 6+32, len(small))
	entryBig := icoEntry(128, 128, 32, 6+32+len(small), len(big))
	data := append(header, entrySmall...)
	data = append(data, entryBig...)
	data = append(data, small...)
	data = append(data, big...)
	img, err := decodeICO(data)
	if err != nil {
		t.Fatal(err)
	}
	if got := img.Bounds().Dx(); got != 128 {
		t.Fatalf("picked width %d, want 128", got)
	}
}

func TestDecodeICOTreatsZeroAlphaAsOpaque(t *testing.T) {
	img, err := decodeICO(bmp32ICO(t, 32, 32, 0))
	if err != nil {
		t.Fatal(err)
	}
	nrgba, ok := img.(*image.NRGBA)
	if !ok {
		t.Fatalf("decoded %T, want *image.NRGBA", img)
	}
	if a := nrgba.NRGBAAt(0, 0).A; a != 255 {
		t.Fatalf("alpha = %d, want 255", a)
	}
}

func TestNormalizeAcceptsICO(t *testing.T) {
	set, err := Normalize(bmp32ICO(t, 64, 64, 255))
	if err != nil {
		t.Fatalf("upload path rejected ICO: %v", err)
	}
	if len(set.Icon256) == 0 || len(set.Icon64) == 0 {
		t.Fatal("empty icon")
	}
}

func TestNormalizeAcceptsSmallICO(t *testing.T) {
	// A 32x32 .ico is common even for crisp artwork, so uploads use the same
	// 16px floor as fetched site icons.
	set, err := Normalize(bmp32ICO(t, 32, 32, 255))
	if err != nil {
		t.Fatalf("upload path rejected a 32x32 ICO: %v", err)
	}
	if len(set.Icon256) == 0 {
		t.Fatal("empty icon")
	}
}

func TestDecodeICORejectsGarbage(t *testing.T) {
	if _, err := decodeICO([]byte{0, 0, 1, 0, 1, 0}); err == nil {
		t.Fatal("truncated ICO accepted")
	}
	if _, err := decodeICO([]byte("nope")); err == nil {
		t.Fatal("non-ICO accepted")
	}
}

func icoWithDepth(t *testing.T, w, h, bitCount int, payload []byte) []byte {
	t.Helper()
	header := icoHeader(1)
	entry := icoEntry(w, h, bitCount, 6+16, len(payload))
	var buf bytes.Buffer
	buf.Write(header)
	buf.Write(entry)
	buf.Write(payload)
	return buf.Bytes()
}

// bmp24Payload builds the older 24-bit frame that keeps all its transparency
// in the AND mask; maskCols left-hand columns are marked transparent.
func bmp24Payload(t *testing.T, w, h, maskCols int) []byte {
	t.Helper()
	rowSize := ((24*w + 31) / 32) * 4
	maskRow := ((w + 31) / 32) * 4
	payload := make([]byte, 40+rowSize*h+maskRow*h)
	binary.LittleEndian.PutUint32(payload[0:4], 40)
	binary.LittleEndian.PutUint32(payload[4:8], uint32(w))
	binary.LittleEndian.PutUint32(payload[8:12], uint32(2*h))
	binary.LittleEndian.PutUint16(payload[12:14], 1)
	binary.LittleEndian.PutUint16(payload[14:16], 24)
	for y := 0; y < h; y++ {
		row := payload[40+(h-1-y)*rowSize:]
		for x := 0; x < w; x++ {
			p := row[x*3:]
			p[0] = byte(x * 5)
			p[1] = byte(y * 5)
			p[2] = 90
		}
		if maskCols > 0 {
			mrow := payload[40+rowSize*h+(h-1-y)*maskRow:]
			for x := 0; x < maskCols && x < w; x++ {
				mrow[x/8] |= 1 << uint(7-x%8)
			}
		}
	}
	return payload
}

func TestDecodeICOAppliesANDMask(t *testing.T) {
	img, err := decodeICO(icoWithDepth(t, 32, 32, 24, bmp24Payload(t, 32, 32, 8)))
	if err != nil {
		t.Fatal(err)
	}
	nrgba := img.(*image.NRGBA)
	if a := nrgba.NRGBAAt(0, 16).A; a != 0 {
		t.Fatalf("masked pixel alpha = %d, want 0", a)
	}
	if a := nrgba.NRGBAAt(31, 16).A; a != 255 {
		t.Fatalf("unmasked pixel alpha = %d, want 255", a)
	}
}

func TestDecodeICOZeroAlphaFallsBackToMask(t *testing.T) {
	payload := bmp32Payload(t, 32, 32, 0)
	rowSize := ((32*32 + 31) / 32) * 4
	maskRow := ((32 + 31) / 32) * 4
	for y := 0; y < 32; y++ {
		mrow := payload[40+rowSize*32+(31-y)*maskRow:]
		mrow[0] = 0x80 // x = 0 transparent
	}
	img, err := decodeICO(icoWithDepth(t, 32, 32, 32, payload))
	if err != nil {
		t.Fatal(err)
	}
	nrgba := img.(*image.NRGBA)
	if a := nrgba.NRGBAAt(0, 10).A; a != 0 {
		t.Fatalf("masked pixel alpha = %d, want 0", a)
	}
	if a := nrgba.NRGBAAt(10, 10).A; a != 255 {
		t.Fatalf("unmasked pixel alpha = %d, want 255", a)
	}
}

func TestDecodeICOAppliesMaskOverOpaqueAlpha(t *testing.T) {
	// Some encoders fill the 32-bit alpha channel with 255 and mark the
	// transparency only in the AND mask; the mask still wins.
	payload := bmp32Payload(t, 32, 32, 255)
	rowSize := ((32*32 + 31) / 32) * 4
	maskRow := ((32 + 31) / 32) * 4
	for y := 0; y < 32; y++ {
		mrow := payload[40+rowSize*32+(31-y)*maskRow:]
		mrow[0] = 0x80 // x = 0 transparent
	}
	img, err := decodeICO(icoWithDepth(t, 32, 32, 32, payload))
	if err != nil {
		t.Fatal(err)
	}
	nrgba := img.(*image.NRGBA)
	if a := nrgba.NRGBAAt(0, 10).A; a != 0 {
		t.Fatalf("masked pixel alpha = %d, want 0", a)
	}
	if a := nrgba.NRGBAAt(10, 10).A; a != 255 {
		t.Fatalf("unmasked pixel alpha = %d, want 255", a)
	}
}

func TestNormalizeFaviconKeepsMaskedCornersTransparent(t *testing.T) {
	payload := bmp32Payload(t, 64, 64, 255)
	rowSize := ((32*64 + 31) / 32) * 4
	maskRow := ((64 + 31) / 32) * 4
	for y := 0; y < 8; y++ {
		mrow := payload[40+rowSize*64+(63-y)*maskRow:]
		for x := 0; x < 8; x++ {
			mrow[x/8] |= 0x80 >> uint(x%8)
		}
	}
	set, err := NormalizeFavicon(icoWithDepth(t, 64, 64, 32, payload))
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(set.Icon256))
	if err != nil {
		t.Fatal(err)
	}
	if a := img.(*image.NRGBA).NRGBAAt(0, 0).A; a != 0 {
		t.Fatalf("masked corner alpha = %d, want 0", a)
	}
	if a := img.(*image.NRGBA).NRGBAAt(128, 128).A; a != 255 {
		t.Fatalf("centre alpha = %d, want 255", a)
	}
}

func TestNormalizeFaviconKeepsTransparentEdges(t *testing.T) {
	set, err := NormalizeFavicon(icoWithDepth(t, 64, 64, 24, bmp24Payload(t, 64, 64, 32)))
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(set.Icon256))
	if err != nil {
		t.Fatal(err)
	}
	if a := img.(*image.NRGBA).NRGBAAt(0, 128).A; a != 0 {
		t.Fatalf("transparent edge alpha = %d, want 0", a)
	}
}
