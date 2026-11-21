package observation

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"testing"
	"time"

	"example.com/codex-vision/internal/platform"
)

func regionDeliveryFixture(t testing.TB) *Manager {
	t.Helper()
	m := New(nil, nil)
	scope := platform.Rect{X: -1100, Y: 100, Width: 960, Height: 540}
	m.opts = Options{Quality: 75, ModelMaxWidth: 320, WatchRegion: &scope}
	bounds := platform.Rect{X: -1280, Width: 1280, Height: 720}
	im := image.NewRGBA(image.Rect(0, 0, bounds.Width, bounds.Height))
	for y := 0; y < bounds.Height; y++ {
		for x := 0; x < bounds.Width; x++ {
			im.SetRGBA(x, y, color.RGBA{uint8(x / 5), uint8(y / 3), uint8((x + y) / 9), 255})
		}
	}
	frame, err := encodeSample(context.Background(), Sample{Image: im, Bounds: bounds}, m.opts)
	if err != nil {
		t.Fatal(err)
	}
	m.latest = frame
	m.status = Status{ID: "bench", State: "running"}
	return m
}

func BenchmarkNextResizedRegion(b *testing.B) {
	m := regionDeliveryFixture(b)
	b.ReportAllocs()
	for b.Loop() {
		m.status.Sequence++
		m.latest.Sequence = m.status.Sequence
		_, f, err := m.Next(context.Background(), "bench", m.deliveredSequence, 0)
		if err != nil || f == nil || f.Width != 320 || f.Height != 180 || f.Bounds != *m.opts.WatchRegion {
			b.Fatal(f, err)
		}
	}
}

// Pause a real resize at its first cancellation check after Next's preflight.
type pausedRenderContext struct {
	context.Context
	checks           int
	entered, release chan struct{}
}

func (c *pausedRenderContext) Err() error {
	c.checks++
	if c.checks == 2 {
		close(c.entered)
		<-c.release
	}
	return c.Context.Err()
}

func TestNextRechecksStateAfterResize(t *testing.T) {
	for _, change := range []string{"stop", "focus", "new_frame", "duplicate"} {
		t.Run(change, func(t *testing.T) {
			m := regionDeliveryFixture(t)
			m.status.CaptureBounds = m.latest.Bounds
			m.latest.Sequence = 1
			m.status.Sequence = 1
			ctx := &pausedRenderContext{Context: context.Background(), entered: make(chan struct{}), release: make(chan struct{})}
			frames := make(chan *Frame, 1)
			go func() { _, frame, _ := m.Next(ctx, "bench", 0, 0); frames <- frame }()
			defer func() {
				select {
				case <-ctx.release:
				default:
					close(ctx.release)
				}
			}()
			select {
			case <-ctx.entered:
			case <-time.After(2 * time.Second):
				t.Fatal("resize never started")
			}
			updated := make(chan error, 1)
			go func() {
				var err error
				switch change {
				case "stop":
					m.Stop()
				case "focus":
					_, err = m.Focus("bench", platform.Rect{X: -1200, Y: 10, Width: 500, Height: 400}, nil, nil)
				case "new_frame":
					m.mu.Lock()
					newer := *m.latest
					newer.Sequence++
					m.latest = &newer
					m.status.Sequence++
					m.mu.Unlock()
				case "duplicate":
					_, duplicate, e := m.Next(context.Background(), "bench", 0, 0)
					err = e
					if duplicate != nil {
						err = fmt.Errorf("duplicate image")
					}
				}
				updated <- err
			}()
			select {
			case err := <-updated:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("resize blocked concurrent operation")
			}
			close(ctx.release)
			f := <-frames
			if change == "stop" || change == "focus" {
				if f != nil || m.Status().ImagesReturned != 0 {
					t.Fatal("obsolete frame delivered")
				}
				return
			}
			if f == nil || f.Sequence != 1 || f.Bounds != *m.opts.WatchRegion || m.Status().ImagesReturned != 1 {
				t.Fatal("invalid first delivery")
			}
			if change == "new_frame" {
				_, next, err := m.Next(context.Background(), "bench", 1, 0)
				if err != nil || next == nil || next.Sequence != 2 || next.Kind != "full" {
					t.Fatal("newer frame lost", next, err)
				}
			}
		})
	}
}

func TestResizedRegionMapsToScaledCapture(t *testing.T) {
	m := regionDeliveryFixture(t)
	// Each stored JPEG pixel spans two desktop units on each axis.
	m.latest.Bounds = platform.Rect{X: -2560, Y: -140, Width: 2560, Height: 1440}
	scope := platform.Rect{X: -2199, Y: 61, Width: 1918, Height: 1076}
	m.opts.WatchRegion = &scope
	m.latest.Sequence = 1
	m.status.Sequence = 1
	_, f, err := m.Next(context.Background(), "bench", 0, 0)
	if err != nil || f == nil {
		t.Fatal(err)
	}
	want := platform.Rect{X: -2200, Y: 60, Width: 1920, Height: 1078}
	if f.Bounds != want || f.FullBounds != scope || f.Width != 320 || f.Height != 179 {
		t.Fatal(f.Bounds, f.FullBounds, f.Width, f.Height)
	}
	pixels, _, err := image.Decode(bytes.NewReader(f.Data))
	if err != nil {
		t.Fatal(err)
	}
	if pixels.Bounds() != image.Rect(0, 0, f.Width, f.Height) {
		t.Fatal(pixels.Bounds())
	}
	// The centre of the selected pixels corresponds to about (660,369) in the source gradient.
	centre := color.RGBAModel.Convert(pixels.At(160, 89)).(color.RGBA)
	if centre.R < 127 || centre.R > 137 || centre.G < 118 || centre.G > 128 {
		t.Fatal("crop origin or scaling is wrong", centre)
	}
}
