package vision

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"testing"
)

func TestImageEncodingAndResize(t *testing.T) {
	im := image.NewRGBA(image.Rect(10, 20, 110, 70))
	im.Set(10, 20, color.RGBA{255, 0, 0, 255})
	for _, format := range []string{"png", "jpeg"} {
		data, mime, w, h, e := Encode(context.Background(), im, format, 80, 50)
		if e != nil || w != 50 || h != 25 || mime != "image/"+format {
			t.Fatal(w, h, mime, e)
		}
		out, _, e := image.Decode(bytes.NewReader(data))
		if e != nil || out.Bounds().Dx() != 50 || out.Bounds().Dy() != 25 {
			t.Fatal(e)
		}
	}
	if _, _, _, _, e := Encode(context.Background(), im, "gif", 85, 0); e == nil {
		t.Fatal("invalid format")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, _, _, e := Encode(ctx, im, "png", 85, 0); e == nil {
		t.Fatal("cancellation ignored")
	}
	if Validate(image.Rect(0, 0, 32000, 32000)) == nil {
		t.Fatal("pixel budget")
	}
}
