package observation

import (
	"example.com/codex-vision/internal/platform"
	"fmt"
	"image"
	"image/draw"
	"time"
)

func cloneRect(r *platform.Rect) *platform.Rect {
	if r == nil {
		return nil
	}
	out := *r
	return &out
}
func cloneWindow(w *platform.Window) *platform.Window {
	if w == nil {
		return nil
	}
	out := *w
	return &out
}
func sameRect(a, b *platform.Rect) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}
func sameWindow(a, b *platform.Window) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && a.ID == b.ID && a.Process == b.Process && a.Bounds == b.Bounds)
}

// Focus changes only model attention. The capture source and full preview URL
// stay unchanged. Frames sampled under an older focus revision are discarded.
func (m *Manager) Focus(id string, region platform.Rect, ready *platform.Rect, window *platform.Window) (Status, error) {
	m.op.Lock()
	defer m.op.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if id == "" || m.status.ID != id {
		return m.status, fmt.Errorf("unknown observation_id")
	}
	if m.status.State != "running" && m.status.State != "paused" {
		return m.status, fmt.Errorf("observation is not active")
	}
	if !m.status.CaptureBounds.Includes(region) {
		return m.status, fmt.Errorf("watch_region outside capture bounds")
	}
	if ready != nil && !m.status.CaptureBounds.Includes(*ready) {
		return m.status, fmt.Errorf("ready_region outside capture bounds")
	}
	if window != nil && (!window.Bounds.Includes(region) || (ready != nil && !window.Bounds.Includes(*ready))) {
		return m.status, fmt.Errorf("attention regions must be inside the pinned window")
	}
	if sameRect(m.opts.WatchRegion, &region) && sameRect(m.opts.ReadyRegion, ready) && sameWindow(m.opts.WatchWindow, window) {
		return m.status, nil
	}
	m.opts.WatchRegion = cloneRect(&region)
	m.opts.ReadyRegion = cloneRect(ready)
	m.opts.WatchWindow = cloneWindow(window)
	m.focusEpoch++
	m.latest = nil
	m.status.LastDetection = nil
	m.baseline = fingerprint{}
	m.persistent = persistence{}
	m.sampleSig = fingerprint{}
	m.markerSig = fingerprint{}
	m.pendingFull = true
	m.dirty = image.Rectangle{}
	m.status.WatchRegion = cloneRect(&region)
	m.status.ReadyRegion = cloneRect(ready)
	m.status.FocusVersion = m.focusEpoch
	m.lastActivity = time.Now()
	m.status.WatchWindowID = ""
	if window != nil {
		m.status.WatchWindowID = window.ID
	}
	m.signalLocked()
	select {
	case m.wake <- struct{}{}:
	default:
	}
	return m.status, nil
}
func (m *Manager) FocusState(id string) (*platform.Rect, *platform.Rect, *platform.Window, uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id == "" || m.status.ID != id {
		return nil, nil, nil, 0, fmt.Errorf("unknown observation_id")
	}
	return cloneRect(m.opts.WatchRegion), cloneRect(m.opts.ReadyRegion), cloneWindow(m.opts.WatchWindow), m.focusEpoch, nil
}
func view(src image.Image, bounds, region platform.Rect) image.Image {
	b := src.Bounds()
	r := image.Rect(b.Min.X+region.X-bounds.X, b.Min.Y+region.Y-bounds.Y, b.Min.X+region.X-bounds.X+region.Width, b.Min.Y+region.Y-bounds.Y+region.Height)
	return cropImage(src, r)
}

func cropImage(src image.Image, r image.Rectangle) image.Image {
	if sub, ok := src.(interface {
		SubImage(image.Rectangle) image.Image
	}); ok {
		return sub.SubImage(r)
	}
	out := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	draw.Draw(out, out.Bounds(), src, r.Min, draw.Src)
	return out
}
