package observation

import (
	"bytes"
	"context"
	"errors"
	"example.com/codex-vision/internal/platform"
	"image"
	"image/color"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func options() Options {
	return Options{Mode: "continuous", Interval: 250 * time.Millisecond, MinDeliveryInterval: 250 * time.Millisecond, TaskIdleTimeout: time.Minute, ChangeThreshold: .001, MaxWidth: 320, Quality: 75, Backend: "mock"}
}
func source(value *atomic.Uint32, cursor *atomic.Int32) func(context.Context) (Sample, error) {
	return func(ctx context.Context) (Sample, error) {
		im := image.NewRGBA(image.Rect(0, 0, 320, 180))
		v := uint8(value.Load())
		for y := 0; y < 180; y++ {
			for x := 0; x < 320; x++ {
				im.SetRGBA(x, y, color.RGBA{v, v, v, 255})
			}
		}
		p := &platform.Point{X: int(cursor.Load()), Y: 30}
		return Sample{Image: im, Bounds: platform.Rect{Width: 320, Height: 180}, Cursor: p}, ctx.Err()
	}
}
func eventually(t *testing.T, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not reached")
}
func TestChangesPauseAndDedup(t *testing.T) {
	var value atomic.Uint32
	var cursor atomic.Int32
	m := New(source(&value, &cursor), nil)
	defer m.Close()
	status, f, err := m.Start(context.Background(), options())
	if err != nil || f == nil || status.ImagesReturned != 1 {
		t.Fatal(status, err)
	}
	id := status.ID
	cursor.Store(100)
	eventually(t, func() bool { return m.Status().LocalFrames >= 2 })
	s, f, err := m.Next(context.Background(), id, 0, 0)
	if err != nil || f != nil || s.Sequence != 1 {
		t.Fatal("cursor should not trigger model frame", s, err)
	}
	value.Store(100)
	s, f, err = m.Next(context.Background(), id, 1, 2*time.Second)
	if err != nil || f == nil || f.Sequence <= 1 || s.ImagesReturned != 2 {
		t.Fatal(s, err)
	}
	_, f, err = m.Next(context.Background(), id, 0, 0)
	if err != nil || f != nil {
		t.Fatal("duplicate delivered", err)
	}
	if _, err = m.Control(id, "pause"); err != nil {
		t.Fatal(err)
	}
	count := m.Status().LocalFrames
	value.Store(200)
	time.Sleep(300 * time.Millisecond)
	if m.Status().LocalFrames != count {
		t.Fatal("captured while paused")
	}
	if _, err = m.Control(id, "resume"); err != nil {
		t.Fatal(err)
	}
	_, f, err = m.Next(context.Background(), id, s.Sequence, 2*time.Second)
	if err != nil || f == nil {
		t.Fatal("missing resumed image", err)
	}
	if _, err = m.Control("wrong", "stop"); err == nil {
		t.Fatal("accepted stale id")
	}
	m.Stop()
	_, f, err = m.Next(context.Background(), id, 0, 0)
	if err != nil || f != nil || m.Status().State != "stopped" {
		t.Fatal(err)
	}
}
func TestIdleStopAndEmergency(t *testing.T) {
	var value atomic.Uint32
	var cursor atomic.Int32
	var stopped atomic.Bool
	m := New(source(&value, &cursor), func() error {
		if stopped.Load() {
			return errors.New("emergency stop")
		}
		return nil
	})
	defer m.Close()
	o := options()
	o.Mode = "task"
	o.TaskIdleTimeout = 80 * time.Millisecond
	if _, _, err := m.Start(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return m.Status().Reason == "task_idle_timeout" })
	if _, _, err := m.Start(context.Background(), options()); err != nil {
		t.Fatal(err)
	}
	stopped.Store(true)
	eventually(t, func() bool { return m.Status().Reason == "session_unavailable" })
	if _, _, err := m.Start(context.Background(), options()); err == nil {
		t.Fatal("ignored stop")
	}
}
func TestCaptureCancellation(t *testing.T) {
	var calls atomic.Int32
	entered := make(chan struct{})
	exited := make(chan struct{})
	m := New(func(ctx context.Context) (Sample, error) {
		if calls.Add(1) == 1 {
			im := image.NewRGBA(image.Rect(0, 0, 160, 90))
			return Sample{Image: im, Bounds: platform.Rect{Width: 160, Height: 90}}, nil
		}
		close(entered)
		<-ctx.Done()
		close(exited)
		return Sample{}, ctx.Err()
	}, nil)
	if _, _, err := m.Start(context.Background(), options()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("capture not started")
	}
	m.Close()
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("capture not canceled")
	}
	if _, _, err := m.Start(context.Background(), options()); err == nil {
		t.Fatal("restarted closed manager")
	}
}
func TestPrivatePreview(t *testing.T) {
	var value atomic.Uint32
	var cursor atomic.Int32
	m := New(source(&value, &cursor), nil)
	defer m.Close()
	s, _, err := m.Start(context.Background(), options())
	if err != nil {
		t.Fatal(err)
	}
	host := "127.0.0.1:43210"
	base := "http://" + host + "/" + s.ID + "/"
	handler := m.previewHandler(s.ID, host)
	do := func(method, path, body, origin string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, base+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		ctx, cancel := context.WithTimeout(req.Context(), 30*time.Millisecond)
		defer cancel()
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, req.WithContext(ctx))
		return out
	}
	r := do("GET", "", "", "")
	if r.Code != 200 || !strings.Contains(r.Body.String(), "Наблюдение за экраном") || r.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("bad preview")
	}
	req := httptest.NewRequest("GET", base+"status", nil)
	req.Host = "evil.example"
	r = httptest.NewRecorder()
	handler.ServeHTTP(r, req)
	if r.Code != 404 {
		t.Fatal("accepted foreign host")
	}
	req = httptest.NewRequest("GET", "http://"+host+"/wrong/status", nil)
	r = httptest.NewRecorder()
	handler.ServeHTTP(r, req)
	if r.Code != 404 {
		t.Fatal("accepted wrong capability")
	}
	if do("POST", "control", `{"action":"pause"}`, "http://evil.example").Code != 403 {
		t.Fatal("accepted foreign origin")
	}
	for _, body := range []string{`{"action":"pause","extra":1}`, `{"action":"pause"} {}`, `{"action":"type"}`} {
		if do("POST", "control", body, "").Code != 400 {
			t.Fatal("accepted invalid control", body)
		}
	}
	r = do("GET", "stream", "", "")
	if r.Code != 200 || !strings.Contains(r.Body.String(), "Content-Type: image/jpeg") {
		t.Fatal("invalid stream")
	}
	r = do("POST", "control", `{"action":"pause"}`, "")
	if r.Code != 200 || m.Status().State != "paused" {
		t.Fatal("pause failed")
	}
	r = do("POST", "control", `{"action":"resume"}`, "")
	if r.Code != 200 || m.Status().State != "running" {
		t.Fatal("resume failed")
	}
	r = do("POST", "control", `{"action":"stop"}`, "")
	if r.Code != 200 || m.Status().State != "stopped" || !strings.Contains(r.Body.String(), "stopped") {
		t.Fatal("stop failed")
	}
	// An old capability cannot control a new session.
	next, _, err := m.Start(context.Background(), options())
	if err != nil {
		t.Fatal(err)
	}
	if do("POST", "control", `{"action":"stop"}`, "").Code != 409 || m.Status().ID != next.ID || m.Status().State != "running" {
		t.Fatal("stale capability stopped new session")
	}
}
func TestPreviewListener(t *testing.T) {
	var value atomic.Uint32
	var cursor atomic.Int32
	m := New(source(&value, &cursor), nil)
	defer m.Close()
	o := options()
	o.Preview = true
	s, _, err := m.Start(context.Background(), o)
	if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) {
		t.Skip("environment prohibits local listeners; HTTP handler tested without sockets")
	}
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: time.Second}
	r, err := client.Get(s.PreviewURL)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != 200 {
		t.Fatal(r.StatusCode)
	}
	m.Stop()
	if _, err := client.Get(s.PreviewURL); err == nil {
		t.Fatal("listener still open")
	}
}

func TestDeliveryIntervalCoalescesChanges(t *testing.T) {
	var value atomic.Uint32
	var cursor atomic.Int32
	m := New(source(&value, &cursor), nil)
	defer m.Close()
	o := options()
	o.MinDeliveryInterval = time.Second
	status, _, err := m.Start(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	value.Store(80)
	eventually(t, func() bool { return m.Status().Sequence >= 2 })
	_, frame, err := m.Next(context.Background(), status.ID, 1, 0)
	if err != nil || frame != nil {
		t.Fatal("delivery interval ignored", err)
	}
	value.Store(170)
	eventually(t, func() bool { return m.Status().Sequence >= 3 })
	next, frame, err := m.Next(context.Background(), status.ID, 1, 2*time.Second)
	if err != nil || frame == nil || frame.Sequence < 3 || next.ImagesReturned != 2 {
		t.Fatal("did not coalesce changes", next, err)
	}
}

func TestAdaptiveIdleAndActivity(t *testing.T) {
	var value atomic.Uint32
	var cursor atomic.Int32
	m := New(source(&value, &cursor), nil)
	defer m.Close()
	o := options()
	o.Adaptive = true
	o.IdleInterval = time.Second
	if _, _, err := m.Start(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return m.Status().CurrentIntervalMS >= 500 })
	m.Touch()
	eventually(t, func() bool { return m.Status().CurrentIntervalMS == 250 })
	if nextInterval(250*time.Millisecond, time.Second, time.Second, 10) != time.Second {
		t.Fatal("idle ceiling ignored")
	}
}
func TestRegionDeliveryAccumulatesCoalescedChanges(t *testing.T) {
	var phase atomic.Uint32
	m := New(func(ctx context.Context) (Sample, error) {
		im := image.NewRGBA(image.Rect(0, 0, 320, 180))
		for i := 3; i < len(im.Pix); i += 4 {
			im.Pix[i] = 255
		}
		p := phase.Load()
		if p >= 1 {
			for y := 40; y < 60; y++ {
				for x := 20; x < 40; x++ {
					im.SetRGBA(x, y, color.RGBA{255, 0, 0, 255})
				}
			}
		}
		if p >= 2 {
			for y := 80; y < 100; y++ {
				for x := 150; x < 170; x++ {
					im.SetRGBA(x, y, color.RGBA{0, 255, 0, 255})
				}
			}
		}
		return Sample{Image: im, Bounds: platform.Rect{X: -320, Width: 320, Height: 180}}, ctx.Err()
	}, nil)
	defer m.Close()
	o := options()
	o.Regions = true
	status, _, err := m.Start(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	phase.Store(1)
	eventually(t, func() bool { return m.Status().Sequence >= 2 })
	phase.Store(2)
	eventually(t, func() bool { return m.Status().Sequence >= 3 })
	_, frame, err := m.Next(context.Background(), status.ID, 1, time.Second)
	if err != nil || frame == nil {
		t.Fatal(err)
	}
	if frame.Kind != "region" || frame.BaseSequence != 1 || frame.FullBounds.Width != 320 || !frame.Bounds.Contains(-300, 40) || !frame.Bounds.Contains(-151, 99) || frame.Width >= 320 {
		t.Fatal("crop lost coalesced content or mapping", frame)
	}
	// A client without the previous image receives full context, never a crop.
	m.mu.Lock()
	m.latest.Sequence++
	m.status.Sequence++
	m.dirty = image.Rect(20, 40, 40, 60)
	m.lastDelivery = time.Now().Add(-time.Second)
	m.mu.Unlock()
	_, frame, err = m.Next(context.Background(), status.ID, 0, 0)
	if err != nil || frame == nil || frame.Kind != "full" {
		t.Fatal("missing base did not force full context", err)
	}
}

func TestFrequentActivityDoesNotStarveCapture(t *testing.T) {
	var value atomic.Uint32
	var cursor atomic.Int32
	m := New(source(&value, &cursor), nil)
	defer m.Close()
	if _, _, err := m.Start(context.Background(), options()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.Touch()
			}
		}
	}()
	eventually(t, func() bool { return m.Status().LocalFrames >= 3 })
}

func TestIgnoredRegionsDoNotWakeModelButRemainVisible(t *testing.T) {
	var phase atomic.Uint32
	m := New(func(ctx context.Context) (Sample, error) {
		im := image.NewRGBA(image.Rect(0, 0, 320, 180))
		for i := 3; i < len(im.Pix); i += 4 {
			im.Pix[i] = 255
		}
		if phase.Load() > 0 {
			for y := 0; y < 30; y++ {
				for x := 0; x < 50; x++ {
					im.SetRGBA(x, y, color.RGBA{255, 255, 255, 255})
				}
			}
		}
		if phase.Load() > 1 {
			for y := 80; y < 100; y++ {
				for x := 100; x < 120; x++ {
					im.SetRGBA(x, y, color.RGBA{255, 0, 0, 255})
				}
			}
		}
		return Sample{Image: im, Bounds: platform.Rect{X: -320, Width: 320, Height: 180}}, ctx.Err()
	}, nil)
	defer m.Close()
	o := options()
	o.IgnoreRegions = []platform.Rect{{X: -320, Width: 50, Height: 30}}
	s, _, err := m.Start(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	phase.Store(1)
	eventually(t, func() bool { return m.Status().LocalFrames >= 2 })
	status, frame, err := m.Next(context.Background(), s.ID, 1, 0)
	if err != nil || frame != nil || status.Sequence != 1 {
		t.Fatal("ignored clock triggered model image", status, err)
	}
	m.mu.Lock()
	preview := m.preview
	m.mu.Unlock()
	im, _, err := image.Decode(bytes.NewReader(preview.Data))
	if err != nil {
		t.Fatal(err)
	}
	r, _, _, _ := im.At(10, 10).RGBA()
	if r < 50000 {
		t.Fatal("ignore region incorrectly redacted preview")
	}
	phase.Store(2)
	_, frame, err = m.Next(context.Background(), s.ID, 1, time.Second)
	if err != nil || frame == nil {
		t.Fatal("real content change missed", err)
	}
}
