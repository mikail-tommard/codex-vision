package observation

import (
	"bytes"
	"context"
	"image"
	_ "image/jpeg"
	"time"

	"example.com/codex-vision/internal/platform"
	"example.com/codex-vision/internal/vision"
)

// A delivery owns its metadata and shares only the immutable capture bytes.
type frameDelivery struct {
	frame             Frame
	selected          image.Rectangle // relative to the capture origin, in desktop units
	quality, maxWidth int
}

func focusedDelivery(frame *Frame, o Options) frameDelivery {
	full := *frame
	full.Diagnostics = copyDiagnostic(frame.Diagnostics)
	scope := frame.Bounds
	if o.WatchRegion != nil {
		scope = *o.WatchRegion
	}
	full.FullBounds = scope
	x, y := scope.X-full.Bounds.X, scope.Y-full.Bounds.Y
	return frameDelivery{full, image.Rect(x, y, x+scope.Width, y+scope.Height), o.Quality, o.ModelMaxWidth}
}

// Caller holds the manager lock while selecting the delta and its base.
func (m *Manager) regionDelivery(after uint64) frameDelivery {
	plan := focusedDelivery(m.latest, m.opts)
	full := &plan.frame
	selected := plan.selected
	base := uint64(0)
	kind := "full"
	imageReason := "no_accumulated_delta"
	switch {
	case !m.opts.Regions:
		imageReason = "regions_disabled"
	case m.pendingFull:
		imageReason = "context_refresh"
	case after != m.deliveredSequence:
		imageReason = "base_mismatch"
	case time.Since(m.lastFull) >= 30*time.Second:
		imageReason = "periodic_context"
	case !m.dirty.Empty():
		imageReason = "large_change"
	}
	if m.opts.Regions && !m.pendingFull && after == m.deliveredSequence && time.Since(m.lastFull) < 30*time.Second && !m.dirty.Empty() {
		dirty := m.dirty.Intersect(selected)
		if !dirty.Empty() && int64(dirty.Dx())*int64(dirty.Dy())*100 < int64(selected.Dx())*int64(selected.Dy())*60 {
			selected = dirty
			kind = "region"
			imageReason = "changed_region"
			base = m.deliveredSequence
		}
	}
	if full.Diagnostics != nil {
		full.Diagnostics.Delivery = "observation_next"
		full.Diagnostics.Image = imageReason
		full.Diagnostics.AccumulatedBounds = desktopRect(m.dirty, full.Bounds)
		if full.Sequence > m.deliveredSequence {
			full.Diagnostics.Coalesced = full.Sequence - m.deliveredSequence - 1
		}
	}
	plan.selected = selected
	full.BaseSequence = base
	full.Kind = kind
	return plan
}

// Decode once, then encode directly from the selected pixels at delivery size.
func (p frameDelivery) render(ctx context.Context) (*Frame, error) {
	f := p.frame
	bounds := f.Bounds
	crop := p.selected != image.Rect(0, 0, bounds.Width, bounds.Height)
	if !crop && (p.maxWidth == 0 || f.Width <= p.maxWidth) {
		return &f, ctx.Err()
	}
	im, _, err := image.Decode(bytes.NewReader(f.Data))
	if err != nil {
		return nil, err
	}
	if crop {
		b := im.Bounds()
		// Round outward so a scaled capture still covers every requested edge.
		r := image.Rect(
			p.selected.Min.X*b.Dx()/bounds.Width,
			p.selected.Min.Y*b.Dy()/bounds.Height,
			(p.selected.Max.X*b.Dx()+bounds.Width-1)/bounds.Width,
			(p.selected.Max.Y*b.Dy()+bounds.Height-1)/bounds.Height,
		).Add(b.Min).Intersect(b)
		im = cropImage(im, r)
		x0, y0 := (r.Min.X-b.Min.X)*bounds.Width/b.Dx(), (r.Min.Y-b.Min.Y)*bounds.Height/b.Dy()
		x1, y1 := (r.Max.X-b.Min.X)*bounds.Width/b.Dx(), (r.Max.Y-b.Min.Y)*bounds.Height/b.Dy()
		f.Bounds = platform.Rect{X: bounds.X + x0, Y: bounds.Y + y0, Width: x1 - x0, Height: y1 - y0}
	}
	f.Data, f.MIME, f.Width, f.Height, err = vision.Encode(ctx, im, "jpeg", p.quality, p.maxWidth)
	if err != nil {
		return nil, err
	}
	return &f, nil
}

func nextInterval(base, current, ceiling time.Duration, unchanged int) time.Duration {
	if unchanged < 3 {
		return base
	}
	return min(ceiling, current*2)
}
