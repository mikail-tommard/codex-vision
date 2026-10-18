package mcpserver

import (
	"context"
	"example.com/codex-vision/internal/input"
	"example.com/codex-vision/internal/observation"
	"example.com/codex-vision/internal/platform"
	"fmt"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"time"
)

type MouseActionWaitArgs struct {
	ImageOnTimeout bool           `json:"image_on_timeout,omitempty" jsonschema:"Default false: timeout returns text only; true explicitly requests one diagnostic frame"`
	ID             string         `json:"observation_id"`
	WindowID       string         `json:"window_id"`
	X              int            `json:"x"`
	Y              int            `json:"y"`
	EndX           int            `json:"end_x"`
	EndY           int            `json:"end_y"`
	Action         string         `json:"action,omitempty" jsonschema:"drag (default) or click_pair; execute once, never retry automatically"`
	Motion         string         `json:"motion,omitempty"`
	DurationMS     int            `json:"duration_ms,omitempty"`
	SettleMS       int            `json:"settle_ms,omitempty" jsonschema:"Stable image duration 100..2000ms; default 350"`
	WaitMS         int            `json:"wait_ms,omitempty" jsonschema:"Local wait 100..30000ms; default up to 25000"`
	WatchRegion    *platform.Rect `json:"watch_region,omitempty" jsonschema:"Task area chosen from initial image; saved for subsequent calls, excludes clocks and unrelated UI"`
	ReadyRegion    *platform.Rect `json:"ready_region,omitempty" jsonschema:"Optional static turn/ready indicator; exclude changing clock digits"`
}

func (v *Service) attentionWindow(ctx context.Context, id string) (platform.Window, error) {
	if id == "" {
		return platform.Window{}, fmt.Errorf("window_id is required")
	}
	w, err := v.Desktop.Active(ctx)
	if err != nil {
		return w, err
	}
	if w.ID != id {
		return w, fmt.Errorf("expected foreground window %s; stop input", id)
	}
	return w, nil
}
func (v *Service) registerActionWait(s *mcp.Server) {
	bind(s, v, "mouse_action_wait", "Execute one window-bound drag (or click pair), wait locally for stable visual changes, return one task-region image. Select watch_region once from the initial image; full preview stays unchanged. Visual readiness is heuristic, never proof of opponent response. On timeout return metadata only by default; image_on_timeout=true explicitly requests a diagnostic frame. Never repeat the action automatically.", false, v.mouseActionWait)
}
func (v *Service) mouseActionWait(ctx context.Context, a MouseActionWaitArgs) (*mcp.CallToolResult, error) {
	if !v.Config.Images {
		return nil, fmt.Errorf("images disabled locally")
	}
	if a.Action == "" {
		a.Action = "drag"
	}
	if a.Action != "drag" && a.Action != "click_pair" {
		return nil, fmt.Errorf("action must be drag or click_pair")
	}
	if a.DurationMS == 0 {
		a.DurationMS = 200
	}
	if a.DurationMS < 10 || a.DurationMS > 5000 {
		return nil, fmt.Errorf("duration_ms must be 10..5000")
	}
	if a.SettleMS == 0 {
		a.SettleMS = 350
	}
	if a.SettleMS < 100 || a.SettleMS > 2000 {
		return nil, fmt.Errorf("settle_ms must be 100..2000")
	}
	if a.WaitMS == 0 {
		a.WaitMS = min(25000, max(100, v.Config.TimeoutMS-2000))
	}
	if a.WaitMS < 100 || a.WaitMS > 30000 {
		return nil, fmt.Errorf("wait_ms must be 100..30000")
	}
	if _, err := input.ApproachDuration(a.Motion, v.Config.CursorMode); err != nil {
		return nil, err
	}
	w, err := v.attentionWindow(ctx, a.WindowID)
	if err != nil {
		return nil, err
	}
	region, ready, pinned, _, err := v.Observer.FocusState(a.ID)
	if err != nil {
		return nil, err
	}
	if a.WatchRegion != nil {
		region = a.WatchRegion
		ready = a.ReadyRegion
	} else if pinned != nil && (pinned.ID != w.ID || pinned.Process != w.Process || pinned.Bounds != w.Bounds) {
		return nil, fmt.Errorf("pinned window changed; inspect image and select watch_region again")
	}
	if a.ReadyRegion != nil {
		ready = a.ReadyRegion
	}
	if region == nil {
		return nil, fmt.Errorf("select watch_region from initial image first")
	}
	if !region.Contains(a.X, a.Y) || !region.Contains(a.EndX, a.EndY) {
		return nil, fmt.Errorf("both gesture endpoints must be inside watch_region")
	}
	focus, err := v.Observer.Focus(a.ID, *region, ready, &w)
	if err != nil {
		return nil, err
	}
	steps := []platform.Action{{Tool: "mouse_drag", WindowID: w.ID, X: a.X, Y: a.Y, EndX: a.EndX, EndY: a.EndY, Button: "left", Motion: a.Motion, Duration: time.Duration(a.DurationMS) * time.Millisecond}}
	if a.Action == "click_pair" {
		steps = []platform.Action{{Tool: "mouse_click", WindowID: w.ID, X: a.X, Y: a.Y, Button: "left", Count: 1, Motion: a.Motion}, {Tool: "mouse_click", WindowID: w.ID, X: a.EndX, Y: a.EndY, Button: "left", Count: 1, Motion: a.Motion}}
	}
	// Validate all coordinates before the first event, including monitor gaps.
	monitors, err := v.Desktop.Monitors(ctx)
	if err != nil {
		return nil, err
	}
	bounds, err := platform.Bounds(monitors)
	if err != nil {
		return nil, err
	}
	onMonitor := func(x, y int) bool {
		for _, m := range monitors {
			if m.Bounds.Contains(x, y) {
				return true
			}
		}
		return false
	}
	for _, step := range steps {
		if err = input.Validate(step, bounds); err != nil {
			return nil, err
		}
	}
	if !onMonitor(a.X, a.Y) || !onMonitor(a.EndX, a.EndY) {
		return nil, fmt.Errorf("gesture endpoint outside monitors")
	}
	completed := 0
	check := func(ctx context.Context) error {
		_, _, _, epoch, e := v.Observer.FocusState(a.ID)
		if e != nil {
			return e
		}
		if epoch != focus.FocusVersion {
			return fmt.Errorf("attention changed; do not repeat action")
		}
		active, e := v.attentionWindow(ctx, w.ID)
		if e != nil {
			return e
		}
		if active.Process != w.Process || active.Bounds != w.Bounds {
			return fmt.Errorf("window geometry or process changed; do not repeat action")
		}
		return nil
	}
	interaction, status, frame, actionErr := v.Observer.ActAndWait(ctx, a.ID, observation.ActionWaitOptions{Settle: time.Duration(a.SettleMS) * time.Millisecond, Wait: time.Duration(a.WaitMS) * time.Millisecond, SuppressTimeoutImage: !a.ImageOnTimeout}, func(ctx context.Context) error {
		for _, step := range steps {
			if err := check(ctx); err != nil {
				return err
			}
			if _, err := v.action(ctx, step); err != nil {
				return err
			}
			completed++
		}
		return nil
	})
	if actionErr == nil {
		actionErr = check(ctx)
		if actionErr != nil {
			frame = nil
		}
	}
	out := v.observationResult(status, frame)
	meta := out.StructuredContent.(map[string]any)
	meta["window_id"] = w.ID
	meta["interaction"] = interaction
	meta["continuation"] = actionContinuation(interaction, frame != nil, actionErr)
	meta["native_steps_completed"] = completed
	if actionErr != nil {
		meta["error"] = actionErr.Error()
		meta["failure"] = classifyFailure(actionErr)
	}
	updated := result(meta)
	updated.Content = append(updated.Content, out.Content[1:]...)
	updated.IsError = actionErr != nil
	return updated, nil
}
