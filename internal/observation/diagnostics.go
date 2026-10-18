package observation

import (
	"example.com/codex-vision/internal/platform"
	"image"
)

// Detector bounds are coarse desktop-coordinate bounds of the sampled grid,
// not object recognition. All values are relative to the masked task scope.
type ChangeDiagnostic struct {
	ComparisonValid  bool           `json:"comparison_valid"`
	Reason           string         `json:"detection_reason"`
	ComparedSequence uint64         `json:"compared_sequence"`
	RawFraction      float64        `json:"raw_fraction"`
	StableFraction   float64        `json:"stable_fraction"`
	Threshold        float64        `json:"threshold"`
	StabilityMS      int64          `json:"stability_ms"`
	Pending          bool           `json:"pending_stability,omitempty"`
	Grid             [2]int         `json:"sample_grid"`
	RawBounds        *platform.Rect `json:"raw_bounds,omitempty"`
	StableBounds     *platform.Rect `json:"stable_bounds,omitempty"`
}
type FrameDiagnostic struct {
	ChangeDiagnostic
	Delivery          string         `json:"delivery_reason"`
	Image             string         `json:"image_reason"`
	AccumulatedBounds *platform.Rect `json:"accumulated_bounds,omitempty"`
	Coalesced         uint64         `json:"coalesced_sequences,omitempty"`
	DeliverySettleMS  int64          `json:"delivery_settle_ms,omitempty"`
}

func desktopRect(r image.Rectangle, bounds platform.Rect) *platform.Rect {
	r = r.Intersect(image.Rect(0, 0, bounds.Width, bounds.Height))
	if r.Empty() {
		return nil
	}
	return &platform.Rect{X: bounds.X + r.Min.X, Y: bounds.Y + r.Min.Y, Width: r.Dx(), Height: r.Dy()}
}
func diagnosticBounds(before, after fingerprint, bounds platform.Rect, fraction float64) *platform.Rect {
	if fraction == 0 {
		return nil
	}
	return desktopRect(changedRect(before, after, image.Rect(0, 0, bounds.Width, bounds.Height)), bounds)
}
func (m *Manager) diagnostic(reason string, before, raw, accepted fingerprint, bounds platform.Rect, rawFraction, stableFraction float64, pending bool) ChangeDiagnostic {
	return ChangeDiagnostic{ComparisonValid: len(before.rgb) > 0 && before.width == raw.width && before.height == raw.height, Reason: reason, ComparedSequence: m.status.Sequence, RawFraction: rawFraction, StableFraction: stableFraction, Threshold: m.opts.ChangeThreshold, StabilityMS: m.opts.Stability.Milliseconds(), Pending: pending, Grid: [2]int{raw.width, raw.height}, RawBounds: diagnosticBounds(before, raw, bounds, rawFraction), StableBounds: diagnosticBounds(before, accepted, bounds, stableFraction)}
}
func copyDiagnostic(d *FrameDiagnostic) *FrameDiagnostic {
	if d == nil {
		return nil
	}
	copy := *d
	return &copy
}
