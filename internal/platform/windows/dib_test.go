package windows

import (
	"context"
	"errors"
	"image/color"
	"testing"
)

func TestDIBPixelsRetainRowsColorsAndOwnStorage(t *testing.T) {
	// Odd width, distinguishable rows, and deliberately non-opaque X bytes.
	pixels := []byte{3, 2, 1, 0, 6, 5, 4, 1, 9, 8, 7, 2, 12, 11, 10, 3, 15, 14, 13, 4, 18, 17, 16, 5}
	im, err := copyDIBPixels(context.Background(), 3, 2, pixels)
	if err != nil {
		t.Fatal(err)
	}
	clear(pixels)
	for y := 0; y < 2; y++ {
		for x := 0; x < 3; x++ {
			n := uint8((y*3 + x) * 3)
			if got := im.RGBAAt(x, y); got != (color.RGBA{n + 1, n + 2, n + 3, 255}) {
				t.Fatal(x, y, got)
			}
		}
	}
}

func TestDIBPixelsRejectIncompleteInvalidAndCancelled(t *testing.T) {
	for _, size := range [][2]int{{0, 2}, {-1, 2}, {2, 0}, {2, 2}} {
		if im, err := copyDIBPixels(context.Background(), size[0], size[1], []byte{0}); err == nil || im != nil {
			t.Fatal("invalid or partial buffer returned", size, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if im, err := copyDIBPixels(ctx, 1, 1, make([]byte, 4)); !errors.Is(err, context.Canceled) || im != nil {
		t.Fatal(im, err)
	}
}
