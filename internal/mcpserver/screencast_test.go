package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"example.com/codex-vision/internal/config"
	"example.com/codex-vision/internal/platform"
	"example.com/codex-vision/internal/screencast"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type castMockSource struct {
	frames chan image.Image
	closed chan struct{}
	once   sync.Once
}

func (s *castMockSource) Done() <-chan struct{} { return s.closed }
func (s *castMockSource) Close() error          { s.once.Do(func() { close(s.closed) }); return nil }
func (s *castMockSource) Next(ctx context.Context) (image.Image, error) {
	select {
	case im := <-s.frames:
		return im, nil
	case <-s.closed:
		return nil, fmt.Errorf("closed")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type castDesktop struct {
	*portalDesktop
	source         *castMockSource
	starts         atomic.Int64
	deny           bool
	waitConsent    bool
	consentStarted chan struct{}
}

func castFixture() *castDesktop {
	return &castDesktop{portalDesktop: newPortalFixture(), source: &castMockSource{frames: make(chan image.Image, 8), closed: make(chan struct{})}, consentStarted: make(chan struct{})}
}
func (d *castDesktop) StartScreenCast(ctx context.Context) (platform.ScreenCastSource, error) {
	d.starts.Add(1)
	close(d.consentStarted)
	if d.waitConsent {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if d.deny {
		return nil, platform.ErrCaptureDenied
	}
	return d.source, nil
}
func decodeStream(t *testing.T, data map[string]any) screencast.Status {
	t.Helper()
	raw, err := json.Marshal(data["stream"])
	if err != nil {
		t.Fatal(err)
	}
	var status screencast.Status
	if err := json.Unmarshal(raw, &status); err != nil {
		t.Fatal(err)
	}
	return status
}

func TestScreenCastCompactPullDedupAndStopBypassesReadBudget(t *testing.T) {
	d := castFixture()
	cfg := config.Default()
	cfg.MaxPerTask = 2
	c, svc := connectProfileDesktop(t, cfg, d, "compact")
	listed, err := c.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Tools) != 4 {
		t.Fatal("cast catalog contains unrelated tools", len(listed.Tools))
	}
	d.source.frames <- image.NewRGBA(image.Rect(0, 0, 320, 180))
	first := call(t, c, "screencast_control", map[string]any{"action": "start", "duration_sec": 30, "max_frames": 10, "wait_ms": 1000}, true)
	data := fieldsData(t, first)
	status := decodeStream(t, data)
	if len(first.Content) != 2 || data["coordinate_mapping_available"] != false || data["desktop_x_per_image_pixel"] != nil {
		t.Fatal(data)
	}
	d.source.frames <- image.NewRGBA(image.Rect(0, 0, 320, 180))
	next := call(t, c, "screencast_control", map[string]any{"action": "next", "stream_id": status.ID, "after_sequence": status.Sequence, "wait_ms": 20}, true)
	if len(next.Content) != 1 || fieldsData(t, next)["changed"] != false || d.starts.Load() != 1 {
		t.Fatal("duplicate image/consent", fieldsData(t, next))
	}
	call(t, c, "screencast_control", map[string]any{"action": "next", "stream_id": status.ID}, false)
	call(t, c, "screencast_control", map[string]any{"action": "stop", "stream_id": status.ID}, true)
	select {
	case <-d.source.closed:
	case <-time.After(time.Second):
		t.Fatal("stop leaked native source")
	}
	if svc.Cast.Status().State != "stopped" {
		t.Fatal(svc.Cast.Status())
	}
}

func TestScreenCastRejectsMissingBudgetsDisabledImagesAndConsentRetry(t *testing.T) {
	d := castFixture()
	d.deny = true
	c, _ := connectProfileDesktop(t, config.Default(), d, "compact")
	call(t, c, "screencast_control", map[string]any{"action": "start"}, false)
	call(t, c, "screencast_control", map[string]any{"action": "start", "duration_sec": 301, "max_frames": 10}, false)
	call(t, c, "screencast_control", map[string]any{"action": "start", "duration_sec": 30, "max_frames": 10, "wait_ms": 1000}, true)
	if d.starts.Load() != 1 {
		t.Fatal("consent was repeated")
	}
	disabled := castFixture()
	cfg := config.Default()
	cfg.Images = false
	c2, _ := connectProfileDesktop(t, cfg, disabled, "compact")
	call(t, c2, "screencast_control", map[string]any{"action": "start", "duration_sec": 30, "max_frames": 10}, false)
	if disabled.starts.Load() != 0 {
		t.Fatal("disabled images opened consent")
	}
}

func TestScreenCastEmergencyAndStopFileCancelPendingConsent(t *testing.T) {
	for _, stopFile := range []bool{false, true} {
		t.Run(fmt.Sprint(stopFile), func(t *testing.T) {
			d := castFixture()
			d.waitConsent = true
			c, svc := connectProfileDesktop(t, config.Default(), d, "compact")
			call(t, c, "screencast_control", map[string]any{"action": "start", "duration_sec": 30, "max_frames": 10}, true)
			select {
			case <-d.consentStarted:
			case <-time.After(time.Second):
				t.Fatal("consent not started")
			}
			if stopFile {
				if err := os.WriteFile(svc.Config.StopFile, []byte("stop"), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				call(t, c, "session_stop", map[string]any{}, true)
			}
			deadline := time.Now().Add(time.Second)
			for time.Now().Before(deadline) {
				if svc.Cast.Status().State == "stopped" {
					return
				}
				time.Sleep(time.Millisecond)
			}
			t.Fatal("consent survived stop", svc.Cast.Status())
		})
	}
}

type castEconomyDesktop struct {
	*castDesktop
	shots atomic.Int64
}

func castEconomyImage(index int) image.Image {
	values := []uint8{10, 10, 10, 90, 90}
	im := image.NewNRGBA(image.Rect(0, 0, 320, 180))
	for y := 0; y < 180; y++ {
		for x := 0; x < 320; x++ {
			im.SetNRGBA(x, y, color.NRGBA{R: values[index], G: 20, B: 30, A: 255})
		}
	}
	return im
}
func (d *castEconomyDesktop) Screenshot(ctx context.Context) (image.Image, error) {
	return castEconomyImage(int(d.shots.Add(1)) - 1), ctx.Err()
}

func TestScreenCastPairedWireAndImageEconomy(t *testing.T) {
	measurements := map[string]map[string]int{}
	for _, strategy := range []string{"single_shots", "stream"} {
		d := &castEconomyDesktop{castDesktop: castFixture()}
		cfg := config.Default()
		cfg.MaxPerMinute = 0
		cfg.MaxPerTask = 0
		c, svc := connectProfileDesktop(t, cfg, d, "compact")
		count, wire, imageBytes := 0, 0, 0
		id := ""
		after := uint64(0)
		for i := 0; i < 5; i++ {
			var out *mcp.CallToolResult
			if strategy == "single_shots" {
				out = call(t, c, "screen_capture", map[string]any{}, true)
			} else {
				d.source.frames <- castEconomyImage(i)
				if i == 0 {
					out = call(t, c, "screencast_control", map[string]any{"action": "start", "duration_sec": 30, "max_frames": 20, "wait_ms": 1000}, true)
					id = decodeStream(t, fieldsData(t, out)).ID
				} else {
					deadline := time.Now().Add(time.Second)
					for svc.Cast.Status().Sampled < i+1 && time.Now().Before(deadline) {
						time.Sleep(time.Millisecond)
					}
					if svc.Cast.Status().Sampled != i+1 {
						t.Fatal("sample did not arrive")
					}
					out = call(t, c, "screencast_control", map[string]any{"action": "next", "stream_id": id, "after_sequence": after}, true)
				}
				data := fieldsData(t, out)
				if frame, ok := data["frame"].(map[string]any); ok {
					after = uint64(frame["sequence"].(float64))
				}
			}
			encoded, err := json.Marshal(out)
			if err != nil {
				t.Fatal(err)
			}
			wire += len(encoded)
			for _, content := range out.Content {
				if im, ok := content.(*mcp.ImageContent); ok {
					count++
					imageBytes += len(im.Data)
				}
			}
		}
		measurements[strategy] = map[string]int{"images": count, "result_bytes": wire, "image_bytes": imageBytes, "single_shot_requests": int(d.shots.Load()), "stream_sessions": int(d.starts.Load())}
		if strategy == "stream" {
			call(t, c, "screencast_control", map[string]any{"action": "stop", "stream_id": id}, true)
		}
	}
	before, after := measurements["single_shots"], measurements["stream"]
	if before["images"] != 5 || after["images"] != 2 || after["result_bytes"] >= before["result_bytes"] || after["stream_sessions"] != 1 || before["single_shot_requests"] != 5 {
		t.Fatal(measurements)
	}
	data, err := json.Marshal(measurements)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("CAST_ECONOMY_RESULT %s", data)
}

func TestScreenCastCancelledStartRequestDoesNotLeaveConsentRunning(t *testing.T) {
	d := castFixture()
	d.waitConsent = true
	c, svc := connectProfileDesktop(t, config.Default(), d, "compact")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = c.CallTool(ctx, &mcp.CallToolParams{Name: "screencast_control", Arguments: map[string]any{"action": "start", "duration_sec": 30, "max_frames": 10, "wait_ms": 4000}})
	}()
	select {
	case <-d.consentStarted:
	case <-time.After(time.Second):
		t.Fatal("consent not started")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancelled request did not return")
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if svc.Cast.Status().State == "stopped" {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("cancelled start left consent active", svc.Cast.Status())
}
