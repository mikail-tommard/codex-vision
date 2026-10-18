package observation

import "image"

type fingerprint struct {
	width, height int
	active        int
	rgb           []byte
}

func signature(src image.Image, masks ...image.Rectangle) fingerprint {
	b := src.Bounds()
	w, h := min(b.Dx(), 320), min(b.Dy(), 180)
	out := fingerprint{width: w, height: h, rgb: make([]byte, w*h*3)}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			point := image.Pt(b.Min.X+x*b.Dx()/w, b.Min.Y+y*b.Dy()/h)
			masked := false
			for _, mask := range masks {
				if point.In(mask) {
					masked = true
					break
				}
			}
			if masked {
				continue
			}
			out.active++
			r, g, blue, _ := src.At(point.X, point.Y).RGBA()
			i := (y*w + x) * 3
			out.rgb[i], out.rgb[i+1], out.rgb[i+2] = byte(r>>8), byte(g>>8), byte(blue>>8)
		}
	}
	return out
}
func difference(a, b fingerprint) float64 {
	if a.width != b.width || a.height != b.height || len(a.rgb) == 0 {
		return 1
	}
	changed := 0
	for i := 0; i < len(a.rgb); i += 3 {
		for channel := 0; channel < 3; channel++ {
			d := int(a.rgb[i+channel]) - int(b.rgb[i+channel])
			if d >= 12 || d <= -12 {
				changed++
				break
			}
		}
	}
	if b.active == 0 {
		return 0
	}
	return float64(changed) / float64(b.active)
}

// changedRect covers sampled changed pixels, including a grid-cell margin.
func changedRect(a, b fingerprint, bounds image.Rectangle) image.Rectangle {
	if a.width != b.width || a.height != b.height || len(a.rgb) == 0 {
		return bounds
	}
	region := image.Rectangle{}
	for y := 0; y < b.height; y++ {
		for x := 0; x < b.width; x++ {
			i := (y*b.width + x) * 3
			changed := false
			for c := 0; c < 3; c++ {
				d := int(a.rgb[i+c]) - int(b.rgb[i+c])
				if d >= 12 || d <= -12 {
					changed = true
					break
				}
			}
			if changed {
				r := image.Rect(bounds.Min.X+max(0, x-1)*bounds.Dx()/b.width, bounds.Min.Y+max(0, y-1)*bounds.Dy()/b.height, bounds.Min.X+min(b.width, x+2)*bounds.Dx()/b.width, bounds.Min.Y+min(b.height, y+2)*bounds.Dy()/b.height)
				region = region.Union(r)
			}
		}
	}
	return region
}
