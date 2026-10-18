package vision

import (
	"context"
	"fmt"
	"image"
)

// Composite maps a native display bitmap into global desktop units. This keeps
// mixed Retina scales independent of the origin and of adjacent display scales.
func Composite(ctx context.Context, dst *image.RGBA, view, display image.Rectangle, src image.Image) error {
	if view.Dx() != dst.Bounds().Dx() || view.Dy() != dst.Bounds().Dy() || display.Empty() || src.Bounds().Empty() {
		return fmt.Errorf("invalid desktop composition bounds")
	}
	intersection := view.Intersect(display)
	b := src.Bounds()
	for y := intersection.Min.Y; y < intersection.Max.Y; y++ {
		if e := ctx.Err(); e != nil {
			return e
		}
		for x := intersection.Min.X; x < intersection.Max.X; x++ {
			sx := b.Min.X + (x-display.Min.X)*b.Dx()/display.Dx()
			sy := b.Min.Y + (y-display.Min.Y)*b.Dy()/display.Dy()
			dst.Set(dst.Bounds().Min.X+x-view.Min.X, dst.Bounds().Min.Y+y-view.Min.Y, src.At(sx, sy))
		}
	}
	return ctx.Err()
}
