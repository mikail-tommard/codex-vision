package wayland

import (
	"context"
	"errors"
	"image"
	"image/png"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"example.com/codex-vision/internal/platform"
)

type fakeRequest struct {
	uri     string
	err     error
	wait    bool
	closed  atomic.Int64
	started chan struct{}
}

func (r *fakeRequest) Wait(ctx context.Context) (string, error) {
	if r.started != nil {
		close(r.started)
	}
	if r.wait {
		<-ctx.Done()
		return "", ctx.Err()
	}
	return r.uri, r.err
}
func (r *fakeRequest) Close() error { r.closed.Add(1); return nil }

type fakePortal struct {
	request *fakeRequest
	calls   atomic.Int64
}

func (p *fakePortal) Begin(ctx context.Context) (screenshotRequest, error) {
	p.calls.Add(1)
	return p.request, ctx.Err()
}

func pngFixture(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Wayland local file URIs use Unix paths; no Windows backend")
	}
	path := filepath.Join(t.TempDir(), "private screenshot.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, image.NewRGBA(image.Rect(0, 0, 32, 24))); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return (&url.URL{Scheme: "file", Path: path}).String()
}

func TestPortalScreenshotConsentAndExplicitCapabilities(t *testing.T) {
	r := &fakeRequest{uri: pngFixture(t)}
	p := &fakePortal{request: r}
	d := newDesktop(p, os.Open)
	defer d.Release()
	im, err := d.Screenshot(context.Background())
	if err != nil || im.Bounds() != image.Rect(0, 0, 32, 24) || r.closed.Load() != 1 || p.calls.Load() != 1 {
		t.Fatal(im, err, r.closed.Load())
	}
	if d.Supports("mouse_click") {
		t.Fatal("portal screenshot granted input")
	}
	if _, err := d.Monitors(context.Background()); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
	if _, err := d.Active(context.Background()); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
	if _, err := d.Capture(context.Background(), platform.Rect{Width: 10, Height: 10}); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
	caps := platform.Capabilities(d)
	if caps["continuous_observation"] != false || caps["coordinate_mapping_available"] != false {
		t.Fatal(caps)
	}
}

func TestPortalCancellationTimeoutDenialAndStopCloseRequest(t *testing.T) {
	for _, mode := range []string{"denied", "timeout", "stop"} {
		t.Run(mode, func(t *testing.T) {
			r := &fakeRequest{wait: mode != "denied", started: make(chan struct{})}
			if mode == "denied" {
				r.err = ErrDenied
			}
			d := newDesktop(&fakePortal{request: r}, os.Open)
			defer d.Release()
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			if mode == "stop" {
				go func() { <-r.started; d.StopCapture() }()
			}
			_, err := d.Screenshot(ctx)
			if err == nil || r.closed.Load() != 1 {
				t.Fatal("unfinished request", err, r.closed.Load())
			}
			if mode == "timeout" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal(err)
			}
			if mode == "denied" && !errors.Is(err, ErrDenied) {
				t.Fatal(err)
			}
		})
	}
}

func TestPortalRejectsUntrustedURIAndOversizedFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Wayland local file URI decoder targets Unix paths")
	}
	for _, uri := range []string{"https://example.com/image.png", "file://other/path", "file:relative.png", "file:///tmp/a?secret=1", "file:///tmp/a#fragment", "file:///tmp/%00a"} {
		_, err := decodeScreenshot(context.Background(), uri, func(string) (*os.File, error) { t.Fatal("invalid URI opened"); return nil, nil })
		if err == nil {
			t.Fatal("invalid URI accepted", uri)
		}
	}
	path := filepath.Join(t.TempDir(), "private-token-path")
	_, err := decodeScreenshot(context.Background(), (&url.URL{Scheme: "file", Path: path}).String(), os.Open)
	if err == nil || strings.Contains(err.Error(), path) {
		t.Fatal("error leaked path", err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate((64 << 20) + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, err := decodeScreenshot(context.Background(), (&url.URL{Scheme: "file", Path: path}).String(), os.Open); err == nil {
		t.Fatal("oversized file decoded")
	}
}

func TestPortalResponseValidation(t *testing.T) {
	if _, err := responseURI(1, nil); !errors.Is(err, ErrDenied) {
		t.Fatal(err)
	}
	for _, code := range []uint32{0, 2, 99} {
		if _, err := responseURI(code, 42); err == nil {
			t.Fatal("invalid result accepted")
		}
	}
	if uri, err := responseURI(0, "file:///tmp/image.png"); err != nil || uri == "" {
		t.Fatal(uri, err)
	}
}
