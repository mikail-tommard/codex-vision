package vision

import (
	"image"
	"image/color"
	"image/draw"
)

type cursorVertex struct{ x, y float64 }

// WithCursorMarker overlays an arrow whose tip is the sampled desktop pointer.
// It does not claim to reproduce the OS cursor shape or its appearance during
// the screenshot's capture interval. Coordinates are relative to src.Bounds().
func WithCursorMarker(src image.Image, x, y int) image.Image {
	b := src.Bounds()
	tip := image.Pt(b.Min.X+x, b.Min.Y+y)
	if !tip.In(b) {
		return src
	}
	out := image.NewRGBA(b)
	draw.Draw(out, b, src, b.Min, draw.Src)
	outer := []cursorVertex{{0, 0}, {0, 20}, {5, 15}, {9, 23}, {13, 21}, {9, 13}, {17, 13}}
	inner := []cursorVertex{{2, 4}, {2, 16}, {5, 12}, {10, 21}, {11, 20}, {7, 11}, {12, 11}}
	paintCursorPolygon(out, tip, outer, color.RGBA{255, 255, 255, 255})
	paintCursorPolygon(out, tip, inner, color.RGBA{0, 0, 0, 255})
	// Give the exact pointer hotspot a pixel even when the outline tapers away.
	out.SetRGBA(tip.X, tip.Y, color.RGBA{255, 255, 255, 255})
	return out
}
func paintCursorPolygon(dst *image.RGBA, tip image.Point, polygon []cursorVertex, c color.RGBA) {
	rect := image.Rect(tip.X, tip.Y, tip.X+18, tip.Y+24).Intersect(dst.Bounds())
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		for x := rect.Min.X; x < rect.Max.X; x++ {
			px, py := float64(x-tip.X)+0.5, float64(y-tip.Y)+0.5
			inside := false
			for i, j := 0, len(polygon)-1; i < len(polygon); j, i = i, i+1 {
				a, b := polygon[i], polygon[j]
				if (a.y > py) != (b.y > py) && px < (b.x-a.x)*(py-a.y)/(b.y-a.y)+a.x {
					inside = !inside
				}
			}
			if inside {
				dst.SetRGBA(x, y, c)
			}
		}
	}
}
