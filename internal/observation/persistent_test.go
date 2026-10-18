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

func twoAreas(left, right uint8) fingerprint {
	im := image.NewRGBA(image.Rect(0, 0, 100, 40))
	for y := 0; y < 40; y++ {
		for x := 0; x < 100; x++ {
			v := left
			if x >= 50 {
				v = right
			}
			im.SetRGBA(x, y, color.RGBA{v, v, v, 255})
		}
	}
	return signature(im)
}
func TestPersistentChangeSurvivesIndependentAnimation(t *testing.T) {
	anchor := twoAreas(0, 0)
	var filter persistence
	at := time.Unix(100, 0)
	for i := 0; i <= 3; i++ {
		left := uint8(80)
		if i%2 == 1 {
			left = 180
		}
		accepted, fraction, pending := filter.filter(anchor, twoAreas(left, 120), at.Add(time.Duration(i)*200*time.Millisecond), 600*time.Millisecond)
		if !pending {
			t.Fatal("animation should still be pending")
		}
		if i < 3 && fraction != 0 {
			t.Fatal("published transient pixels", fraction)
		}
		if i == 3 && (fraction != .5 || difference(anchor, accepted) != .5 || difference(accepted, twoAreas(0, 120)) != 0) {
			t.Fatal("stable content lost behind animation", fraction)
		}
	}
}
func TestTransientReturnAndTolerance(t *testing.T) {
	anchor := twoAreas(0, 0)
	var filter persistence
	at := time.Unix(100, 0)
	for _, sample := range []struct {
		ms  int
		sig fingerprint
	}{{0, twoAreas(120, 0)}, {200, twoAreas(0, 0)}, {800, twoAreas(0, 0)}, {1000, twoAreas(10, 10)}} {
		_, fraction, _ := filter.filter(anchor, sample.sig, at.Add(time.Duration(sample.ms)*time.Millisecond), 600*time.Millisecond)
		if fraction != 0 {
			t.Fatal("transient or sub-tolerance noise published", sample.ms, fraction)
		}
	}
	accepted, fraction, _ := filter.filter(anchor, twoAreas(120, 0), at, 0)
	if fraction != .5 || difference(anchor, accepted) != .5 {
		t.Fatal("disable did not restore immediate detection")
	}
}
func TestStableObservationKeepsPreviewLiveAndDropsTransient(t *testing.T) {
	var value atomic.Uint32
	var cursor atomic.Int32
	m := New(source(&value, &cursor), nil)
	defer m.Close()
	o := options()
	o.Stability = 600 * time.Millisecond
	s, _, e := m.Start(context.Background(), o)
	if e != nil {
		t.Fatal(e)
	}
	value.Store(100)
	eventually(t, func() bool { return m.Status().LocalFrames >= 2 })
	// Return to the baseline before the hold time, as with a transient highlight.
	value.Store(0)
	eventually(t, func() bool { return m.Status().LocalFrames >= 5 })
	status, frame, e := m.Next(context.Background(), s.ID, s.Sequence, 0)
	if e != nil || frame != nil || status.Sequence != s.Sequence || status.PreviewSequence <= 1 || status.FilteredFrames == 0 {
		t.Fatal("transient leaked or preview froze", status, e)
	}
	value.Store(180)
	status, frame, e = m.Next(context.Background(), s.ID, s.Sequence, 2*time.Second)
	if e != nil || frame == nil || status.Sequence <= s.Sequence {
		t.Fatal("persistent change was lost", status, e)
	}
}
func TestActionFrameAcknowledgedBeforeBackgroundFilter(t *testing.T) {
	var value atomic.Uint32
	var cursor atomic.Int32
	m := New(source(&value, &cursor), nil)
	defer m.Close()
	o := options()
	o.Stability = time.Second
	o.WatchRegion = &platform.Rect{Width: 160, Height: 160}
	s, _, e := m.Start(context.Background(), o)
	if e != nil {
		t.Fatal(e)
	}
	result, status, frame, e := m.ActAndWait(context.Background(), s.ID, ActionWaitOptions{Settle: 100 * time.Millisecond, Wait: time.Second, SingleChange: true}, func(context.Context) error { value.Store(120); return nil })
	if e != nil || frame == nil || result.TimedOut || frame.Sequence <= s.Sequence {
		t.Fatal(result, status, e)
	}
	// Wait beyond the observer hold: the already delivered state must not reappear.
	status, duplicate, e := m.Next(context.Background(), s.ID, frame.Sequence, 1500*time.Millisecond)
	if e != nil || duplicate != nil || status.ImagesReturned != 2 {
		t.Fatal("macro state delivered twice", status, e)
	}
	// A genuinely new state remains deliverable after acknowledging the macro.
	value.Store(220)
	_, next, e := m.Next(context.Background(), s.ID, frame.Sequence, 2*time.Second)
	if e != nil || next == nil || next.Sequence <= frame.Sequence {
		t.Fatal("new state lost after acknowledgement", e)
	}
}

func TestActionAcknowledgementPreservesNewerPublishedState(t *testing.T) {
	var value atomic.Uint32
	var cursor atomic.Int32
	m := New(source(&value, &cursor), nil)
	defer m.Close()
	o := options()
	o.Interval = 10 * time.Second
	started, _, e := m.Start(context.Background(), o)
	if e != nil {
		t.Fatal(e)
	}
	// Capture a pending action result, then simulate a newer published frame
	// winning the lock before that result is delivered to the model.
	m.mu.Lock()
	older := sampledState{frame: m.preview, sig: m.sampleSig, epoch: m.focusEpoch, serial: m.previewSequence, generation: m.generation}
	m.mu.Unlock()
	value.Store(180)
	sample, e := source(&value, &cursor)(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	newer, sig, e := m.prepare(context.Background(), sample, o)
	if e != nil {
		t.Fatal(e)
	}
	newer.Sequence = 2
	m.mu.Lock()
	m.latest = newer
	m.status.Sequence = 2
	m.baseline = sig
	m.dirty = image.Rect(0, 0, 100, 100)
	m.mu.Unlock()
	status, frame, e := m.deliverSample(context.Background(), started.ID, older)
	if e != nil || frame.Sequence != 1 || status.Sequence != 2 {
		t.Fatal("older delivery overwrote newer sequence", status, e)
	}
	_, next, e := m.Next(context.Background(), started.ID, 1, time.Second)
	if e != nil || next == nil || next.Sequence != 2 {
		t.Fatal("newer state discarded by acknowledgement", e)
	}
}

func TestModelResolutionDoesNotChangePreviewOrCoordinates(t *testing.T) {
	var value atomic.Uint32
	var cursor atomic.Int32
	m := New(source(&value, &cursor), nil)
	defer m.Close()
	o := options()
	o.ModelMaxWidth = 160
	s, f, e := m.Start(context.Background(), o)
	if e != nil || f.Width != 160 || f.Bounds.Width != 320 || s.ModelMaxWidth != 160 {
		t.Fatal(s, f, e)
	}
	m.mu.Lock()
	previewWidth := m.preview.Width
	m.mu.Unlock()
	if previewWidth != 320 {
		t.Fatal("preview resized", previewWidth)
	}
	value.Store(120)
	_, f, e = m.Next(context.Background(), s.ID, s.Sequence, time.Second)
	if e != nil || f == nil || f.Width != 160 || f.Bounds.Width != 320 {
		t.Fatal(f, e)
	}
}
