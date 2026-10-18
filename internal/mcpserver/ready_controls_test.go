package mcpserver

import (
	"context"
	"encoding/json"
	"example.com/codex-vision/internal/observation"
	"example.com/codex-vision/internal/platform"
	"fmt"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"sync/atomic"
	"testing"
	"time"
)

type readyDesktop struct {
	*gestureDesktop
	actionAt   atomic.Int64
	neverBusy  bool
	incomplete bool
}

func (d *readyDesktop) Execute(ctx context.Context, a platform.Action) error {
	err := d.gestureDesktop.Execute(ctx, a)
	if err == nil {
		d.actionAt.Store(time.Now().UnixNano())
	}
	return err
}
func (d *readyDesktop) ReadControls(ctx context.Context, w platform.Window, n int) (platform.ControlSnapshot, error) {
	enabled := true
	at := d.actionAt.Load()
	if at != 0 && !d.neverBusy && time.Since(time.Unix(0, at)) < 650*time.Millisecond {
		enabled = false
	}
	return platform.ControlSnapshot{Controls: []platform.UIControl{{Label: "Choose action", Role: "AXButton", Enabled: &enabled, Bounds: platform.Rect{X: 40, Y: 40, Width: 100, Height: 30}}}, Truncated: d.incomplete, Visited: 3}, ctx.Err()
}
func TestDesktopBatchReadyCycleAndNoFalseVisualReady(t *testing.T) {
	for _, neverBusy := range []bool{false, true} {
		t.Run(fmt.Sprint(neverBusy), func(t *testing.T) {
			d := &readyDesktop{gestureDesktop: &gestureDesktop{workflowDesktop: workflow()}, neverBusy: neverBusy}
			cfg := uiConfig()
			cfg.Images = true
			cfg.Allow = []string{"mouse_click", "keyboard_press"}
			c, _ := connectDesktop(t, cfg, d)
			controls := call(t, c, "ui_controls", map[string]any{"window_id": "mock-window"}, true)
			if len(controls.Content) != 1 {
				t.Fatal("controls returned image")
			}
			start := observeStatus(t, call(t, c, "observation_start", map[string]any{"preview": false, "region": platform.Rect{Width: 320, Height: 180}, "interval_ms": 250}, true))
			r := call(t, c, "desktop_batch", map[string]any{"observation_id": start.ID, "window_id": "mock-window", "watch_region": platform.Rect{Width: 160, Height: 160}, "motion": "rapid", "result_mode": "image", "steps": []map[string]any{{"action": "click", "x": 40, "y": 40}, {"action": "press", "key": "SPACE"}}, "ready_when": map[string]any{"label": "Choose action", "role": "AXButton", "visual_fallback": false}, "settle_ms": 100, "wait_ms": 1600}, true)
			var out struct {
				Interaction observation.ActionWaitResult `json:"interaction"`
				Readiness   readinessReport              `json:"readiness"`
				Completed   int                          `json:"completed_steps"`
			}
			if err := json.Unmarshal([]byte(r.Content[0].(*mcp.TextContent).Text), &out); err != nil {
				t.Fatal(err)
			}
			if out.Completed != 2 || out.Interaction.RetryAction || out.Interaction.ResponseVerified || out.Interaction.Timings.InputMS < 0 || out.Interaction.Timings.TotalMS <= 0 {
				t.Fatal(out)
			}
			if neverBusy {
				if out.Interaction.ReadyConfirmed || !out.Interaction.TimedOut || len(r.Content) != 1 {
					t.Fatal("pixel change falsely verified ready cycle", out)
				}
			} else {
				if !out.Interaction.ReadyConfirmed || !out.Readiness.BusyObserved || !out.Readiness.Matched || len(r.Content) != 2 {
					t.Fatal(out)
				}
			}
		})
	}
}
func TestReadinessPreflightAndExplicitFallback(t *testing.T) {
	d := &gestureDesktop{workflowDesktop: workflow()}
	cfg := uiConfig()
	cfg.Images = true
	cfg.Allow = []string{"mouse_click"}
	c, _ := connectDesktop(t, cfg, d)
	start := observeStatus(t, call(t, c, "observation_start", map[string]any{"preview": false, "region": platform.Rect{Width: 320, Height: 180}, "interval_ms": 250}, true))
	selector := map[string]any{"label": "Choose action", "visual_fallback": false}
	args := map[string]any{"observation_id": start.ID, "window_id": "mock-window", "watch_region": platform.Rect{Width: 160, Height: 160}, "steps": []map[string]any{{"action": "click", "x": 40, "y": 40}}, "ready_when": selector, "settle_ms": 100, "wait_ms": 850}
	call(t, c, "desktop_batch", args, false)
	if len(d.actions()) != 0 {
		t.Fatal("strict unsupported preflight posted input")
	}
	selector["visual_fallback"] = true
	r := call(t, c, "desktop_batch", args, true)
	var out struct {
		Readiness   readinessReport              `json:"readiness"`
		Interaction observation.ActionWaitResult `json:"interaction"`
	}
	json.Unmarshal([]byte(r.Content[0].(*mcp.TextContent).Text), &out)
	if out.Readiness.Mode != "visual_fallback" || out.Interaction.ReadyConfirmed || len(d.actions()) != 1 || len(r.Content) != 2 {
		t.Fatal(out)
	}
}
func TestControlMatcherRejectsIncompleteAmbiguousUnknown(t *testing.T) {
	enabled := true
	control := platform.UIControl{Label: "Ready", Role: "AXButton", Enabled: &enabled}
	selector := ReadyWhen{Label: "Ready"}
	for _, raw := range []platform.ControlSnapshot{{Controls: []platform.UIControl{control}, Truncated: true}, {Controls: []platform.UIControl{control, control}}, {Controls: []platform.UIControl{{Label: "Ready", Role: "AXButton"}}}} {
		if _, _, err := controlReady(raw, selector); err == nil {
			t.Fatal("unsafe readiness match", raw)
		}
	}
}

func TestControlsPreserveFillableSnapshot(t *testing.T) {
	d := &readyDesktop{gestureDesktop: &gestureDesktop{workflowDesktop: workflow()}}
	cfg := uiConfig()
	cfg.Images = true
	c, _ := connectDesktop(t, cfg, d)
	id, _ := snapshotData(t, call(t, c, "ui_snapshot", map[string]any{"window_id": "mock-window"}, true))
	call(t, c, "ui_controls", map[string]any{"window_id": "mock-window"}, true)
	call(t, c, "ui_fill", map[string]any{"snapshot_id": id, "label": "Name", "text": "test"}, true)
	if len(d.actions()) != 1 {
		t.Fatal("controls invalidated field snapshot")
	}
}

type deniedControlsDesktop struct{ *gestureDesktop }

func (d *deniedControlsDesktop) ReadControls(context.Context, platform.Window, int) (platform.ControlSnapshot, error) {
	return platform.ControlSnapshot{}, fmt.Errorf("accessibility_permission_denied")
}
func TestReadyFallbackDoesNotHidePermissionFailure(t *testing.T) {
	d := &deniedControlsDesktop{gestureDesktop: &gestureDesktop{workflowDesktop: workflow()}}
	cfg := uiConfig()
	cfg.Images = true
	cfg.Allow = []string{"mouse_click"}
	c, _ := connectDesktop(t, cfg, d)
	start := observeStatus(t, call(t, c, "observation_start", map[string]any{"preview": false}, true))
	call(t, c, "desktop_batch", map[string]any{"observation_id": start.ID, "window_id": "mock-window", "watch_region": platform.Rect{Width: 160, Height: 160}, "steps": []map[string]any{{"action": "click", "x": 40, "y": 40}}, "ready_when": map[string]any{"label": "Ready", "visual_fallback": true}}, false)
	if len(d.actions()) != 0 {
		t.Fatal("permission failure bypassed via fallback")
	}
}
