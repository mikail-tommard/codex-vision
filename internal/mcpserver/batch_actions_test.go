package mcpserver

import (
	"context"
	"encoding/json"
	"example.com/codex-vision/internal/observation"
	"example.com/codex-vision/internal/platform"
	"fmt"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"testing"
	"time"
)

func TestDesktopBatchSingleImageAndPartialFailure(t *testing.T) {
	for _, mode := range []string{"success", "timeout", "partial", "invalid"} {
		t.Run(mode, func(t *testing.T) {
			d := &gestureDesktop{workflowDesktop: workflow(), failSecond: mode == "partial"}
			cfg := uiConfig()
			cfg.Images = true
			cfg.Allow = []string{"mouse_click", "mouse_move", "mouse_scroll", "keyboard_press", "keyboard_type"}
			c, svc := connectDesktop(t, cfg, d)
			started := call(t, c, "observation_start", map[string]any{"preview": false, "mode": "continuous", "region": platform.Rect{Width: 320, Height: 180}, "interval_ms": 250}, true)
			var start struct {
				Observation observation.Status `json:"observation"`
			}
			if err := json.Unmarshal([]byte(started.Content[0].(*mcp.TextContent).Text), &start); err != nil {
				t.Fatal(err)
			}
			steps := []map[string]any{{"action": "click", "x": 40, "y": 40}, {"action": "scroll", "delta": -120}}
			if mode == "timeout" {
				d.phase.Store(80)
			} // Final pixels are already in the pre-action baseline.
			if mode == "invalid" {
				steps[1]["delta"] = -125
			}
			args := map[string]any{"observation_id": start.Observation.ID, "window_id": "mock-window", "steps": steps, "watch_region": platform.Rect{Width: 160, Height: 160}, "settle_ms": 100, "wait_ms": 850}
			r := call(t, c, "desktop_batch", args, mode == "success" || mode == "timeout")
			if mode == "invalid" {
				if d.executed.Load() != 0 {
					t.Fatal("preflight posted input")
				}
				return
			}
			var out struct {
				Interaction observation.ActionWaitResult `json:"interaction"`
				Completed   int                          `json:"completed_steps"`
				Frame       *observation.Frame           `json:"frame"`
			}
			if err := json.Unmarshal([]byte(r.Content[0].(*mcp.TextContent).Text), &out); err != nil {
				t.Fatal(err)
			}
			if out.Interaction.RetryAction {
				t.Fatal("unexpected retry", out)
			}
			switch mode {
			case "success":
				if out.Completed != 2 || out.Frame == nil || len(r.Content) != 2 || out.Interaction.Stage != "stable_change" {
					t.Fatal(out)
				}
			case "timeout":
				if out.Completed != 2 || out.Frame != nil || len(r.Content) != 1 || !out.Interaction.TimedOut {
					t.Fatal(out)
				}
				if svc.Observer.Status().ImagesReturned != 1 {
					t.Fatal("timeout returned image")
				}
			case "partial":
				if out.Completed != 1 || out.Frame != nil || !out.Interaction.MayHaveChanged || d.executed.Load() != 2 {
					t.Fatal(out)
				}
			}
		})
	}
}

func TestDesktopBatchSchemaAndNoImplicitSubmit(t *testing.T) {
	d := &gestureDesktop{workflowDesktop: workflow()}
	cfg := uiConfig()
	cfg.Images = true
	cfg.Allow = []string{"keyboard_type"}
	c, _ := connectDesktop(t, cfg, d)
	tools, e := c.ListTools(context.Background(), nil)
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, tool := range tools.Tools {
		if tool.Name == "desktop_batch" {
			raw, _ := json.Marshal(tool.InputSchema)
			var schema map[string]any
			json.Unmarshal(raw, &schema)
			props := schema["properties"].(map[string]any)
			steps := props["steps"].(map[string]any)
			if steps["maxItems"] != float64(12) {
				t.Fatal(string(raw))
			}
			found = true
		}
	}
	if !found {
		t.Fatal("missing batch tool")
	}
	r := call(t, c, "observation_start", map[string]any{"preview": false, "region": platform.Rect{Width: 320, Height: 180}, "interval_ms": 250}, true)
	var start struct {
		Observation observation.Status `json:"observation"`
	}
	json.Unmarshal([]byte(r.Content[0].(*mcp.TextContent).Text), &start)
	call(t, c, "desktop_batch", map[string]any{"observation_id": start.Observation.ID, "window_id": "mock-window", "steps": []map[string]any{{"action": "type", "text": "Тест ввода"}}, "watch_region": platform.Rect{Width: 160, Height: 160}, "settle_ms": 100, "wait_ms": 850}, true)
	acts := d.actions()
	if len(acts) != 1 || acts[0].Tool != "keyboard_type" || acts[0].Text != "Тест ввода" {
		t.Fatal(fmt.Sprint(acts))
	}
}

type interruptibleGestureDesktop struct {
	*gestureDesktop
	entered chan struct{}
}

func (d *interruptibleGestureDesktop) Execute(ctx context.Context, a platform.Action) error {
	close(d.entered)
	<-ctx.Done()
	return ctx.Err()
}
func TestDesktopBatchStopInterruptsNativeAction(t *testing.T) {
	d := &interruptibleGestureDesktop{gestureDesktop: &gestureDesktop{workflowDesktop: workflow()}, entered: make(chan struct{})}
	cfg := uiConfig()
	cfg.Images = true
	cfg.Allow = []string{"mouse_click"}
	c, _ := connectDesktop(t, cfg, d)
	r := call(t, c, "observation_start", map[string]any{"preview": false, "region": platform.Rect{Width: 320, Height: 180}, "interval_ms": 250}, true)
	var start struct {
		Observation observation.Status `json:"observation"`
	}
	json.Unmarshal([]byte(r.Content[0].(*mcp.TextContent).Text), &start)
	pending := asyncCall(c, "desktop_batch", map[string]any{"observation_id": start.Observation.ID, "window_id": "mock-window", "watch_region": platform.Rect{Width: 160, Height: 160}, "steps": []map[string]any{{"action": "click", "x": 40, "y": 40}, {"action": "click", "x": 80, "y": 80}}, "wait_ms": 850})
	select {
	case <-d.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("native action not started")
	}
	call(t, c, "session_stop", map[string]any{}, true)
	reply := awaitReply(t, pending)
	if !reply.r.IsError || len(d.actions()) != 0 || len(reply.r.Content) != 1 {
		t.Fatal("stop did not abort batch without image", reply.r)
	}
}
