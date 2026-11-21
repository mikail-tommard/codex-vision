package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"example.com/codex-vision/internal/platform"
)

func waitWorkflow(t *testing.T, svc *Service) WorkflowReport {
	t.Helper()
	svc.workflows.mu.Lock()
	done := svc.workflows.done
	svc.workflows.mu.Unlock()
	select {
	case <-done:
	case <-time.After(4 * time.Second):
		t.Fatal("workflow did not terminate")
	}
	return svc.workflows.snapshot()
}

func workflowPlan() []WorkflowStep {
	return []WorkflowStep{
		{Step: DesktopStep{Action: "click", Target: &ControlSelector{Label: "Execute", Role: "AXButton"}}, Expect: ExpectedState{TitleContains: "Saved 1"}},
		{Step: DesktopStep{Action: "press", Key: "SPACE"}, Expect: ExpectedState{TitleContains: "Saved 2"}},
	}
}

func TestWorkflowNativeCheckpointsWithoutScreenshots(t *testing.T) {
	d := agentFixture("Saved")
	cfg := uiConfig()
	cfg.Workflows = true
	cfg.Allow = []string{"mouse_click", "keyboard_press"}
	c, svc := connectDesktop(t, cfg, d)
	plan := workflowPlan()
	plan[0].Step.Button = "right"
	call(t, c, "workflow_control", map[string]any{"action": "start", "window_id": "mock-window", "steps": plan, "max_actions": 2, "timeout_ms": 5000}, true)
	r := waitWorkflow(t, svc)
	actions := d.actions()
	if r.State != "success" || r.Checkpoint != 2 || len(r.Journal) != 2 || len(actions) != 2 || actions[0].Button != "right" || svc.Observer.Status().LocalFrames != 0 {
		t.Fatal(r, actions)
	}
	encoded, _ := json.Marshal(r)
	if strings.Contains(string(encoded), "Execute") || strings.Contains(string(encoded), "Saved") {
		t.Fatal("workflow report retained UI text", string(encoded))
	}
	call(t, c, "workflow_control", map[string]any{"action": "resume", "workflow_id": r.ID}, false)
}

func TestWorkflowResumeOnlyBeforeInput(t *testing.T) {
	d := &selectorDesktop{agentDesktop: agentFixture("Saved"), mode: "duplicate"}
	cfg := uiConfig()
	cfg.Workflows, cfg.TimeoutMS = true, 1000
	cfg.Allow = []string{"mouse_click"}
	c, svc := connectDesktop(t, cfg, d)
	plan := workflowPlan()[:1]
	call(t, c, "workflow_control", map[string]any{"action": "start", "window_id": "mock-window", "steps": plan, "max_actions": 1, "timeout_ms": 5000}, true)
	r := waitWorkflow(t, svc)
	if !r.Resumable || r.InputMayHavePosted || len(d.actions()) != 0 {
		t.Fatal(r)
	}
	d.mode = "unique" // Worker has stopped; no concurrent reads remain.
	call(t, c, "workflow_control", map[string]any{"action": "resume", "workflow_id": r.ID}, true)
	r = waitWorkflow(t, svc)
	if r.State != "success" || r.Attempts != 2 || len(d.actions()) != 1 {
		t.Fatal(r)
	}
	// A posted action with an unobserved result must never be replayed.
	plan[0].Expect.TitleContains = "Never appears"
	call(t, c, "workflow_control", map[string]any{"action": "start", "window_id": "mock-window", "steps": plan, "max_actions": 1, "timeout_ms": 5000}, true)
	r = waitWorkflow(t, svc)
	if r.State != "timeout" || r.Resumable || !r.InputMayHavePosted || len(d.actions()) != 2 {
		t.Fatal(r)
	}
	call(t, c, "workflow_control", map[string]any{"action": "resume", "workflow_id": r.ID}, false)
	if len(d.actions()) != 2 {
		t.Fatal("uncertain action replayed")
	}
}

func TestWorkflowCancellationBypassesBusyInputLane(t *testing.T) {
	d := agentFixture("Saved")
	cfg := uiConfig()
	cfg.Workflows = true
	cfg.Allow = []string{"mouse_click"}
	c, svc := connectDesktop(t, cfg, d)
	unlock, err := svc.lockTool(context.Background(), "keyboard_press")
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	call(t, c, "workflow_control", map[string]any{"action": "start", "window_id": "mock-window", "steps": workflowPlan()[:1], "max_actions": 1, "timeout_ms": 5000}, true)
	id := svc.workflows.snapshot().ID
	call(t, c, "workflow_control", map[string]any{"action": "cancel", "workflow_id": id}, true)
	r := waitWorkflow(t, svc)
	if r.State != "cancelled" || r.Resumable || len(d.actions()) != 0 {
		t.Fatal(r)
	}
}

func TestWorkflowRejectsUnsafePlanAndUsesPolicy(t *testing.T) {
	d := agentFixture("Saved")
	cfg := uiConfig()
	cfg.Workflows = true
	cfg.Allow = nil
	c, svc := connectDesktop(t, cfg, d)
	plan := workflowPlan()
	plan[1].Step = DesktopStep{Action: "click", X: 50, Y: 50}
	call(t, c, "workflow_control", map[string]any{"action": "start", "window_id": "mock-window", "steps": plan, "max_actions": 2, "timeout_ms": 5000}, false)
	if len(d.actions()) != 0 {
		t.Fatal("invalid later step allowed earlier action")
	}
	call(t, c, "workflow_control", map[string]any{"action": "start", "window_id": "mock-window", "steps": workflowPlan()[:1], "max_actions": 1, "timeout_ms": 5000}, true)
	r := waitWorkflow(t, svc)
	if r.State == "success" || len(d.actions()) != 0 {
		t.Fatal("workflow bypassed allowlist", r)
	}
}

func TestWorkflowPreflightRecoveryHasFiniteAttemptsAndOriginalDeadline(t *testing.T) {
	d := &selectorDesktop{agentDesktop: agentFixture("Saved"), mode: "duplicate"}
	cfg := uiConfig()
	cfg.Workflows = true
	cfg.Allow = []string{"mouse_click"}
	c, svc := connectDesktop(t, cfg, d)
	start := map[string]any{"action": "start", "window_id": "mock-window", "steps": workflowPlan()[:1], "max_actions": 1, "timeout_ms": 5000}
	call(t, c, "workflow_control", start, true)
	r := waitWorkflow(t, svc)
	deadline := r.Deadline
	for attempt := 0; attempt < 3; attempt++ {
		call(t, c, "workflow_control", map[string]any{"action": "resume", "workflow_id": r.ID}, true)
		r = waitWorkflow(t, svc)
		if !r.Deadline.Equal(deadline) {
			t.Fatal("resume extended execution deadline")
		}
	}
	if r.Resumable || r.Attempts != 4 || len(d.actions()) != 0 {
		t.Fatal("unbounded recovery", r)
	}
	call(t, c, "workflow_control", map[string]any{"action": "resume", "workflow_id": r.ID}, false)
}

func TestWorkflowSessionStopAndReadOnlyAreEnforced(t *testing.T) {
	for _, readonly := range []bool{false, true} {
		t.Run(map[bool]string{false: "stop", true: "observe"}[readonly], func(t *testing.T) {
			d := agentFixture("Saved")
			cfg := uiConfig()
			cfg.Workflows = true
			cfg.Allow = []string{"mouse_click"}
			if readonly {
				cfg.Mode = "observe"
			}
			c, svc := connectDesktop(t, cfg, d)
			unlock, err := svc.lockTool(context.Background(), "keyboard_press")
			if err != nil {
				t.Fatal(err)
			}
			defer unlock()
			call(t, c, "workflow_control", map[string]any{"action": "start", "window_id": "mock-window", "steps": workflowPlan()[:1], "max_actions": 1, "timeout_ms": 5000}, !readonly)
			if !readonly {
				call(t, c, "session_stop", map[string]any{}, true)
				r := waitWorkflow(t, svc)
				if r.State != "cancelled" || r.Resumable {
					t.Fatal(r)
				}
			}
			if len(d.actions()) != 0 {
				t.Fatal("stop or read-only bypassed")
			}
		})
	}
}

func TestSessionStopRevokesIdleWorkflowResume(t *testing.T) {
	d := &selectorDesktop{agentDesktop: agentFixture("Saved"), mode: "duplicate"}
	cfg := uiConfig()
	cfg.Workflows = true
	cfg.Allow = []string{"mouse_click"}
	c, svc := connectDesktop(t, cfg, d)
	call(t, c, "workflow_control", map[string]any{"action": "start", "window_id": "mock-window", "steps": workflowPlan()[:1], "max_actions": 1, "timeout_ms": 5000}, true)
	r := waitWorkflow(t, svc)
	if !r.Resumable {
		t.Fatal("fixture did not reach pre-input failure", r)
	}
	call(t, c, "session_stop", map[string]any{}, true)
	if after := svc.workflows.snapshot(); after.Resumable || after.State != "cancelled" {
		t.Fatal("stop left idle checkpoint resumable", after)
	}
	call(t, c, "workflow_control", map[string]any{"action": "resume", "workflow_id": r.ID}, false)
}

var _ platform.ControlReader = (*selectorDesktop)(nil)
