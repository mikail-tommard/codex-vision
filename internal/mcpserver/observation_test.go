package mcpserver

import (
	"context"
	"encoding/json"
	"example.com/codex-vision/internal/config"
	"example.com/codex-vision/internal/observation"
	"example.com/codex-vision/internal/platform"
	"example.com/codex-vision/internal/platform/mock"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"image"
	"sync/atomic"
	"testing"
	"time"
)

type changingDesktop struct {
	mock.Desktop
	value atomic.Uint32
}

func (d *changingDesktop) Capture(ctx context.Context, r platform.Rect) (image.Image, error) {
	im := image.NewRGBA(image.Rect(0, 0, r.Width, r.Height))
	v := uint8(d.value.Load())
	for i := 0; i < len(im.Pix); i += 4 {
		im.Pix[i] = v
		im.Pix[i+3] = 255
	}
	return im, ctx.Err()
}
func observeStatus(t *testing.T, r *mcp.CallToolResult) observation.Status {
	t.Helper()
	var out struct {
		Observation observation.Status `json:"observation"`
	}
	if err := json.Unmarshal([]byte(r.Content[0].(*mcp.TextContent).Text), &out); err != nil {
		t.Fatal(err)
	}
	return out.Observation
}
func TestObservationMCPPulls(t *testing.T) {
	d := &changingDesktop{}
	cfg := config.Default()
	cfg.MaxPerMinute = 100
	c, svc := connectDesktop(t, cfg, d)
	r := call(t, c, "observation_start", map[string]any{"preview": false, "interval_ms": 250, "min_delivery_ms": 250}, true)
	s := observeStatus(t, r)
	if s.StabilityMS != 600 || s.Mode != "task" || s.State != "running" || len(r.Content) != 2 {
		t.Fatal(s, r.Content)
	}
	if _, ok := r.Content[1].(*mcp.ImageContent); !ok {
		t.Fatal("missing first image")
	}
	call(t, c, "observation_start", map[string]any{"preview": false}, false)
	before := svc.total
	time.Sleep(300 * time.Millisecond)
	if svc.total != before {
		t.Fatal("background capture consumed MCP read budget")
	}
	r = call(t, c, "observation_next", map[string]any{"observation_id": s.ID, "after_sequence": s.Sequence, "wait_ms": 0}, true)
	if len(r.Content) != 1 {
		t.Fatal("unchanged returned image")
	}
	d.value.Store(170)
	// Long polling must not hold the capture semaphore; background capture must advance.
	r = call(t, c, "observation_next", map[string]any{"observation_id": s.ID, "after_sequence": s.Sequence, "wait_ms": 1500}, true)
	next := observeStatus(t, r)
	if next.Sequence <= s.Sequence || len(r.Content) != 2 {
		t.Fatal("long poll blocked capture", next)
	}
	call(t, c, "observation_control", map[string]any{"action": "pause", "observation_id": s.ID}, true)
	call(t, c, "observation_control", map[string]any{"action": "resume", "observation_id": s.ID}, true)
	call(t, c, "session_stop", map[string]any{}, true)
	if svc.Observer.Status().State != "stopped" || len(d.Actions) != 0 {
		t.Fatal("stop did not release observer or observation performed input")
	}
	call(t, c, "observation_start", map[string]any{"preview": false}, false)
}
func TestObservationRejectsInvalidOptions(t *testing.T) {
	c, _ := connect(t, config.Default())
	for _, a := range []map[string]any{
		{"stability_ms": -1}, {"stability_ms": 5001}, {"mode": "forever"}, {"interval_ms": 10}, {"min_delivery_ms": 1}, {"change_threshold": 1.1}, {"max_width": 2000}, {"quality": 101}, {"task_idle_timeout_sec": 1},
		{"monitor_id": "missing"}, {"monitor_id": "mock-primary", "region": map[string]any{"x": 0, "y": 0, "width": 10, "height": 10}},
		{"region": map[string]any{"x": 2000, "y": 0, "width": 10, "height": 10}},
	} {
		a["preview"] = false
		call(t, c, "observation_start", a, false)
	}
	call(t, c, "observation_next", map[string]any{"observation_id": "missing", "wait_ms": 0}, false)
	call(t, c, "observation_control", map[string]any{"action": "status"}, true)
}

func TestObservationCanDisableTemporalFilter(t *testing.T) {
	c, _ := connect(t, config.Default())
	r := call(t, c, "observation_start", map[string]any{"preview": false, "stability_ms": 0}, true)
	if observeStatus(t, r).StabilityMS != 0 {
		t.Fatal("explicit zero ignored")
	}
}

func TestObservationDiagnosticPlacementAndOptOut(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(map[bool]string{true: "on", false: "off"}[enabled], func(t *testing.T) {
			c, _ := connect(t, config.Default())
			r := call(t, c, "observation_start", map[string]any{"preview": false, "diagnostics": enabled, "interval_ms": 10000}, true)
			var out struct {
				Observation observation.Status `json:"observation"`
				Frame       *observation.Frame `json:"frame"`
			}
			if e := json.Unmarshal([]byte(r.Content[0].(*mcp.TextContent).Text), &out); e != nil {
				t.Fatal(e)
			}
			if len(r.Content) != 2 || out.Frame == nil || out.Observation.LastDetection != nil {
				t.Fatal("diagnostic duplicated in image response", out)
			}
			if (out.Frame.Diagnostics != nil) != enabled {
				t.Fatal("frame diagnostic toggle ignored")
			}
			r = call(t, c, "observation_next", map[string]any{"observation_id": out.Observation.ID, "after_sequence": out.Frame.Sequence, "wait_ms": 0}, true)
			if len(r.Content) != 1 {
				t.Fatal("metadata added an image")
			}
			status := observeStatus(t, r)
			if (status.LastDetection != nil) != enabled {
				t.Fatal("missing last detector decision", status)
			}
		})
	}
}
