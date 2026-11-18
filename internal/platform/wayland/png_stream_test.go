package wayland

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/png"
	"testing"
)

func TestNativePNGStreamReadsSeparateFrames(t *testing.T) {
	var encoded bytes.Buffer
	for _, rect := range []image.Rectangle{image.Rect(0, 0, 64, 40), image.Rect(0, 0, 40, 64)} {
		if err := png.Encode(&encoded, image.NewRGBA(rect)); err != nil {
			t.Fatal(err)
		}
	}
	for _, expected := range []image.Rectangle{image.Rect(0, 0, 64, 40), image.Rect(0, 0, 40, 64)} {
		im, err := readStreamPNG(&encoded)
		if err != nil || im.Bounds() != expected {
			t.Fatal(im, err)
		}
	}
	if encoded.Len() != 0 {
		t.Fatal("frame reader consumed wrong boundary")
	}
}

func TestNativePNGStreamRejectsCorruptAndOversizedFramesBeforeDecode(t *testing.T) {
	var good bytes.Buffer
	if err := png.Encode(&good, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	oversized := append([]byte(nil), good.Bytes()[:33]...)
	binary.BigEndian.PutUint32(oversized[16:20], 32000)
	binary.BigEndian.PutUint32(oversized[20:24], 32000)
	binary.BigEndian.PutUint32(oversized[29:33], crc32.ChecksumIEEE(oversized[12:29]))
	hugeChunk := append([]byte(nil), good.Bytes()[:8]...)
	hugeChunk = append(hugeChunk, 255, 255, 255, 255, 'I', 'H', 'D', 'R')
	for _, data := range [][]byte{[]byte("console log instead of pixels"), good.Bytes()[:12], oversized, hugeChunk} {
		if _, err := readStreamPNG(bytes.NewReader(data)); err == nil {
			t.Fatal("invalid native frame accepted")
		}
	}
}
