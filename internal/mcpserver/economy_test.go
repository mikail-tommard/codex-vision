package mcpserver

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"example.com/codex-vision/internal/observation"
	"example.com/codex-vision/internal/platform"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestReadyAutoTextRetainsUnseenPixels(t *testing.T) {
	d := &readyDesktop{gestureDesktop: &gestureDesktop{workflowDesktop: workflow()}}
	cfg := uiConfig()
	cfg.Images = true
	cfg.Allow = []string{"mouse_click"}
	c, svc := connectDesktop(t, cfg, d)
	start := observeStatus(t, call(t, c, "observation_start", map[string]any{"preview": false, "region": platform.Rect{Width: 320, Height: 180}, "interval_ms": 250, "min_delivery_ms": 250}, true))
	r := call(t, c, "desktop_batch", map[string]any{
		"observation_id": start.ID, "window_id": "mock-window",
		"watch_region": platform.Rect{Width: 160, Height: 160},
		"steps":        []map[string]any{{"action": "click", "x": 40, "y": 40}},
		"ready_when":   map[string]any{"label": "Choose action", "visual_fallback": false},
		"settle_ms":    100, "wait_ms": 2000,
	}, true)
	var out struct {
		Interaction  observation.ActionWaitResult `json:"interaction"`
		Readiness    readinessReport              `json:"readiness"`
		Continuation struct {
			Next string `json:"next"`
		} `json:"continuation"`
	}
	if err := json.Unmarshal([]byte(r.Content[0].(*mcp.TextContent).Text), &out); err != nil {
		t.Fatal(err)
	}
	if len(r.Content) != 1 || !out.Interaction.ReadyConfirmed || out.Readiness.Label != "Choose action" || out.Continuation.Next != "use_ui_result" || svc.Observer.Status().ImagesReturned != 1 {
		t.Fatal("expected one text result and no encoded model frame", out, len(r.Content), svc.Observer.Status())
	}
	// Text proves only readiness. It must not consume the unseen visual state.
	_, f, err := svc.Observer.Next(context.Background(), start.ID, start.Sequence, time.Second)
	if err != nil || f == nil {
		t.Fatal("unseen pixels lost", err)
	}
}

type responseDesktop struct{ *gestureDesktop }

func (d *responseDesktop) Execute(ctx context.Context, a platform.Action) error {
	if err := d.gestureDesktop.Execute(ctx, a); err != nil {
		return err
	}
	go func() { time.Sleep(900 * time.Millisecond); d.phase.Store(170) }()
	return nil
}

func TestBatchResponseWaitAndNoDuplicateFrame(t *testing.T) {
	d := &responseDesktop{gestureDesktop: &gestureDesktop{workflowDesktop: workflow()}}
	cfg := uiConfig()
	cfg.Images = true
	cfg.Allow = []string{"mouse_click"}
	c, svc := connectDesktop(t, cfg, d)
	start := observeStatus(t, call(t, c, "observation_start", map[string]any{"preview": false, "region": platform.Rect{Width: 320, Height: 180}, "interval_ms": 250, "min_delivery_ms": 250}, true))
	r := call(t, c, "desktop_batch", map[string]any{
		"observation_id": start.ID, "window_id": "mock-window", "wait_for": "response",
		"watch_region": platform.Rect{Width: 160, Height: 160},
		"steps":        []map[string]any{{"action": "click", "x": 40, "y": 40}},
		"settle_ms":    100, "wait_ms": 2600,
	}, true)
	var out struct {
		Interaction  observation.ActionWaitResult `json:"interaction"`
		Frame        *observation.Frame           `json:"frame"`
		Continuation struct {
			Next string `json:"next"`
		} `json:"continuation"`
	}
	if err := json.Unmarshal([]byte(r.Content[0].(*mcp.TextContent).Text), &out); err != nil {
		t.Fatal(err)
	}
	if !out.Interaction.SubsequentChangeObserved || out.Interaction.ResponseVerified || out.Frame == nil || out.Continuation.Next != "inspect_result" || len(r.Content) != 2 || len(d.actions()) != 1 || svc.Observer.Status().ImagesReturned != 2 {
		t.Fatal(out, len(r.Content), svc.Observer.Status())
	}
	_, f, err := svc.Observer.Next(context.Background(), start.ID, out.Frame.Sequence, 800*time.Millisecond)
	if err != nil || f != nil {
		t.Fatal("duplicate frame after action", err)
	}
}

func TestContinuationDoesNotSuggestReplay(t *testing.T) {
	for _, r := range []observation.ActionWaitResult{{Posted: true, TimedOut: true}, {MayHaveChanged: true}} {
		c := actionContinuation(r, false, nil)
		if c["repeat_action"] != false || c["immediate_observation_needed"] != false {
			t.Fatal(c)
		}
	}
}

func TestMouseTimeoutDefaultsToText(t *testing.T) {
	d := &gestureDesktop{workflowDesktop: workflow()}
	cfg := uiConfig()
	cfg.Images = true
	cfg.Allow = []string{"mouse_drag"}
	c, svc := connectDesktop(t, cfg, d)
	start := observeStatus(t, call(t, c, "observation_start", map[string]any{"preview": false, "region": platform.Rect{Width: 320, Height: 180}, "interval_ms": 250, "min_delivery_ms": 250}, true))
	r := call(t, c, "mouse_action_wait", map[string]any{
		"observation_id": start.ID, "window_id": "mock-window", "x": 40, "y": 40, "end_x": 80, "end_y": 80,
		"watch_region": platform.Rect{Width: 160, Height: 160}, "settle_ms": 100, "wait_ms": 850,
	}, true)
	var out struct {
		Interaction  observation.ActionWaitResult `json:"interaction"`
		Continuation struct {
			Next string `json:"next"`
		} `json:"continuation"`
	}
	if err := json.Unmarshal([]byte(r.Content[0].(*mcp.TextContent).Text), &out); err != nil {
		t.Fatal(err)
	}
	if !out.Interaction.TimedOut || !out.Interaction.Posted || len(r.Content) != 1 || out.Continuation.Next != "waiting" || svc.Observer.Status().ImagesReturned != 1 || len(d.actions()) != 1 {
		t.Fatal(out, len(r.Content), svc.Observer.Status())
	}
}
