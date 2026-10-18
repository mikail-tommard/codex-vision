package vision

import (
	"context"
	"image"
	"image/color"
	"testing"
)

func TestCompositeMixedScaleAndNegativeOrigin(t *testing.T) {
	view := image.Rect(-4, -2, 4, 2)
	dst := image.NewRGBA(image.Rect(0, 0, 8, 4))
	red := image.NewUniform(color.RGBA{255, 0, 0, 255})
	left := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			left.Set(x, y, red.C)
		}
	}
	right := image.NewRGBA(image.Rect(10, 10, 14, 14))
	for y := 10; y < 14; y++ {
		for x := 10; x < 14; x++ {
			right.Set(x, y, color.RGBA{0, 0, 255, 255})
		}
	}
	if e := Composite(context.Background(), dst, view, image.Rect(-4, -2, 0, 2), left); e != nil {
		t.Fatal(e)
	}
	if e := Composite(context.Background(), dst, view, image.Rect(0, -2, 4, 2), right); e != nil {
		t.Fatal(e)
	}
	if dst.RGBAAt(0, 0).R != 255 || dst.RGBAAt(3, 3).R != 255 || dst.RGBAAt(4, 0).B != 255 || dst.RGBAAt(7, 3).B != 255 {
		t.Fatal("mixed-scale mapping failed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if Composite(ctx, dst, view, view, left) == nil {
		t.Fatal("cancel ignored")
	}
}
