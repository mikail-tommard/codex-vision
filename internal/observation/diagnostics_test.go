package observation

import (
	"context"
	"example.com/codex-vision/internal/platform"
	"image"
	"image/color"
	"sync/atomic"
	"testing"
	"time"
)

func TestDiagnosticsExplainStableChangeAndDesktopBounds(t *testing.T) {
	var value atomic.Uint32
	capture := func(ctx context.Context) (Sample, error) {
		im := image.NewRGBA(image.Rect(0, 0, 320, 180))
		v := uint8(value.Load())
		for y := 100; y < 120; y++ {
			for x := 200; x < 220; x++ {
				im.SetRGBA(x, y, color.RGBA{v, v, v, 255})
			}
		}
		return Sample{Image: im, Bounds: platform.Rect{X: -100, Y: 50, Width: 320, Height: 180}}, ctx.Err()
	}
	m := New(capture, nil)
	defer m.Close()
	o := options()
	o.Diagnostics = true
	o.Stability = 600 * time.Millisecond
	s, f, e := m.Start(context.Background(), o)
	if e != nil || f.Diagnostics == nil || f.Diagnostics.Delivery != "observation_start" || f.Diagnostics.Reason != "initial_context" {
		t.Fatal(s, f, e)
	}
	value.Store(180)
	eventually(t, func() bool { d := m.Status().LastDetection; return d != nil && d.Reason == "awaiting_stability" })
	_, premature, e := m.Next(context.Background(), s.ID, s.Sequence, 0)
	if e != nil || premature != nil {
		t.Fatal("diagnostics changed filtering", e)
	}
	_, f, e = m.Next(context.Background(), s.ID, s.Sequence, 2*time.Second)
	if e != nil || f == nil || f.Diagnostics == nil {
		t.Fatal(f, e)
	}
	d := f.Diagnostics
	if d.Reason != "stable_change" || d.Delivery != "observation_next" || d.ComparedSequence != s.Sequence || d.StabilityMS != 600 || d.RawFraction <= 0 || d.StableFraction <= 0 || d.RawBounds == nil || d.StableBounds == nil {
		t.Fatal(d)
	}
	if !d.RawBounds.Contains(110, 160) || d.RawBounds.Width > 24 || d.RawBounds.Height > 24 {
		t.Fatal("diagnostic bounds not in desktop coordinates", d.RawBounds)
	}
}
func TestDeliveryReasonsCoalescingAndImmutability(t *testing.T) {
	var value atomic.Uint32
	var cursor atomic.Int32
	m := New(source(&value, &cursor), nil)
	defer m.Close()
	o := options()
	o.Interval = 10 * time.Second
	o.Diagnostics = true
	o.Regions = true
	started, _, e := m.Start(context.Background(), o)
	if e != nil {
		t.Fatal(e)
	}
	m.mu.Lock()
	original := m.latest.Diagnostics
	m.dirty = image.Rect(40, 40, 60, 60)
	m.pendingFull = false
	m.status.Sequence = 4
	copyFrame := *m.latest
	copyFrame.Sequence = 4
	m.latest = &copyFrame
	f, e := m.regionFrame(context.Background(), started.Sequence)
	if e != nil || f.Diagnostics == nil || f.Diagnostics.Image != "changed_region" || f.Diagnostics.Coalesced != 2 || f.Diagnostics.AccumulatedBounds == nil {
		m.mu.Unlock()
		t.Fatal(f, e)
	}
	_, e = m.regionFrame(context.Background(), 0)
	if e != nil || original.Delivery != "observation_start" || original.Image != "initial_context" {
		m.mu.Unlock()
		t.Fatal("delivery metadata mutated cached frame", original, e)
	}
	f, e = m.regionFrame(context.Background(), 0)
	m.mu.Unlock()
	if e != nil || f.Diagnostics.Image != "base_mismatch" {
		t.Fatal(f, e)
	}
}
func TestActionTimeoutReasonAndOptOutWithoutExtraCaptures(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(map[bool]string{true: "on", false: "off"}[enabled], func(t *testing.T) {
			var value atomic.Uint32
			var cursor atomic.Int32
			var captures atomic.Int32
			src := source(&value, &cursor)
			m := New(func(ctx context.Context) (Sample, error) { captures.Add(1); return src(ctx) }, nil)
			defer m.Close()
			o := options()
			o.Diagnostics = enabled
			o.Interval = 10 * time.Second
			o.WatchRegion = &platform.Rect{Width: 160, Height: 160}
			s, _, e := m.Start(context.Background(), o)
			if e != nil || captures.Load() != 1 {
				t.Fatal(e)
			}
			_ = m.Status()
			_, _, e = m.Next(context.Background(), s.ID, s.Sequence, 0)
			if e != nil || captures.Load() != 1 {
				t.Fatal("diagnostics caused an extra initial capture", e)
			}
			r, _, f, e := m.ActAndWait(context.Background(), s.ID, ActionWaitOptions{Settle: 100 * time.Millisecond, Wait: 400 * time.Millisecond}, func(context.Context) error { return nil })
			if e != nil || !r.TimedOut || f == nil {
				t.Fatal(r, e)
			}
			if enabled {
				if f.Diagnostics == nil || f.Diagnostics.Delivery != "action_wait_timeout" || f.Diagnostics.DeliverySettleMS != 100 || f.Diagnostics.Reason != "unchanged" {
					t.Fatal(f.Diagnostics)
				}
			} else if f.Diagnostics != nil || m.Status().LastDetection != nil {
				t.Fatal("opt-out ignored")
			}
			// Serializing/reading diagnostics does not request another native sample.
			_, duplicate, e := m.Next(context.Background(), s.ID, f.Sequence, 0)
			if e != nil || duplicate != nil {
				t.Fatal("diagnostic read repeated model image", e)
			}
		})
	}
}
