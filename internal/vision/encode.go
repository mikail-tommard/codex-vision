package vision

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
)

const MaxPixels = 32_000_000

func Validate(r image.Rectangle) error {
	if r.Dx() <= 0 || r.Dy() <= 0 || int64(r.Dx())*int64(r.Dy()) > MaxPixels {
		return fmt.Errorf("capture exceeds %d pixel budget or is empty", MaxPixels)
	}
	return nil
}
func Encode(ctx context.Context, src image.Image, format string, quality, maxWidth int) ([]byte, string, int, int, error) {
	if err := Validate(src.Bounds()); err != nil {
		return nil, "", 0, 0, err
	}
	if format == "" {
		format = "png"
	}
	if format != "png" && format != "jpeg" {
		return nil, "", 0, 0, fmt.Errorf("format must be png or jpeg")
	}
	if quality == 0 {
		quality = 85
	}
	if quality < 1 || quality > 100 || maxWidth < 0 || maxWidth > 16384 {
		return nil, "", 0, 0, fmt.Errorf("quality must be 1..100; max_width 0..16384")
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if maxWidth > 0 && w > maxWidth {
		nw := maxWidth
		nh := max(1, h*nw/w)
		dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
		for y := 0; y < nh; y++ {
			if e := ctx.Err(); e != nil {
				return nil, "", 0, 0, e
			}
			for x := 0; x < nw; x++ {
				dst.Set(x, y, src.At(b.Min.X+x*w/nw, b.Min.Y+y*h/nh))
			}
		}
		src = dst
		w, h = nw, nh
	}
	if e := ctx.Err(); e != nil {
		return nil, "", 0, 0, e
	}
	var out bytes.Buffer
	var err error
	if format == "png" {
		err = png.Encode(&out, src)
	} else {
		err = jpeg.Encode(&out, src, &jpeg.Options{Quality: quality})
	}
	if err == nil {
		err = ctx.Err()
	}
	return out.Bytes(), "image/" + format, w, h, err
}
