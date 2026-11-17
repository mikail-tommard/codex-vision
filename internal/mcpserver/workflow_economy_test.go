package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
)

func decodeWorkflow(t *testing.T, value any) WorkflowReport {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var report WorkflowReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	return report
}

func TestWorkflowWaitReducesCallsAndSerializedBytes(t *testing.T) {
	for _, profile := range []string{"compact", "full"} {
		t.Run(profile, func(t *testing.T) {
			results := make(map[string]map[string]any)
			for _, variant := range []string{"start_then_status", "start_and_wait"} {
				d := agentFixture("Saved")
				cfg := uiConfig()
				cfg.Workflows, cfg.MeasureWireBytes = true, true
				cfg.Allow = []string{"mouse_click", "keyboard_press"}
				c, svc := connectProfileDesktop(t, cfg, d, profile)
				args := map[string]any{"action": "start", "window_id": "mock-window", "steps": workflowPlan(), "max_actions": 2, "timeout_ms": 5000}
				if variant == "start_and_wait" {
					args["wait_ms"], args["after_event"] = 3000, 0
				}
				out := call(t, c, "workflow_control", args, true)
				if variant == "start_then_status" {
					r := waitWorkflow(t, svc)
					out = call(t, c, "workflow_control", map[string]any{"action": "status", "workflow_id": r.ID}, true)
				}
				r := decodeWorkflow(t, fieldsData(t, out))
				if r.State != "success" || r.Checkpoint != 2 || r.EventCursor != 2 || len(d.actions()) != 2 || len(out.Content) != 1 {
					t.Fatal("economy changed the observed result", r, d.actions())
				}
				results[variant] = svc.metrics.snapshot()
			}
			before, after := results["start_then_status"], results["start_and_wait"]
			if before["tool_calls"].(int64) != 2 || after["tool_calls"].(int64) != 1 || after["serialized_result_bytes"].(int64) >= before["serialized_result_bytes"].(int64) || after["images_returned"].(int64) != 0 {
				t.Fatal("workflow wait did not reduce protocol overhead", results)
			}
			measurement := map[string]any{"profile": profile, "environment": "paired synthetic workflow; same two verified actions", "calls_before": before["tool_calls"], "calls_after": after["tool_calls"], "serialized_bytes_before": before["serialized_result_bytes"], "serialized_bytes_after": after["serialized_result_bytes"], "images_after": after["images_returned"], "scope": "CallToolResult JSON; not Codex tokens, subscription quota or model quality"}
			encoded, err := json.Marshal(measurement)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("ECONOMY_RESULT %s", encoded)
		})
	}
}

func TestWorkflowJournalCursorOmitsAlreadyDeliveredEvents(t *testing.T) {
	d := agentFixture("Saved")
	cfg := uiConfig()
	cfg.Workflows = true
	cfg.Allow = []string{"mouse_click", "keyboard_press"}
	c, svc := connectDesktop(t, cfg, d)
	call(t, c, "workflow_control", map[string]any{"action": "start", "window_id": "mock-window", "steps": workflowPlan(), "max_actions": 2, "timeout_ms": 5000, "wait_ms": 3000}, true)
	r := svc.workflows.snapshot()
	for cursor, count := range map[int]int{0: 2, 1: 1, 2: 0} {
		out := call(t, c, "workflow_control", map[string]any{"action": "status", "workflow_id": r.ID, "after_event": cursor}, true)
		report := decodeWorkflow(t, fieldsData(t, out))
		if report.EventCursor != 2 || len(report.Journal) != count || report.State != "success" {
			t.Fatal("wrong journal delta", cursor, report)
		}
	}
	call(t, c, "workflow_control", map[string]any{"action": "status", "workflow_id": r.ID, "after_event": 3}, false)
	if len(d.actions()) != 2 {
		t.Fatal("status replayed input")
	}
}

func TestWorkflowWaitDoesNotBlockCancellation(t *testing.T) {
	d := agentFixture("Saved")
	cfg := uiConfig()
	cfg.Workflows = true
	cfg.Allow = []string{"mouse_click"}
	c, svc := connectDesktop(t, cfg, d)
	unlock, err := svc.lockTool(t.Context(), "keyboard_press")
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	call(t, c, "workflow_control", map[string]any{"action": "start", "window_id": "mock-window", "steps": workflowPlan()[:1], "max_actions": 1, "timeout_ms": 5000}, true)
	id := svc.workflows.snapshot().ID
	pending := asyncCall(c, "workflow_control", map[string]any{"action": "status", "workflow_id": id, "wait_ms": 3000, "after_event": 0})
	call(t, c, "workflow_control", map[string]any{"action": "cancel", "workflow_id": id}, true)
	reply := awaitReply(t, pending)
	if reply.err != nil || reply.r == nil || reply.r.IsError {
		t.Fatal("waiting status did not finish", fmt.Sprint(reply))
	}
	r := decodeWorkflow(t, fieldsData(t, reply.r))
	if r.State != "cancelled" || len(d.actions()) != 0 {
		t.Fatal(r, d.actions())
	}
}

func TestWorkflowWaitBudgetReturnsRunningWithoutReplaying(t *testing.T) {
	d := agentFixture("Saved")
	cfg := uiConfig()
	cfg.Workflows = true
	cfg.Allow = []string{"mouse_click"}
	c, svc := connectDesktop(t, cfg, d)
	unlock, err := svc.lockTool(t.Context(), "keyboard_press")
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	start := map[string]any{"action": "start", "window_id": "mock-window", "steps": workflowPlan()[:1], "max_actions": 1, "timeout_ms": 5000, "wait_ms": 100}
	for _, invalid := range []map[string]any{{"wait_ms": -1}, {"wait_ms": 30001}, {"after_event": 1}} {
		args := make(map[string]any, len(start)+1)
		for k, v := range start {
			args[k] = v
		}
		for k, v := range invalid {
			args[k] = v
		}
		call(t, c, "workflow_control", args, false)
		if svc.workflows.snapshot().ID != "" {
			t.Fatal("invalid wait started workflow")
		}
	}
	out := call(t, c, "workflow_control", start, true)
	r := decodeWorkflow(t, fieldsData(t, out))
	if r.State != "running" || r.Checkpoint != 0 || len(d.actions()) != 0 {
		t.Fatal(r)
	}
	latest := decodeWorkflow(t, fieldsData(t, call(t, c, "workflow_control", map[string]any{"action": "status", "after_event": 0}, true)))
	if latest.ID != r.ID {
		t.Fatal("cannot recover latest workflow ID")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := svc.workflowControl(ctx, WorkflowArgs{Action: "status", ID: r.ID, WaitMS: 1000}); err != context.Canceled {
		t.Fatal("waiting status ignored client cancellation", err)
	}
	call(t, c, "workflow_control", map[string]any{"action": "cancel", "workflow_id": r.ID}, true)
	if stopped := waitWorkflow(t, svc); stopped.State != "cancelled" || len(d.actions()) != 0 {
		t.Fatal(stopped)
	}
}
