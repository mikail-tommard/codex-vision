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

type DesktopStep struct {
	TargetID   string   `json:"target_id,omitempty" jsonschema:"desktop_act only: short-lived control ID from desktop_observe; do not combine with x/y"`
	Action     string   `json:"action" jsonschema:"click, move, scroll, press, hotkey or type. Only batch predictable steps; do not guess targets after navigation"`
	X          int      `json:"x,omitempty"`
	Y          int      `json:"y,omitempty"`
	Delta      int      `json:"delta,omitempty" jsonschema:"scroll: nonzero multiple of 120, -1200..1200; negative scrolls down"`
	Horizontal bool     `json:"horizontal,omitempty"`
	Key        string   `json:"key,omitempty" jsonschema:"press: canonical ENTER, ESCAPE, PAGEDOWN etc; not Return or Esc"`
	Keys       []string `json:"keys,omitempty"`
	Text       string   `json:"text,omitempty"`
}
type DesktopBatchArgs struct {
	Expect         *ExpectedState `json:"expect,omitempty" jsonschema:"Optional exact native UI postcondition; mutually exclusive with ready_when"`
	WaitFor        string         `json:"wait_for,omitempty" jsonschema:"change (default) for navigation; response for a stable own change followed by another stable change. AX ready_when takes precedence. Visual changes do not prove an opponent response"`
	ResultMode     string         `json:"result_mode,omitempty" jsonschema:"auto (default): text on confirmed AX readiness, otherwise image. image: always inspect final visual state. Use image when the next decision needs new visual information, e.g. games"`
	ReadyWhen      *ReadyWhen     `json:"ready_when,omitempty" jsonschema:"Optional AX predicate: observe ready to busy to ready after the batch, then return one result; configure using ui_controls"`
	ID             string         `json:"observation_id"`
	WindowID       string         `json:"window_id"`
	Steps          []DesktopStep  `json:"steps" jsonschema:"1..12 predictable steps, stop on first failure; no retries, rollback or implicit submit"`
	WatchRegion    *platform.Rect `json:"watch_region,omitempty"`
	Motion         string         `json:"motion,omitempty"`
	SettleMS       int            `json:"settle_ms,omitempty" jsonschema:"100..2000ms; default 350"`
	WaitMS         int            `json:"wait_ms,omitempty" jsonschema:"100..30000ms; default up to 25000"`
	ImageOnTimeout bool           `json:"image_on_timeout,omitempty" jsonschema:"Default false: unchanged/loading timeout returns metadata only; true explicitly requests one final image"`
}

func (v *Service) registerDesktopBatch(s *mcp.Server) {
	bind(s, v, "desktop_batch", "Economical default for predictable website actions: click/move/scroll/press/hotkey/type in one confirmed window, then local AX ready_when cycle (when configured) or wait_for=response for two visual phases; one final result. result_mode=auto returns text for confirmed AX readiness, otherwise at most ONE watch-region image. Use result_mode=image when new visual information is needed. Configure attention once. Timeout returns metadata only by default; do not repeat snapshots of a stuck page. No implicit Enter, retries or rollback; inspect completed_steps and may_have_changed after failure. Separate batches whenever a later target depends on new visual information. For semantic fields prefer ui_fill_batch.", false, v.desktopBatch)
}
func (v *Service) desktopBatch(ctx context.Context, a DesktopBatchArgs) (*mcp.CallToolResult, error) {
	return v.runDesktopBatch(ctx, a, nil)
}
func (v *Service) runDesktopBatch(ctx context.Context, a DesktopBatchArgs, beforeStep func(context.Context, int) error) (*mcp.CallToolResult, error) {
	toolStarted := time.Now()
	if a.Expect != nil && a.ReadyWhen != nil {
		return nil, fmt.Errorf("choose expect or ready_when")
	}
	if a.WaitFor == "" {
		a.WaitFor = "change"
	}
	if a.ResultMode == "" {
		a.ResultMode = "auto"
	}
	if a.WaitFor != "change" && a.WaitFor != "response" {
		return nil, fmt.Errorf("wait_for must be change or response")
	}
	if a.ResultMode != "auto" && a.ResultMode != "image" {
		return nil, fmt.Errorf("result_mode must be auto or image")
	}
	if !v.Config.Images {
		return nil, fmt.Errorf("images disabled locally")
	}
	if len(a.Steps) < 1 || len(a.Steps) > 12 {
		return nil, fmt.Errorf("steps must contain 1..12 actions")
	}
	if a.SettleMS == 0 {
		a.SettleMS = 350
	}
	if a.WaitMS == 0 {
		a.WaitMS = min(25000, max(100, v.Config.TimeoutMS-2000))
	}
	if a.SettleMS < 100 || a.SettleMS > 2000 || a.WaitMS < 100 || a.WaitMS > 30000 {
		return nil, fmt.Errorf("settle_ms must be 100..2000 and wait_ms 100..30000")
	}
	if _, e := input.ApproachDuration(a.Motion, v.Config.CursorMode); e != nil {
		return nil, e
	}
	w, e := v.attentionWindow(ctx, a.WindowID)
	if e != nil {
		return nil, e
	}
	region, ready, pinned, _, e := v.Observer.FocusState(a.ID)
	if e != nil {
		return nil, e
	}
	if a.WatchRegion != nil {
		if region == nil || *region != *a.WatchRegion {
			ready = nil
		}
		region = a.WatchRegion
	} else if pinned != nil && (pinned.ID != w.ID || pinned.Process != w.Process || pinned.Bounds != w.Bounds) {
		return nil, fmt.Errorf("pinned window changed; inspect and reselect watch_region")
	}
	if region == nil {
		return nil, fmt.Errorf("select watch_region from initial image first")
	}
	monitors, e := v.Desktop.Monitors(ctx)
	if e != nil {
		return nil, e
	}
	bounds, e := platform.Bounds(monitors)
	if e != nil {
		return nil, e
	}
	onMonitor := func(x, y int) bool {
		for _, m := range monitors {
			if m.Bounds.Contains(x, y) {
				return true
			}
		}
		return false
	}
	steps := make([]platform.Action, 0, len(a.Steps))
	textBytes := 0
	for i, step := range a.Steps {
		if step.TargetID != "" {
			return nil, fmt.Errorf("target_id requires desktop_act and a fresh view_id")
		}
		act := platform.Action{WindowID: w.ID, Motion: a.Motion, X: step.X, Y: step.Y}
		switch step.Action {
		case "click":
			act.Tool = "mouse_click"
			act.Button = "left"
			act.Count = 1
		case "move":
			act.Tool = "mouse_move"
		case "scroll":
			act.Tool = "mouse_scroll"
			act.Delta = step.Delta
			act.Horizontal = step.Horizontal
			if step.Delta%120 != 0 {
				return nil, fmt.Errorf("step %d: scroll delta must be multiple of 120", i+1)
			}
		case "press":
			act.Tool = "keyboard_press"
			act.Keys = []string{step.Key}
		case "hotkey":
			act.Tool = "keyboard_hotkey"
			act.Keys = step.Keys
		case "type":
			act.Tool = "keyboard_type"
			act.Text = step.Text
			textBytes += len(step.Text)
		default:
			return nil, fmt.Errorf("step %d: unsupported action", i+1)
		}
		if e := input.Validate(act, bounds); e != nil {
			return nil, fmt.Errorf("step %d: %w", i+1, e)
		}
		if (step.Action == "click" || step.Action == "move") && (!w.Bounds.Contains(step.X, step.Y) || !onMonitor(step.X, step.Y)) {
			return nil, fmt.Errorf("step %d: point outside pinned window or monitors", i+1)
		}
		if !platform.Supports(v.Desktop, act.Tool) {
			return nil, fmt.Errorf("step %d: capability unsupported", i+1)
		}
		allowed := false
		for _, name := range v.Config.Allow {
			if name == act.Tool {
				allowed = true
			}
		}
		if !allowed {
			return nil, fmt.Errorf("step %d: capability is not locally allowlisted", i+1)
		}
		steps = append(steps, act)
	}
	if textBytes > 16384 {
		return nil, fmt.Errorf("batch text exceeds 16384 bytes")
	}
	focus, e := v.Observer.Focus(a.ID, *region, ready, &w)
	if e != nil {
		return nil, e
	}
	check := func(ctx context.Context) error {
		_, _, _, epoch, e := v.Observer.FocusState(a.ID)
		if e != nil {
			return e
		}
		if epoch != focus.FocusVersion {
			return fmt.Errorf("attention changed; stop batch without retry")
		}
		current, e := v.attentionWindow(ctx, w.ID)
		if e != nil {
			return e
		}
		if current.Process != w.Process || current.Bounds != w.Bounds {
			return fmt.Errorf("window geometry or process changed; stop batch without retry")
		}
		return nil
	}
	guard, err := v.prepareReady(ctx, w, a.ReadyWhen, time.Duration(a.SettleMS)*time.Millisecond)
	if err != nil {
		return nil, err
	}
	expected, err := v.prepareExpectation(ctx, w, a.Expect, time.Duration(a.SettleMS)*time.Millisecond)
	if err != nil {
		return nil, err
	}
	var readyCheck func(context.Context) (bool, error)
	if guard != nil && guard.report.Mode == "ax_ready" {
		readyCheck = guard.observe
	}
	if expected != nil {
		readyCheck = expected.observe
	}
	completed := 0
	preflightMS := time.Since(toolStarted).Milliseconds()
	stepMS := []int64{}
	var pointerMS int64
	pointerSteps := 0
	interaction, status, frame, err := v.Observer.ActAndWait(ctx, a.ID, observation.ActionWaitOptions{Settle: time.Duration(a.SettleMS) * time.Millisecond, Wait: time.Duration(a.WaitMS) * time.Millisecond, Ready: readyCheck, SingleChange: a.WaitFor == "change", MetadataOnReady: a.ResultMode == "auto", SuppressTimeoutImage: !a.ImageOnTimeout}, func(ctx context.Context) error {
		for index, step := range steps {
			if e := check(ctx); e != nil {
				return e
			}
			if beforeStep != nil {
				if e := beforeStep(ctx, index); e != nil {
					return e
				}
			}
			started := time.Now()
			eventResult, e := v.action(ctx, step)
			if e == nil {
				if meta, ok := eventResult.StructuredContent.(map[string]any); ok {
					if duration, ok := meta["cursor_path_ms"].(int64); ok {
						pointerMS += duration
						pointerSteps++
					}
				}
			}
			stepMS = append(stepMS, time.Since(started).Milliseconds())
			if e != nil {
				return e
			}
			completed++
		}
		// Probe immediately after the final event, before the first image sample,
		// to catch short-lived disabled/absent control states.
		if readyCheck != nil {
			if _, e := readyCheck(ctx); e != nil {
				return e
			}
		}

		return nil
	})
	if err == nil {
		err = check(ctx)
		if err != nil {
			frame = nil
		}
	}
	if err != nil && completed > 0 {
		interaction.MayHaveChanged = true
		if completed == len(steps) {
			interaction.Posted = true
			interaction.Stage = "post_input_check_failed"
		}
	}
	out := v.observationResult(status, frame)
	meta := out.StructuredContent.(map[string]any)
	meta["window_id"] = w.ID
	meta["interaction"] = interaction
	meta["continuation"] = actionContinuation(interaction, frame != nil, err)
	meta["completed_steps"] = completed
	if expected != nil {
		meta["verification"] = expected.report
	} else {
		meta["verification"] = verificationReport{Scope: "not configured; visual changes or readiness alone do not verify application outcome"}
	}
	meta["timings"] = map[string]any{"cursor_path_ms": pointerMS, "measured_pointer_steps": pointerSteps, "preflight_ms": preflightMS, "step_ms": stepMS, "tool_ms": time.Since(toolStarted).Milliseconds(), "note": "local server only; step time includes permission and window checks, not model/network time"}
	if expected != nil {
		meta["readiness"] = readinessReport{Mode: "expected_state", Matched: expected.report.Matched}
	} else if guard != nil {
		meta["readiness"] = guard.report
	} else {
		meta["readiness"] = readinessReport{Mode: "visual", Reason: "no AX predicate configured"}
	}
	if err != nil {
		meta["error"] = err.Error()
		meta["failure"] = classifyFailure(err)
	}
	updated := result(meta)
	updated.Content = append(updated.Content, out.Content[1:]...)
	updated.IsError = err != nil
	return updated, nil
}
