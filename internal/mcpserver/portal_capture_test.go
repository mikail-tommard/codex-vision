package mcpserver

import (
	"context"
	"image"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"example.com/codex-vision/internal/config"
	"example.com/codex-vision/internal/platform/mock"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type portalDesktop struct {
	*mock.Desktop
	calls   atomic.Int64
	wait    bool
	started chan struct{}
	stopped chan struct{}
	once    sync.Once
}

func newPortalFixture() *portalDesktop {
	return &portalDesktop{Desktop: &mock.Desktop{}, started: make(chan struct{}), stopped: make(chan struct{})}
}
func (*portalDesktop) Name() string   { return "test-wayland-portal" }
func (d *portalDesktop) StopCapture() { d.once.Do(func() { close(d.stopped) }) }
func (d *portalDesktop) Screenshot(ctx context.Context) (image.Image, error) {
	d.calls.Add(1)
	close(d.started)
	if d.wait {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-d.stopped:
			return nil, context.Canceled
		}
	}
	return image.NewRGBA(image.Rect(0, 0, 1280, 720)), nil
}

func TestPortalCompactCatalogAndUnmappedImage(t *testing.T) {
	d := newPortalFixture()
	c, _ := connectProfileDesktop(t, config.Default(), d, "compact")
	listed, err := c.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Tools) != 3 {
		t.Fatal("portal advertised unavailable GUI tools", len(listed.Tools))
	}
	r := call(t, c, "screen_capture", map[string]any{}, true)
	data := fieldsData(t, r)
	if len(r.Content) != 2 || data["width"] != float64(768) || data["coordinate_mapping_available"] != false || data["source_bounds"] != nil || data["desktop_x_per_image_pixel"] != nil {
		t.Fatal("invented desktop mapping", data)
	}
	if r.Content[1].(*mcp.ImageContent).MIMEType != "image/jpeg" {
		t.Fatal("portal default not compressed")
	}
}

func TestPortalDisabledImagesAndInvalidArgsNeverPrompt(t *testing.T) {
	for _, images := range []bool{false, true} {
		d := newPortalFixture()
		cfg := config.Default()
		cfg.Images = images
		c, _ := connectDesktop(t, cfg, d)
		args := map[string]any{}
		if images {
			args["monitor_id"] = "unmapped"
		}
		call(t, c, "screen_capture", args, false)
		if d.calls.Load() != 0 {
			t.Fatal("invalid capture opened consent dialog")
		}
	}
}

func TestPortalEmergencyAndStopFileCancelPendingCapture(t *testing.T) {
	for _, stopFile := range []bool{false, true} {
		t.Run(map[bool]string{false: "emergency", true: "stop_file"}[stopFile], func(t *testing.T) {
			d := newPortalFixture()
			d.wait = true
			c, svc := connectProfileDesktop(t, config.Default(), d, "compact")
			done := make(chan error, 1)
			go func() {
				r, err := c.CallTool(context.Background(), &mcp.CallToolParams{Name: "screen_capture", Arguments: map[string]any{}})
				if err == nil && !r.IsError {
					done <- nil
					return
				}
				if err == nil {
					err = context.Canceled
				}
				done <- err
			}()
			select {
			case <-d.started:
			case <-time.After(time.Second):
				t.Fatal("capture did not start")
			}
			if stopFile {
				if err := os.WriteFile(svc.Config.StopFile, []byte("stop"), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				call(t, c, "session_stop", map[string]any{}, true)
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("stopped capture succeeded")
				}
			case <-time.After(time.Second):
				t.Fatal("stop waited for capture timeout")
			}
			if d.calls.Load() != 1 {
				t.Fatal("consent request was repeated")
			}
		})
	}
}
