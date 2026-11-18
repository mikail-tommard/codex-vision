package wayland

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/png"
	"io"

	"example.com/codex-vision/internal/vision"
)

// Read exactly one bounded PNG from a concatenated native stream. png.Decode
// alone can allocate from IHDR before a caller has checked the pixel budget.
func readStreamPNG(r io.Reader) (image.Image, error) {
	var signature [8]byte
	if _, err := io.ReadFull(r, signature[:]); err != nil {
		return nil, err
	}
	if string(signature[:]) != "\x89PNG\r\n\x1a\n" {
		return nil, fmt.Errorf("invalid frame signature")
	}
	const maxBytes = 64 << 20
	data := bytes.NewBuffer(signature[:])
	for chunks := 0; chunks < 4096; chunks++ {
		var header [8]byte
		if _, err := io.ReadFull(r, header[:]); err != nil {
			return nil, err
		}
		length := uint64(binary.BigEndian.Uint32(header[:4]))
		if length+12 > uint64(maxBytes-data.Len()) {
			return nil, fmt.Errorf("native frame exceeds byte budget")
		}
		if chunks == 0 && (string(header[4:]) != "IHDR" || length != 13) {
			return nil, fmt.Errorf("invalid native frame header")
		}
		_, _ = data.Write(header[:])
		if _, err := io.CopyN(data, r, int64(length)+4); err != nil {
			return nil, err
		}
		if chunks == 0 {
			cfg, err := png.DecodeConfig(bytes.NewReader(data.Bytes()))
			if err != nil {
				return nil, fmt.Errorf("invalid native image dimensions")
			}
			if err := vision.Validate(image.Rect(0, 0, cfg.Width, cfg.Height)); err != nil {
				return nil, err
			}
		}
		if string(header[4:]) == "IEND" {
			if length != 0 {
				return nil, fmt.Errorf("invalid native frame end")
			}
			return png.Decode(bytes.NewReader(data.Bytes()))
		}
	}
	return nil, fmt.Errorf("native frame chunk budget exceeded")
}
