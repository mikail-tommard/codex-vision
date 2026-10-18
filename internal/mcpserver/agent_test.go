package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"example.com/codex-vision/internal/observation"
	"example.com/codex-vision/internal/platform"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// An explicit synthetic app state machine, not a browser, shell or CAD model.
type agentDesktop struct {
	*gestureDesktop
	goal                 string
	offset               int
	moveAfterFirst       bool
	loseFocusAfterAction bool
	stages               int
}

func agentFixture(goal string) *agentDesktop {
	return &agentDesktop{gestureDesktop: &gestureDesktop{workflowDesktop: workflow()}, goal: goal}
}
func (d *agentDesktop) ReadControls(ctx context.Context, w platform.Window, n int) (platform.ControlSnapshot, error) {
	d.stateMu.Lock()
	defer d.stateMu.Unlock()
	x := 40 + d.offset
	if d.moveAfterFirst && len(d.recorded) > 0 {
		x += 20
	}
	if d.loseFocusAfterAction && len(d.recorded) > 0 {
		d.window.ID = "other-window"
	}
	enabled := true
	return platform.ControlSnapshot{Controls: []platform.UIControl{{Label: "Execute", Role: "AXButton", Enabled: &enabled, Bounds: platform.Rect{X: x, Y: 40, Width: 100, Height: 30}}}, Visited: 1}, ctx.Err()
}
func (d *agentDesktop) Execute(ctx context.Context, a platform.Action) error {
	if err := d.gestureDesktop.Execute(ctx, a); err != nil {
		return err
	}
	d.stateMu.Lock()
	defer d.stateMu.Unlock()
	if a.Tool == "window_focus" {
		d.window.ID = a.WindowID
		return nil
	}
	button := platform.Rect{X: 40 + d.offset, Y: 40, Width: 100, Height: 30}
	if d.moveAfterFirst && len(d.recorded) > 1 {
		button.X += 20
	}
	clicked := a.Tool == "mouse_click" && button.Contains(a.X, a.Y)
	pressed := a.Tool == "keyboard_press" && len(a.Keys) == 1 && a.Keys[0] == "SPACE"
	if clicked || pressed {
		d.stages++
		d.window.Title = fmt.Sprintf("%s %d", d.goal, d.stages)
	}
	return nil
}

func agentClient(t *testing.T, d *agentDesktop) (*mcp.ClientSession, *Service) {
	t.Helper()
	cfg := uiConfig()
	cfg.Images = true
	cfg.TimeoutMS = 6000
	cfg.Allow = []string{"mouse_click", "mouse_move", "keyboard_press", "ui_fill", "window_focus"}
	return connectDesktop(t, cfg, d)
}

type agentData struct {
	Frame *observation.Frame `json:"frame"`
	View  struct {
		ID            string `json:"view_id"`
		ObservationID string `json:"observation_id"`
	} `json:"view"`
	Controls []struct {
		ID     string        `json:"target_id"`
		Bounds platform.Rect `json:"bounds"`
	} `json:"controls"`
	Verification verificationReport           `json:"verification"`
	Interaction  observation.ActionWaitResult `json:"interaction"`
	Completed    int                          `json:"completed_steps"`
	Fields       map[string]any               `json:"fields"`
	Failure      failureInfo                  `json:"failure"`
	Window       platform.Window              `json:"window"`
}

func agentResult(t *testing.T, r *mcp.CallToolResult) agentData {
	t.Helper()
	var d agentData
	if err := json.Unmarshal([]byte(r.Content[0].(*mcp.TextContent).Text), &d); err != nil {
		t.Fatal(err)
	}
	return d
}
func agentClick(view, id, goal string) map[string]any {
	return map[string]any{"view_id": view, "steps": []map[string]any{{"action": "click", "target_id": id}}, "expect": map[string]any{"title_contains": goal}, "settle_ms": 100, "wait_ms": 3000}
}

func TestUnifiedObserveAndActReturnsNextViewWithoutExtraImage(t *testing.T) {
	d := agentFixture("Saved")
	c, svc := agentClient(t, d)
	initial := call(t, c, "desktop_observe", map[string]any{"include_fields": true, "labels": []string{"Name"}}, true)
	v := agentResult(t, initial)
	if len(initial.Content) != 2 || v.View.ID == "" || len(v.Controls) != 1 || v.Controls[0].ID == "" || v.Fields["snapshot_id"] == nil {
		t.Fatal(v)
	}
	r := call(t, c, "desktop_act", agentClick(v.View.ID, v.Controls[0].ID, "Saved 1"), true)
	first := agentResult(t, r)
	if !first.Verification.Matched || !first.Interaction.ReadyConfirmed || first.Interaction.ResponseVerified || len(r.Content) != 1 || first.View.ID == "" || first.View.ID == v.View.ID {
		t.Fatal(first, len(r.Content))
	}
	call(t, c, "desktop_act", agentClick(v.View.ID, v.Controls[0].ID, "Saved 2"), false)
	if len(d.actions()) != 1 {
		t.Fatal("consumed view replayed")
	}
	second := agentResult(t, call(t, c, "desktop_act", agentClick(first.View.ID, first.Controls[0].ID, "Saved 2"), true))
	if !second.Verification.Matched || len(d.actions()) != 2 {
		t.Fatal(second)
	}
	metrics := svc.metrics.snapshot()
	if metrics["images_returned"] != int64(1) {
		t.Fatal("text readiness encoded extra images", metrics)
	}
}

func TestUnifiedActRefusesMovedTargetsAndPartialReplay(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(fmt.Sprint(partial), func(t *testing.T) {
			d := agentFixture("Saved")
			c, _ := agentClient(t, d)
			v := agentResult(t, call(t, c, "desktop_observe", map[string]any{}, true))
			d.stateMu.Lock()
			if partial {
				d.moveAfterFirst = true
			} else {
				d.offset = 20
			}
			d.stateMu.Unlock()
			args := agentClick(v.View.ID, v.Controls[0].ID, "Saved")
			if partial {
				args["steps"] = []map[string]any{{"action": "click", "target_id": v.Controls[0].ID}, {"action": "click", "target_id": v.Controls[0].ID}}
			}
			r := call(t, c, "desktop_act", args, false)
			out := agentResult(t, r)
			if partial {
				if out.Completed != 1 || !out.Interaction.MayHaveChanged || len(r.Content) != 1 || len(d.actions()) != 1 {
					t.Fatal(out)
				}
				call(t, c, "desktop_act", args, false)
				if len(d.actions()) != 1 {
					t.Fatal("partial action replayed")
				}
			} else if len(d.actions()) != 0 || out.Failure.Code != "stale_reference" {
				t.Fatal(out)
			}
		})
	}
}

func TestUnifiedActImageCoordinatesAndPreflightFocus(t *testing.T) {
	d := agentFixture("Saved")
	c, _ := agentClient(t, d)
	v := agentResult(t, call(t, c, "desktop_observe", map[string]any{"watch_region": platform.Rect{X: 20, Y: 30, Width: 200, Height: 120}}, true))
	d.stateMu.Lock()
	d.window.ID = "other-window"
	d.stateMu.Unlock()
	args := map[string]any{"view_id": v.View.ID, "coordinates": "image", "steps": []map[string]any{{"action": "click", "x": 50, "y": 35}}, "expect": map[string]any{"title_contains": "Saved"}, "settle_ms": 100, "wait_ms": 3000}
	r := agentResult(t, call(t, c, "desktop_act", args, true))
	actions := d.actions()
	if !r.Verification.Matched || len(actions) != 2 || actions[0].Tool != "window_focus" || actions[1].X != 70 || actions[1].Y != 65 {
		t.Fatal(r, actions)
	}
}

func TestUnifiedPostInputFocusFailureKeepsActionReport(t *testing.T) {
	d := agentFixture("Saved")
	c, _ := agentClient(t, d)
	v := agentResult(t, call(t, c, "desktop_observe", map[string]any{}, true))
	d.stateMu.Lock()
	d.loseFocusAfterAction = true
	d.stateMu.Unlock()
	r := call(t, c, "desktop_act", agentClick(v.View.ID, v.Controls[0].ID, "Saved"), false)
	out := agentResult(t, r)
	if !out.Interaction.Posted || out.Completed != 1 || len(r.Content) != 1 || len(d.actions()) != 1 || out.View.ID != "" {
		t.Fatal(out, len(r.Content))
	}
}

func TestExpectationAlreadySatisfiedPreventsInput(t *testing.T) {
	d := agentFixture("Saved")
	c, _ := agentClient(t, d)
	v := agentResult(t, call(t, c, "desktop_observe", map[string]any{}, true))
	r := agentResult(t, call(t, c, "desktop_act", agentClick(v.View.ID, v.Controls[0].ID, "Loading"), false))
	if r.Failure.Code != "expectation_already_satisfied" || len(d.actions()) != 0 {
		t.Fatal(r)
	}
}

func TestFailureClassificationAndNoReplay(t *testing.T) {
	for message, code := range map[string]string{"stale view": "stale_reference", "cursor target outside allowed foreground window": "coordinates_outside_target", "Windows foreground not confirmed": "focus_unconfirmed", "target process is not locally allowlisted": "access_denied", "ambiguous control": "ambiguous_target", "semantic UI unsupported": "unsupported_capability"} {
		r := classifyFailure(fmt.Errorf("%s", message))
		if r.Code != code || r.RetryAction {
			t.Fatal(message, r)
		}
	}
}

func TestUnifiedObserveUnsupportedUIStillHasVisualContext(t *testing.T) {
	d := &gestureDesktop{workflowDesktop: workflow()}
	cfg := uiConfig()
	cfg.Images = true
	c, _ := connectDesktop(t, cfg, d)
	r := call(t, c, "desktop_observe", map[string]any{}, true)
	if len(r.Content) != 2 || !strings.Contains(r.Content[0].(*mcp.TextContent).Text, "unsupported backend") {
		t.Fatal(r)
	}
}

type expectedControlsDesktop struct {
	*agentDesktop
	mode string
}

func (d *expectedControlsDesktop) ReadControls(ctx context.Context, w platform.Window, n int) (platform.ControlSnapshot, error) {
	raw, err := d.agentDesktop.ReadControls(ctx, w, n)
	if err != nil {
		return raw, err
	}
	d.stateMu.Lock()
	stage := d.stages
	d.stateMu.Unlock()
	if stage == 0 || d.mode == "timeout" {
		return raw, nil
	}
	raw.Controls[0].Label = "Saved"
	switch d.mode {
	case "truncated":
		raw.Truncated = true
	case "unknown":
		raw.Controls[0].Enabled = nil
	case "duplicate":
		raw.Controls = append(raw.Controls, raw.Controls[0])
	}
	return raw, nil
}

func TestExpectedControlRequiresCompleteKnownEvidence(t *testing.T) {
	for _, mode := range []string{"present", "absent", "truncated", "unknown", "duplicate", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			d := &expectedControlsDesktop{agentDesktop: agentFixture("Saved"), mode: mode}
			cfg := uiConfig()
			cfg.Images = true
			cfg.TimeoutMS = 6000
			cfg.Allow = []string{"mouse_click"}
			c, _ := connectDesktop(t, cfg, d)
			view := agentResult(t, call(t, c, "desktop_observe", map[string]any{"interval_ms": 250}, true))
			expect := map[string]any{"control_label": "Saved", "enabled": true}
			if mode == "absent" {
				expect = map[string]any{"control_label": "Execute", "present": false}
			}
			args := agentClick(view.View.ID, view.Controls[0].ID, "")
			args["expect"] = expect
			args["wait_ms"] = 850
			valid := mode == "present" || mode == "absent" || mode == "timeout"
			r := call(t, c, "desktop_act", args, valid)
			out := agentResult(t, r)
			if len(d.actions()) != 1 || out.Completed != 1 || len(r.Content) != 1 {
				t.Fatal(out, len(r.Content))
			}
			if mode == "present" || mode == "absent" {
				if !out.Verification.Matched || out.View.ID == "" {
					t.Fatal(out)
				}
			} else if out.Verification.Matched {
				t.Fatal("unproven state reported as verified", out)
			}
			if mode == "timeout" && (!out.Interaction.TimedOut || out.View.ID != "") {
				t.Fatal(out)
			}
		})
	}
}

func TestImageCoordinateConversionHandlesScaleAndNegativeOrigin(t *testing.T) {
	d := agentFixture("Saved")
	d.window.Bounds = platform.Rect{X: -300, Y: 0, Width: 900, Height: 240}
	c, _ := agentClient(t, d)
	v := agentResult(t, call(t, c, "desktop_observe", map[string]any{"interval_ms": 250}, true))
	if v.Frame == nil || v.Frame.Width >= 900 {
		t.Fatal("fixture did not exercise scaling", v.Frame)
	}
	u := ((90-v.Frame.Bounds.X)*v.Frame.Width + v.Frame.Bounds.Width - 1) / v.Frame.Bounds.Width
	z := ((55-v.Frame.Bounds.Y)*v.Frame.Height + v.Frame.Bounds.Height - 1) / v.Frame.Bounds.Height
	x := v.Frame.Bounds.X + u*v.Frame.Bounds.Width/v.Frame.Width
	y := v.Frame.Bounds.Y + z*v.Frame.Bounds.Height/v.Frame.Height
	args := map[string]any{"view_id": v.View.ID, "coordinates": "image", "steps": []map[string]any{{"action": "click", "x": u, "y": z}}, "expect": map[string]any{"title_contains": "Saved"}, "settle_ms": 100, "wait_ms": 2000}
	call(t, c, "desktop_act", args, true)
	a := d.actions()
	if len(a) != 1 || a[0].X != x || a[0].Y != y {
		t.Fatal(a, x, y)
	}
}

func TestUnifiedToolsRespectRecoveryAndEmergencyStop(t *testing.T) {
	d := agentFixture("Saved")
	c, svc := agentClient(t, d)
	v := agentResult(t, call(t, c, "desktop_observe", map[string]any{}, true))
	svc.recovery.failure("mock-window", "foreground not confirmed")
	svc.recovery.failure("mock-window", "foreground not confirmed")
	call(t, c, "desktop_act", agentClick(v.View.ID, v.Controls[0].ID, "Saved"), false)
	call(t, c, "desktop_observe", map[string]any{}, false)
	if len(d.actions()) != 0 {
		t.Fatal("recovery guard bypassed")
	}
	svc.recovery.confirm("mock-window")
	call(t, c, "session_stop", map[string]any{}, true)
	call(t, c, "desktop_act", agentClick(v.View.ID, v.Controls[0].ID, "Saved"), false)
	if len(d.actions()) != 0 {
		t.Fatal("emergency stop bypassed")
	}
}

func TestViewCannotOverrideChangedAttention(t *testing.T) {
	d := agentFixture("Saved")
	c, _ := agentClient(t, d)
	v := agentResult(t, call(t, c, "desktop_observe", map[string]any{}, true))
	call(t, c, "observation_control", map[string]any{"observation_id": v.View.ObservationID, "action": "focus", "window_id": "mock-window", "watch_region": platform.Rect{X: 10, Y: 10, Width: 200, Height: 100}}, true)
	out := agentResult(t, call(t, c, "desktop_act", agentClick(v.View.ID, v.Controls[0].ID, "Saved"), false))
	if len(d.actions()) != 0 || out.Failure.Code != "stale_reference" {
		t.Fatal(out)
	}
}

func TestActCanSelectTaskRegionWithoutExtraObservation(t *testing.T) {
	d := agentFixture("Saved")
	c, svc := agentClient(t, d)
	v := agentResult(t, call(t, c, "desktop_observe", map[string]any{"interval_ms": 250}, true))
	region := platform.Rect{X: 20, Y: 30, Width: 200, Height: 100}
	args := agentClick(v.View.ID, v.Controls[0].ID, "Saved")
	args["watch_region"] = region
	out := agentResult(t, call(t, c, "desktop_act", args, true))
	status := svc.Observer.Status()
	if !out.Verification.Matched || status.WatchRegion == nil || *status.WatchRegion != region || svc.metrics.snapshot()["tool_calls"] != int64(2) {
		t.Fatal(out, status)
	}
}

func TestUnifiedObserveReusesNarrowContinuousCapture(t *testing.T) {
	d := agentFixture("Saved")
	c, svc := agentClient(t, d)
	region := platform.Rect{X: 20, Y: 30, Width: 200, Height: 100}
	before := observeStatus(t, call(t, c, "observation_start", map[string]any{"region": region, "mode": "continuous", "preview": false, "interval_ms": 250, "min_delivery_ms": 250}, true))
	out := agentResult(t, call(t, c, "desktop_observe", map[string]any{}, true))
	after := svc.Observer.Status()
	if out.View.ObservationID != before.ID || after.Mode != "continuous" || out.Frame == nil || out.Frame.Bounds != region {
		t.Fatal(out, after)
	}
}

func TestWrongVisualClickDoesNotSatisfyUIExpectation(t *testing.T) {
	d := agentFixture("Saved")
	c, _ := agentClient(t, d)
	v := agentResult(t, call(t, c, "desktop_observe", map[string]any{"interval_ms": 250}, true))
	args := agentClick(v.View.ID, "", "Saved")
	args["steps"] = []map[string]any{{"action": "click", "x": 10, "y": 10}}
	args["wait_ms"] = 650
	out := agentResult(t, call(t, c, "desktop_act", args, true))
	if out.Verification.Matched || !out.Interaction.TimedOut || out.View.ID != "" || len(d.actions()) != 1 {
		t.Fatal(out)
	}
}
