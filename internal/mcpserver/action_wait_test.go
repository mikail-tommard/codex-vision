package mcpserver

import (
	"context"
	"encoding/json"
	"example.com/codex-vision/internal/observation"
	"example.com/codex-vision/internal/platform"
	"fmt"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"image"
	"image/color"
	"sync/atomic"
	"testing"
)

type gestureDesktop struct {
	*workflowDesktop
	phase      atomic.Uint32
	failSecond bool
	executed   atomic.Int32
}

func (d *gestureDesktop) Capture(ctx context.Context, r platform.Rect) (image.Image, error) {
	im := image.NewRGBA(image.Rect(0, 0, r.Width, r.Height))
	value := uint8(d.phase.Load())
	for y := 0; y < r.Height; y++ {
		for x := 0; x < r.Width; x++ {
			im.SetRGBA(x, y, color.RGBA{value, value, value, 255})
		}
	}
	return im, ctx.Err()
}
func (d *gestureDesktop) Execute(ctx context.Context, a platform.Action) error {
	n := d.executed.Add(1)
	if d.failSecond && n == 2 {
		return fmt.Errorf("second click failed")
	}
	if err := d.workflowDesktop.Execute(ctx, a); err != nil {
		return err
	}
	d.phase.Store(80)
	return nil
}
func TestMouseActionWaitMCP(t *testing.T) {
	for _, pair := range []bool{false, true} {
		t.Run(fmt.Sprint(pair), func(t *testing.T) {
			d := &gestureDesktop{workflowDesktop: workflow(), failSecond: pair}
			cfg := uiConfig()
			cfg.Images = true
			cfg.Allow = []string{"mouse_drag", "mouse_click"}
			c, _ := connectDesktop(t, cfg, d)
			r := call(t, c, "observation_start", map[string]any{"preview": false, "mode": "continuous", "region": platform.Rect{Width: 320, Height: 180}, "interval_ms": 250}, true)
			var start struct {
				Observation observation.Status `json:"observation"`
			}
			if err := json.Unmarshal([]byte(r.Content[0].(*mcp.TextContent).Text), &start); err != nil {
				t.Fatal(err)
			}
			args := map[string]any{"observation_id": start.Observation.ID, "window_id": "mock-window", "x": 40, "y": 40, "end_x": 80, "end_y": 80, "image_on_timeout": true, "watch_region": platform.Rect{Width: 160, Height: 160}, "settle_ms": 100, "wait_ms": 650}
			if pair {
				args["action"] = "click_pair"
			}
			r = call(t, c, "mouse_action_wait", args, !pair)
			var out struct {
				Interaction observation.ActionWaitResult `json:"interaction"`
				Completed   int                          `json:"native_steps_completed"`
				Frame       *observation.Frame           `json:"frame"`
			}
			if err := json.Unmarshal([]byte(r.Content[0].(*mcp.TextContent).Text), &out); err != nil {
				t.Fatal(err)
			}
			actions := d.actions()
			if len(actions) != 1 || out.Completed != 1 || out.Interaction.RetryAction {
				t.Fatal(out, actions)
			}
			if pair {
				if out.Interaction.Posted || !out.Interaction.MayHaveChanged || out.Frame != nil {
					t.Fatal(out)
				}
			} else {
				if actions[0].Tool != "mouse_drag" || !out.Interaction.Posted || !out.Interaction.TimedOut || out.Frame == nil || len(r.Content) != 2 {
					t.Fatal(out)
				}
			}
			// An out-of-region endpoint must cause no additional native input.
			args["end_x"] = 200
			call(t, c, "mouse_action_wait", args, false)
			if len(d.actions()) != 1 {
				t.Fatal("invalid gesture posted")
			}
			// Reusing a stored scope after the window moves requires a fresh selection.
			delete(args, "watch_region")
			args["end_x"] = 80
			d.stateMu.Lock()
			d.window.Bounds.X = 10
			d.stateMu.Unlock()
			call(t, c, "mouse_action_wait", args, false)
			if len(d.actions()) != 1 {
				t.Fatal("stale geometry posted")
			}
		})
	}
}
