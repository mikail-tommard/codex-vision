package vision

import (
	"image"
	"image/color"
	"image/draw"
	"testing"
)

func TestCursorMarkerClipsAndDoesNotModifySource(t *testing.T) {
	src := image.NewRGBA(image.Rect(-10, -10, 40, 40))
	base := color.RGBA{20, 30, 40, 255}
	draw.Draw(src, src.Bounds(), image.NewUniform(base), image.Point{}, draw.Src)
	out := WithCursorMarker(src, 2, 3)
	if out.At(-8, -7) == base || src.RGBAAt(-8, -7) != base {
		t.Fatal("marker missing or source modified")
	}
	edge := WithCursorMarker(src, 49, 49)
	if edge.Bounds() != src.Bounds() || edge.At(39, 39) == base {
		t.Fatal("marker failed at image boundary")
	}
	if WithCursorMarker(src, -1, 0) != src || WithCursorMarker(src, 50, 0) != src {
		t.Fatal("off-image marker should be omitted")
	}
}
