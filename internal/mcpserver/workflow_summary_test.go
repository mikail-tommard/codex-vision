package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"example.com/codex-vision/internal/platform"
)

func TestWorkflowSummaryReducesBytesWithoutLosingCheckpoints(t *testing.T) {
	for _, profile := range []string{"compact", "full"} {
		t.Run(profile, func(t *testing.T) {
			results := make(map[string]int64)
			for _, detail := range []string{"full", "summary"} {
				d := agentFixture("Saved")
				cfg := uiConfig()
				cfg.Workflows, cfg.MeasureWireBytes = true, true
				cfg.Allow = []string{"keyboard_press"}
				c, svc := connectProfileDesktop(t, cfg, d, profile)
				plan := make([]WorkflowStep, 8)
				for i := range plan {
					plan[i] = WorkflowStep{Step: DesktopStep{Action: "press", Key: "SPACE"}, Expect: ExpectedState{TitleContains: fmt.Sprintf("Saved %d", i+1)}}
				}
				out := call(t, c, "workflow_control", map[string]any{"action": "start", "window_id": "mock-window", "steps": plan, "max_actions": 8, "timeout_ms": 10000, "wait_ms": 3000, "detail": detail}, true)
				r := decodeWorkflow(t, fieldsData(t, out))
				if r.State != "success" || r.Checkpoint != 8 || r.EventCursor != 8 || r.InputMayHavePosted || len(d.actions()) != 8 {
					t.Fatal("delivery changed workflow result", r, d.actions())
				}
				if detail == "summary" && (r.JournalMode != "summary" || len(r.Journal) != 0) {
					t.Fatal("summary repeated successful history", r)
				}
				if detail == "full" && len(r.Journal) != 8 {
					t.Fatal("legacy full journal lost events", r)
				}
				metrics := svc.metrics.snapshot()
				if metrics["tool_calls"] != int64(1) || metrics["images_returned"] != int64(0) {
					t.Fatal(metrics)
				}
				results[detail] = metrics["serialized_result_bytes"].(int64)
			}
			if results["summary"]*2 >= results["full"] {
				t.Fatal("summary did not remove enough repeated data", results)
			}
			encoded, err := json.Marshal(map[string]any{"profile": profile, "environment": "paired mock workflow; same eight verified keyboard actions", "strategy_before": "wait_ms plus full journal", "strategy_after": "wait_ms plus summary", "serialized_bytes_before": results["full"], "serialized_bytes_after": results["summary"], "tool_calls_each": 1, "images_each": 0, "scope": "CallToolResult JSON only; excludes requests/catalog/transport and Codex tokens or quota"})
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("SUMMARY_RESULT %s", encoded)
		})
	}
}

type pendingActionDesktop struct {
	*agentDesktop
	posted chan struct{}
}

func (d *pendingActionDesktop) Execute(ctx context.Context, a platform.Action) error {
	if err := d.agentDesktop.Execute(ctx, a); err != nil {
		return err
	}
	close(d.posted)
	return nil
}

func TestWorkflowSummaryPreservesPendingInputAndFailure(t *testing.T) {
	d := &pendingActionDesktop{agentDesktop: agentFixture("Saved"), posted: make(chan struct{})}
	cfg := uiConfig()
	cfg.Workflows = true
	cfg.Allow = []string{"keyboard_press"}
	c, svc := connectDesktop(t, cfg, d)
	plan := []WorkflowStep{{Step: DesktopStep{Action: "press", Key: "SPACE"}, Expect: ExpectedState{TitleContains: "Never appears"}}}
	call(t, c, "workflow_control", map[string]any{"action": "start", "window_id": "mock-window", "steps": plan, "max_actions": 1, "timeout_ms": 5000}, true)
	select {
	case <-d.posted:
	case <-time.After(2 * time.Second):
		t.Fatal("native fixture action not posted")
	}
	out := call(t, c, "workflow_control", map[string]any{"action": "status", "detail": "summary"}, true)
	r := decodeWorkflow(t, fieldsData(t, out))
	if r.State != "running" || r.Checkpoint != 0 || !r.InputMayHavePosted || len(r.Journal) != 0 {
		t.Fatal("running report hid pending input", r)
	}
	call(t, c, "workflow_control", map[string]any{"action": "cancel", "workflow_id": r.ID}, true)
	finished := waitWorkflow(t, svc)
	out = call(t, c, "workflow_control", map[string]any{"action": "status", "workflow_id": r.ID, "detail": "summary"}, true)
	r = decodeWorkflow(t, fieldsData(t, out))
	if r.State != "cancelled" || !r.InputMayHavePosted || len(r.Journal) != 1 || r.Journal[0].Code != "cancelled" || finished.Checkpoint != 0 || len(d.actions()) != 1 {
		t.Fatal("summary hid uncertainty or failure code", r)
	}
	call(t, c, "workflow_control", map[string]any{"action": "resume", "workflow_id": r.ID}, false)
	if len(d.actions()) != 1 {
		t.Fatal("uncertain action replayed")
	}
}

func TestWorkflowSummaryValidationDoesNotStartWorker(t *testing.T) {
	d := agentFixture("Saved")
	cfg := uiConfig()
	cfg.Workflows = true
	c, svc := connectDesktop(t, cfg, d)
	for _, options := range []map[string]any{{"detail": "unknown"}, {"detail": "summary", "after_event": 0}} {
		args := map[string]any{"action": "start", "window_id": "mock-window", "steps": workflowPlan(), "max_actions": 2, "timeout_ms": 5000}
		for k, v := range options {
			args[k] = v
		}
		call(t, c, "workflow_control", args, false)
		if svc.workflows.snapshot().ID != "" || len(d.actions()) != 0 {
			t.Fatal("invalid delivery started native workflow")
		}
	}
}
