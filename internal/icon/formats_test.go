package icon

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/gif"
	"os"
	"testing"
)

// bmp24 builds a bottom-up 24-bit BMP, which is what most tools emit.
func bmp24(t *testing.T, side int) []byte {
	t.Helper()
	rowSize := (side*3 + 3) &^ 3
	pixelBytes := rowSize * side
	const fileHeaderSize, infoHeaderSize = 14, 40
	total := fileHeaderSize + infoHeaderSize + pixelBytes
	buf := make([]byte, total)
	copy(buf[0:2], "BM")
	binary.LittleEndian.PutUint32(buf[2:6], uint32(total))
	binary.LittleEndian.PutUint32(buf[10:14], fileHeaderSize+infoHeaderSize)
	binary.LittleEndian.PutUint32(buf[14:18], infoHeaderSize)
	binary.LittleEndian.PutUint32(buf[18:22], uint32(side))
	binary.LittleEndian.PutUint32(buf[22:26], uint32(side))
	binary.LittleEndian.PutUint16(buf[26:28], 1)
	binary.LittleEndian.PutUint16(buf[28:30], 24)
	binary.LittleEndian.PutUint32(buf[34:38], uint32(pixelBytes))
	for y := 0; y < side; y++ {
		row := buf[fileHeaderSize+infoHeaderSize+(side-1-y)*rowSize:]
		for x := 0; x < side; x++ {
			row[x*3+0] = byte(x)
			row[x*3+1] = byte(y)
			row[x*3+2] = 0x40
		}
	}
	return buf
}

func gifBytes(t *testing.T, side int) []byte {
	t.Helper()
	pal := color.Palette{color.NRGBA{0, 0, 0, 255}, color.NRGBA{0x28, 0x8a, 0xd8, 255}}
	img := image.NewPaletted(image.Rect(0, 0, side, side), pal)
	for y := 0; y < side; y++ {
		for x := 0; x < side; x++ {
			img.SetColorIndex(x, y, uint8((x/(side/2)+1)%2))
		}
	}
	var buf bytes.Buffer
	if err := gif.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestNormalizeConvertsGIF(t *testing.T) {
	set, err := Normalize(gifBytes(t, 128))
	if err != nil {
		t.Fatalf("gif: %v", err)
	}
	if len(set.Icon256) == 0 || len(set.Icon64) == 0 {
		t.Fatal("empty icon")
	}
}

func TestNormalizeConvertsBMP(t *testing.T) {
	set, err := Normalize(bmp24(t, 96))
	if err != nil {
		t.Fatalf("bmp: %v", err)
	}
	if len(set.Icon256) == 0 {
		t.Fatal("empty icon")
	}
}

func TestNormalizeConvertsWebP(t *testing.T) {
	data, err := os.ReadFile("testdata/sample.webp")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	set, err := Normalize(data)
	if err != nil {
		t.Fatalf("webp: %v", err)
	}
	if len(set.Icon256) == 0 {
		t.Fatal("empty icon")
	}
}

func TestNormalizeRejectsSVG(t *testing.T) {
	if _, err := Normalize([]byte(" <svg xmlns=\"http://www.w3.org/2000/svg\" width=\"128\" height=\"128\"></svg>")); err == nil {
		t.Fatal("expected an error for SVG input")
	}
}
