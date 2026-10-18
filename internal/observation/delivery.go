package observation

import (
	"bytes"
	"context"
	"example.com/codex-vision/internal/platform"
	"example.com/codex-vision/internal/vision"
	"image"
	"image/draw"
	_ "image/jpeg"
	"time"
)

func cropFrame(ctx context.Context, source *Frame, im image.Image, r image.Rectangle, quality int, kind string, base uint64) (*Frame, error) {
	b := im.Bounds()
	r = r.Intersect(b)
	crop := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	draw.Draw(crop, crop.Bounds(), im, r.Min, draw.Src)
	data, mime, w, h, err := vision.Encode(ctx, crop, "jpeg", quality, 0)
	if err != nil {
		return nil, err
	}
	full := *source
	bounds := source.Bounds
	x0 := (r.Min.X - b.Min.X) * bounds.Width / b.Dx()
	y0 := (r.Min.Y - b.Min.Y) * bounds.Height / b.Dy()
	x1 := (r.Max.X - b.Min.X) * bounds.Width / b.Dx()
	y1 := (r.Max.Y - b.Min.Y) * bounds.Height / b.Dy()
	full.Data = data
	full.MIME = mime
	full.Width = w
	full.Height = h
	full.Bounds = platform.Rect{X: bounds.X + x0, Y: bounds.Y + y0, Width: x1 - x0, Height: y1 - y0}
	full.Kind = kind
	full.BaseSequence = base
	return &full, nil
}
func (m *Manager) regionFrame(ctx context.Context, after uint64) (*Frame, error) {
	full := *m.latest
	full.Diagnostics = copyDiagnostic(full.Diagnostics)
	full.Kind = "full"
	scope := full.Bounds
	if m.opts.WatchRegion != nil {
		scope = *m.opts.WatchRegion
	}
	full.FullBounds = scope
	selected := image.Rect(scope.X-full.Bounds.X, scope.Y-full.Bounds.Y, scope.X-full.Bounds.X+scope.Width, scope.Y-full.Bounds.Y+scope.Height)
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
	if selected == image.Rect(0, 0, full.Bounds.Width, full.Bounds.Height) {
		return m.modelFrame(ctx, &full)
	}
	im, _, err := image.Decode(bytes.NewReader(full.Data))
	if err != nil {
		return nil, err
	}
	b := im.Bounds()
	r := image.Rect(selected.Min.X*b.Dx()/full.Bounds.Width, selected.Min.Y*b.Dy()/full.Bounds.Height, (selected.Max.X*b.Dx()+full.Bounds.Width-1)/full.Bounds.Width, (selected.Max.Y*b.Dy()+full.Bounds.Height-1)/full.Bounds.Height)
	cropped, err := cropFrame(ctx, &full, im, r, m.opts.Quality, kind, base)
	if err != nil {
		return nil, err
	}
	return m.modelFrame(ctx, cropped)
}
func nextInterval(base, current, ceiling time.Duration, unchanged int) time.Duration {
	if unchanged < 3 {
		return base
	}
	return min(ceiling, current*2)
}

// Keep the preview JPEG intact. Resize only images explicitly delivered to a model.
func (m *Manager) modelFrame(ctx context.Context, f *Frame) (*Frame, error) {
	if m.opts.ModelMaxWidth == 0 || f.Width <= m.opts.ModelMaxWidth {
		return f, nil
	}
	im, _, err := image.Decode(bytes.NewReader(f.Data))
	if err != nil {
		return nil, err
	}
	data, mime, w, h, err := vision.Encode(ctx, im, "jpeg", m.opts.Quality, m.opts.ModelMaxWidth)
	if err != nil {
		return nil, err
	}
	copyFrame := *f
	copyFrame.Data = data
	copyFrame.MIME = mime
	copyFrame.Width = w
	copyFrame.Height = h
	return &copyFrame, nil
}
