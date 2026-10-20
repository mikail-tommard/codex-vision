package windows

import (
	"context"
	"fmt"
	"image"

	"example.com/codex-vision/internal/vision"
)

// copyDIBPixels copies a top-down 32-bpp BI_RGB DIB (BGRX) to owned opaque
// RGBA. The source can be freed immediately after this function returns.
func copyDIBPixels(ctx context.Context, width, height int, pixels []byte) (*image.RGBA, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("invalid DIB dimensions")
	}
	bounds := image.Rect(0, 0, width, height)
	if err := vision.Validate(bounds); err != nil {
		return nil, err
	}
	if len(pixels) != width*height*4 {
		return nil, fmt.Errorf("incomplete DIB pixel buffer: got %d bytes for %dx%d", len(pixels), width, height)
	}
	im := image.NewRGBA(bounds)
	for y := 0; y < height; y++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for x := 0; x < width; x++ {
			i := y*im.Stride + x*4
			im.Pix[i], im.Pix[i+1], im.Pix[i+2], im.Pix[i+3] = pixels[i+2], pixels[i+1], pixels[i], 255
		}
	}
	return im, nil
}
