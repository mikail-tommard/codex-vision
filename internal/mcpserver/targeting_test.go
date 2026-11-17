package mcpserver

import (
	"context"
	"strings"
	"testing"

	"example.com/codex-vision/internal/platform"
)

type selectorDesktop struct {
	*agentDesktop
	mode string
}

func (d *selectorDesktop) ReadControls(ctx context.Context, w platform.Window, n int) (platform.ControlSnapshot, error) {
	raw, err := d.agentDesktop.ReadControls(ctx, w, n)
	switch d.mode {
	case "duplicate", "role":
		other := raw.Controls[0]
		other.Bounds.X += 150
		if d.mode == "role" {
			other.Role = "AXMenuItem"
		}
		raw.Controls = append(raw.Controls, other)
	case "disabled":
		value := false
		raw.Controls[0].Enabled = &value
	case "unknown":
		raw.Controls[0].Enabled = nil
	case "truncated":
		raw.Truncated = true
	case "late_duplicate":
		if len(d.actions()) > 0 {
			raw.Controls = append(raw.Controls, raw.Controls[0])
		}
	}
	return raw, err
}

func TestSemanticTargetUsesObservedUniqueFreshControl(t *testing.T) {
	for _, mode := range []string{"unique", "role", "duplicate", "disabled", "unknown", "truncated", "late_duplicate"} {
		t.Run(mode, func(t *testing.T) {
			d := &selectorDesktop{agentDesktop: agentFixture("Saved"), mode: mode}
			cfg := uiConfig()
			cfg.Allow = []string{"mouse_click"}
			c, _ := connectDesktop(t, cfg, d)
			view := agentResult(t, call(t, c, "desktop_observe", map[string]any{"include_controls": true, "interval_ms": 250}, true))
			steps := []map[string]any{{"action": "click", "target": map[string]any{"label": "Execute", "role": "AXButton"}}}
			if mode == "late_duplicate" {
				steps = append(steps, steps[0])
			}
			valid := mode == "unique" || mode == "role"
			r := call(t, c, "desktop_act", map[string]any{"view_id": view.View.ID, "steps": steps, "expect": map[string]any{"title_contains": "Saved"}, "settle_ms": 100, "wait_ms": 1500}, valid)
			out := agentResult(t, r)
			want := 0
			if valid || mode == "late_duplicate" {
				want = 1
			}
			if len(d.actions()) != want || (valid && !out.Verification.Matched) {
				t.Fatal(out, d.actions())
			}
			if mode == "late_duplicate" && (out.Completed != 1 || !out.Interaction.MayHaveChanged) {
				t.Fatal("partial batch report lost", out)
			}
		})
	}
}

func TestSemanticTargetValidationBeforeRefocus(t *testing.T) {
	d := agentFixture("Saved")
	c, svc := agentClient(t, d)
	view := agentResult(t, call(t, c, "desktop_observe", map[string]any{}, true))
	d.stateMu.Lock()
	d.window.ID = "other"
	d.stateMu.Unlock()
	for _, step := range []DesktopStep{
		{Action: "click", Target: &ControlSelector{Label: "Execute"}, TargetID: "both"},
		{Action: "click", X: 1, Target: &ControlSelector{Label: "Execute"}},
		{Action: "press", Target: &ControlSelector{Label: "Execute"}},
		{Action: "click", Target: &ControlSelector{}},
		{Action: "click", Target: &ControlSelector{Label: strings.Repeat("a", 513)}},
	} {
		if _, err := svc.desktopAct(context.Background(), DesktopActArgs{ViewID: view.View.ID, Steps: []DesktopStep{step}}); err == nil {
			t.Fatal("invalid target accepted", step)
		}
	}
	if len(d.actions()) != 0 {
		t.Fatal("invalid target caused refocus")
	}
}

func TestTitleChangeInvalidatesSameWindowView(t *testing.T) {
	d := agentFixture("Saved")
	c, _ := agentClient(t, d)
	view := agentResult(t, call(t, c, "desktop_observe", map[string]any{}, true))
	d.stateMu.Lock()
	d.window.Title = "Another document"
	d.stateMu.Unlock()
	out := agentResult(t, call(t, c, "desktop_act", agentClick(view.View.ID, view.Controls[0].ID, "Saved"), false))
	if out.Failure.Code != "stale_reference" || len(d.actions()) != 0 {
		t.Fatal("changed document received input", out)
	}
}

func TestSignificantVisualChangeInvalidatesCoordinateView(t *testing.T) {
	d := agentFixture("Saved")
	c, svc := agentClient(t, d)
	view := agentResult(t, call(t, c, "desktop_observe", map[string]any{"interval_ms": 250, "min_delivery_ms": 250}, true))
	d.phase.Store(80) // External visual change, with unchanged title/process/bounds.
	call(t, c, "observation_next", map[string]any{"observation_id": view.View.ObservationID, "after_sequence": view.Frame.Sequence, "wait_ms": 2500}, true)
	if svc.Observer.Status().Sequence <= view.Frame.Sequence {
		t.Fatal("fixture did not publish external change")
	}
	out := agentResult(t, call(t, c, "desktop_act", map[string]any{"view_id": view.View.ID, "steps": []map[string]any{{"action": "click", "x": 50, "y": 50}}}, false))
	if out.Failure.Code != "stale_reference" || len(d.actions()) != 0 {
		t.Fatal("old coordinates accepted", out)
	}
	// An independently refreshed unique structural target can survive animation.
	verified := agentResult(t, call(t, c, "desktop_act", agentClick(view.View.ID, view.Controls[0].ID, "Saved"), true))
	if !verified.Verification.Matched {
		t.Fatal(verified)
	}
}
