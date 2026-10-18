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

func TestFocusFiltersClockButKeepsFullPreview(t *testing.T) {
	var clock atomic.Uint32
	source := func(ctx context.Context) (Sample, error) {
		im := image.NewRGBA(image.Rect(0, 0, 320, 180))
		for y := 0; y < 180; y++ {
			for x := 200; x < 320; x++ {
				im.SetRGBA(x, y, color.RGBA{uint8(clock.Load()), 0, 0, 255})
			}
		}
		return Sample{Image: im, Bounds: platform.Rect{Width: 320, Height: 180}}, ctx.Err()
	}
	m := New(source, nil)
	defer m.Close()
	o := options()
	o.WatchRegion = &platform.Rect{Width: 160, Height: 160}
	s, f, e := m.Start(context.Background(), o)
	if e != nil || f.Width != 160 {
		t.Fatal(s, f, e)
	}
	clock.Store(120)
	eventually(t, func() bool { return m.Status().LocalFrames >= 3 })
	s, f, e = m.Next(context.Background(), s.ID, s.Sequence, 0)
	if e != nil || f != nil || s.Sequence != 1 {
		t.Fatal("clock leaked into model delivery", s, e)
	}
	m.mu.Lock()
	width := m.preview.Width
	m.mu.Unlock()
	if width != 320 {
		t.Fatal("preview cropped", width)
	}
	id := s.ID
	_, e = m.Focus(id, platform.Rect{X: 200, Width: 100, Height: 160}, nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	s, f, e = m.Next(context.Background(), id, 1, time.Second)
	if e != nil || f == nil || f.Bounds.X != 200 || s.ID != id {
		t.Fatal(s, f, e)
	}
}

func TestActionWaitOneCallbackAndOneScopedImage(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		t.Run(map[bool]string{false: "response", true: "timeout"}[timeout], func(t *testing.T) {
			var value atomic.Uint32
			var cursor atomic.Int32
			m := New(source(&value, &cursor), nil)
			defer m.Close()
			o := options()
			o.WatchRegion = &platform.Rect{X: 50, Y: 40, Width: 180, Height: 100}
			s, _, e := m.Start(context.Background(), o)
			if e != nil {
				t.Fatal(e)
			}
			calls := 0
			wait := 2500 * time.Millisecond
			if timeout {
				wait = 600 * time.Millisecond
			}
			result, status, frame, e := m.ActAndWait(context.Background(), s.ID, ActionWaitOptions{Settle: 100 * time.Millisecond, Wait: wait}, func(context.Context) error {
				calls++
				if !timeout {
					value.Store(80)
					go func() { time.Sleep(850 * time.Millisecond); value.Store(170) }()
				}
				return nil
			})
			if e != nil || calls != 1 || !result.Posted || result.RetryAction || result.ResponseVerified || frame == nil || frame.Bounds.Width != 180 || status.ImagesReturned != 2 {
				t.Fatal(result, status, frame, e, calls)
			}
			if result.TimedOut != timeout || (!timeout && !result.SubsequentChangeObserved) {
				t.Fatal(result)
			}
		})
	}
}
func TestActionWaitStopsWithoutRetry(t *testing.T) {
	var value atomic.Uint32
	var cursor atomic.Int32
	m := New(source(&value, &cursor), nil)
	defer m.Close()
	o := options()
	o.WatchRegion = &platform.Rect{Width: 160, Height: 160}
	s, _, e := m.Start(context.Background(), o)
	if e != nil {
		t.Fatal(e)
	}
	calls := 0
	r, _, f, e := m.ActAndWait(context.Background(), s.ID, ActionWaitOptions{Settle: 100 * time.Millisecond, Wait: time.Second}, func(context.Context) error { calls++; m.Stop(); return nil })
	if e == nil || calls != 1 || !r.Posted || f != nil || r.RetryAction {
		t.Fatal(r, f, e, calls)
	}
}

func TestActionWaitMarkerAndAttentionCancellation(t *testing.T) {
	for _, changeFocus := range []bool{false, true} {
		t.Run(map[bool]string{false: "marker", true: "focus_changed"}[changeFocus], func(t *testing.T) {
			var phase atomic.Uint32
			capture := func(ctx context.Context) (Sample, error) {
				im := image.NewRGBA(image.Rect(0, 0, 320, 180))
				v := phase.Load()
				for y := 0; y < 160; y++ {
					for x := 0; x < 160; x++ {
						im.SetRGBA(x, y, color.RGBA{uint8(v * 80), 0, 0, 255})
					}
				}
				for y := 10; y < 30; y++ {
					for x := 200; x < 220; x++ {
						red := uint8(0)
						if v == 1 {
							red = 255
						}
						im.SetRGBA(x, y, color.RGBA{red, 0, 0, 255})
					}
				}
				return Sample{Image: im, Bounds: platform.Rect{Width: 320, Height: 180}}, ctx.Err()
			}
			m := New(capture, nil)
			defer m.Close()
			o := options()
			o.WatchRegion = &platform.Rect{Width: 160, Height: 160}
			o.ReadyRegion = &platform.Rect{X: 200, Y: 10, Width: 20, Height: 20}
			s, _, e := m.Start(context.Background(), o)
			if e != nil {
				t.Fatal(e)
			}
			calls := 0
			r, _, f, e := m.ActAndWait(context.Background(), s.ID, ActionWaitOptions{Settle: 100 * time.Millisecond, Wait: 2500 * time.Millisecond}, func(context.Context) error {
				calls++
				phase.Store(1)
				if changeFocus {
					_, err := m.Focus(s.ID, platform.Rect{Width: 150, Height: 150}, nil, nil)
					return err
				}
				go func() { time.Sleep(850 * time.Millisecond); phase.Store(2) }()
				return nil
			})
			if calls != 1 || r.RetryAction {
				t.Fatal(r, calls)
			}
			if changeFocus {
				if e == nil || f != nil {
					t.Fatal("scope change did not abort", r, e)
				}
			} else if e != nil || f == nil || !r.MarkerDepartureObserved || !r.SubsequentChangeObserved || r.TimedOut {
				t.Fatal(r, e)
			}
		})
	}
}
func TestCancelledActionWaitDoesNotInvokeInput(t *testing.T) {
	var value atomic.Uint32
	var cursor atomic.Int32
	m := New(source(&value, &cursor), nil)
	defer m.Close()
	o := options()
	o.WatchRegion = &platform.Rect{Width: 160, Height: 160}
	s, _, e := m.Start(context.Background(), o)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	r, _, f, e := m.ActAndWait(ctx, s.ID, ActionWaitOptions{Settle: 100 * time.Millisecond, Wait: time.Second}, func(context.Context) error { calls++; return nil })
	if e == nil || calls != 0 || f != nil || r.Attempted {
		t.Fatal(r, e, calls)
	}
}
