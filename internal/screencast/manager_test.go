package screencast

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"example.com/codex-vision/internal/platform"
)

type queuedSource struct {
	frames chan image.Image
	closed chan struct{}
	once   sync.Once
	closes atomic.Int64
}

func sourceFixture() *queuedSource {
	return &queuedSource{frames: make(chan image.Image, 8), closed: make(chan struct{})}
}
func (s *queuedSource) Done() <-chan struct{} { return s.closed }
func (s *queuedSource) Next(ctx context.Context) (image.Image, error) {
	select {
	case im := <-s.frames:
		return im, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.closed:
		return nil, fmt.Errorf("closed")
	}
}
func (s *queuedSource) Close() error {
	s.once.Do(func() { s.closes.Add(1); close(s.closed) })
	return nil
}
func imageFixture(value uint8) image.Image {
	im := image.NewNRGBA(image.Rect(0, 0, 64, 40))
	for y := 0; y < 40; y++ {
		for x := 0; x < 64; x++ {
			im.SetNRGBA(x, y, color.NRGBA{R: value, G: 20, B: 30, A: 255})
		}
	}
	return im
}
func waitSample(t *testing.T, m *Manager, count int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if m.Status().Sampled >= count {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("frame not sampled", m.Status())
}
func waitDone(t *testing.T, m *Manager) {
	t.Helper()
	m.mu.Lock()
	done := m.done
	m.mu.Unlock()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("native cleanup did not finish")
	}
}
func startFixture(t *testing.T, m *Manager, s *queuedSource, max int) Status {
	t.Helper()
	status, err := m.Start(Options{Duration: 5 * time.Second, MaxFrames: max}, func(context.Context) (platform.ScreenCastSource, error) { return s, nil }, func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	return status
}

func TestStreamDropsDuplicateImagesAndKeepsOneConsentSession(t *testing.T) {
	var m Manager
	s := sourceFixture()
	start := startFixture(t, &m, s, 20)
	after := uint64(0)
	images, bytes := 0, 0
	for i, value := range []uint8{10, 10, 10, 90, 90} {
		s.frames <- imageFixture(value)
		waitSample(t, &m, i+1)
		status, frame, err := m.Next(context.Background(), start.ID, after, 0)
		if err != nil {
			t.Fatal(err)
		}
		if frame != nil {
			after = frame.Sequence
			images++
			bytes += len(frame.Data)
		}
		if status.State != "running" {
			t.Fatal(status)
		}
	}
	status := m.Status()
	if status.Sequence != 2 || status.Duplicates != 3 || images != 2 {
		t.Fatal(status, images)
	}
	if _, err := m.Start(Options{Duration: 5 * time.Second, MaxFrames: 20}, func(context.Context) (platform.ScreenCastSource, error) {
		t.Fatal("second consent started")
		return nil, nil
	}, func() error { return nil }); err == nil {
		t.Fatal("parallel capture allowed")
	}
	if _, err := m.Stop(start.ID); err != nil {
		t.Fatal(err)
	}
	waitDone(t, &m)
	if s.closes.Load() != 1 {
		t.Fatal("source leaked or closed repeatedly")
	}
	t.Logf("STREAM_RESULT sampled=5 images_before_single_shot=5 images_after_stream=%d image_bytes_after=%d scope=mock_images_not_tokens", images, bytes)
}

func TestStreamCancelDuringConsentAndTerminalPixelsAreReleased(t *testing.T) {
	var m Manager
	defer m.Close()
	opened := make(chan struct{})
	status, err := m.Start(Options{Duration: 5 * time.Second, MaxFrames: 20}, func(ctx context.Context) (platform.ScreenCastSource, error) {
		close(opened)
		<-ctx.Done()
		return nil, ctx.Err()
	}, func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	<-opened
	if _, err := m.Stop(status.ID); err != nil {
		t.Fatal(err)
	}
	waitDone(t, &m)
	_, frame, err := m.Next(context.Background(), status.ID, 0, 0)
	if err != nil || frame != nil || m.Status().State != "stopped" {
		t.Fatal(frame, err, m.Status())
	}
}

func TestStreamFrameBudgetCountsDuplicatesAndClosesSource(t *testing.T) {
	var m Manager
	s := sourceFixture()
	status := startFixture(t, &m, s, 2)
	s.frames <- imageFixture(10)
	s.frames <- imageFixture(10)
	waitDone(t, &m)
	if m.Status().State != "limit_reached" || m.Status().Sampled != 2 || s.closes.Load() != 1 {
		t.Fatal(m.Status())
	}
	if _, frame, err := m.Next(context.Background(), status.ID, 0, 0); err != nil || frame != nil {
		t.Fatal("terminal session leaked pixels", frame, err)
	}
}

func TestStreamLongPollTimeoutCursorAndStopGuard(t *testing.T) {
	var m Manager
	s := sourceFixture()
	status := startFixture(t, &m, s, 20)
	_, frame, err := m.Next(context.Background(), status.ID, 0, 10*time.Millisecond)
	if err != nil || frame != nil {
		t.Fatal(frame, err)
	}
	if _, _, err := m.Next(context.Background(), status.ID, 1, 0); err == nil {
		t.Fatal("future cursor accepted")
	}
	if _, err := m.Stop("wrong"); err == nil {
		t.Fatal("wrong stream stopped")
	}
	m.Close()
	waitDone(t, &m)
	if _, err := m.Start(Options{Duration: 5 * time.Second, MaxFrames: 2}, func(context.Context) (platform.ScreenCastSource, error) { return s, nil }, func() error { return nil }); err == nil {
		t.Fatal("emergency stop was reversible")
	}
}

func TestStreamDeadlineClosesBlockedNativeRead(t *testing.T) {
	var m Manager
	s := sourceFixture()
	startFixture(t, &m, s, 20)
	m.mu.Lock()
	done := m.done
	m.mu.Unlock()
	select {
	case <-done:
	case <-time.After(6 * time.Second):
		t.Fatal("capture exceeded duration")
	}
	if m.Status().State != "timeout" || s.closes.Load() != 1 {
		t.Fatal(m.Status())
	}
}

func TestStreamStopFileGuardAndProviderFailure(t *testing.T) {
	var m Manager
	defer m.Close()
	stopped := atomic.Bool{}
	s := sourceFixture()
	_, err := m.Start(Options{Duration: 5 * time.Second, MaxFrames: 20}, func(context.Context) (platform.ScreenCastSource, error) { return s, nil }, func() error {
		if stopped.Load() {
			return fmt.Errorf("stopped")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	stopped.Store(true)
	waitDone(t, &m)
	if m.Status().State != "stopped" {
		t.Fatal(m.Status())
	}
	// A failed provider is a terminal report, never simulated readiness.
	_, err = m.Start(Options{Duration: 5 * time.Second, MaxFrames: 20}, func(context.Context) (platform.ScreenCastSource, error) { return nil, fmt.Errorf("private UI details") }, func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	waitDone(t, &m)
	if m.Status().State != "failed" || m.Status().Code != "source_unavailable" {
		t.Fatal(m.Status())
	}
}

func TestStreamRevocationPreventsCachedPixels(t *testing.T) {
	var m Manager
	s := sourceFixture()
	status := startFixture(t, &m, s, 20)
	s.frames <- imageFixture(10)
	waitSample(t, &m, 1)
	_ = s.Close() // System/provider revocation while an image is cached.
	result, frame, err := m.Next(context.Background(), status.ID, 0, 0)
	if err != nil || frame != nil || result.State != "failed" {
		t.Fatal("revoked stream exposed pixels", result, frame, err)
	}
	waitDone(t, &m)
}

type failingCleanupSource struct{ *queuedSource }

func (s *failingCleanupSource) Close() error {
	s.queuedSource.Close()
	return fmt.Errorf("private native details")
}

func TestStreamCleanupFailurePreventsAnotherNativeSession(t *testing.T) {
	var m Manager
	defer m.Close()
	opened := make(chan struct{})
	status, err := m.Start(Options{Duration: 5 * time.Second, MaxFrames: 20}, func(context.Context) (platform.ScreenCastSource, error) {
		close(opened)
		return &failingCleanupSource{sourceFixture()}, nil
	}, func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	<-opened
	m.Stop(status.ID)
	waitDone(t, &m)
	if m.Status().Code != "cleanup_failed" {
		t.Fatal(m.Status())
	}
	if _, err := m.Start(Options{Duration: 5 * time.Second, MaxFrames: 20}, func(context.Context) (platform.ScreenCastSource, error) {
		t.Fatal("new source opened after cleanup failure")
		return nil, nil
	}, func() error { return nil }); err == nil {
		t.Fatal("cleanup failure ignored")
	}
}

func TestStreamConcurrentPullHonorsCallerTimeout(t *testing.T) {
	var m Manager
	s := sourceFixture()
	status := startFixture(t, &m, s, 20)
	first := make(chan struct{})
	go func() { defer close(first); m.Next(context.Background(), status.ID, 0, time.Second) }()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		waiting := len(m.pulls) == 1
		m.mu.Unlock()
		if waiting {
			break
		}
		time.Sleep(time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, _, err := m.Next(ctx, status.ID, 0, time.Second); err != context.DeadlineExceeded {
		t.Fatal("queued pull ignored caller cancellation", err)
	}
	m.Stop(status.ID)
	select {
	case <-first:
	case <-time.After(time.Second):
		t.Fatal("stop did not unblock long poll")
	}
}

type gatedImage struct {
	image.Image
	gate    <-chan struct{}
	reached chan struct{}
	once    sync.Once
}

func (g *gatedImage) At(x, y int) color.Color {
	g.once.Do(func() { close(g.reached) })
	<-g.gate
	return g.Image.At(x, y)
}

func TestStreamRevocationOrCallerCancellationDuringEncodingReturnsNoPixels(t *testing.T) {
	for _, revoke := range []bool{false, true} {
		t.Run(fmt.Sprint(revoke), func(t *testing.T) {
			var m Manager
			s := sourceFixture()
			status := startFixture(t, &m, s, 20)
			s.frames <- imageFixture(10)
			waitSample(t, &m, 1)
			gate := make(chan struct{})
			reached := make(chan struct{})
			defer func() {
				select {
				case <-gate:
				default:
					close(gate)
				}
			}()
			m.mu.Lock()
			m.image = &gatedImage{Image: m.image, gate: gate, reached: reached}
			m.mu.Unlock()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan struct{})
			var frame *Frame
			var err error
			go func() { defer close(done); _, frame, err = m.Next(ctx, status.ID, 0, 0) }()
			select {
			case <-reached:
			case <-time.After(time.Second):
				t.Fatal("encoder did not reach image")
			}
			if revoke {
				s.Close()
			} else {
				cancel()
			}
			close(gate)
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("encoder did not finish")
			}
			if frame != nil || (!revoke && err != context.Canceled) || m.Status().Delivered != 0 {
				t.Fatal("cancelled/revoked encoding exposed pixels", frame, err, m.Status())
			}
		})
	}
}

func TestStreamConsentTimeoutIsNotSuccessfulReadiness(t *testing.T) {
	var m Manager
	defer m.Close()
	_, err := m.Start(Options{Duration: 5 * time.Second, MaxFrames: 20}, func(context.Context) (platform.ScreenCastSource, error) { return nil, context.DeadlineExceeded }, func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	waitDone(t, &m)
	if m.Status().State != "timeout" || m.Status().Code != "setup_timeout" || m.Status().Sequence != 0 {
		t.Fatal(m.Status())
	}
}
