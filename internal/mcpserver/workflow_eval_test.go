package mcpserver

import (
	"encoding/json"
	"testing"
	"time"

	"example.com/codex-vision/internal/platform"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Paired scripted driver-contract evaluations. No LLM, website, command or CAD
// application runs here. Keep those distinctions in the machine-readable report.
func TestAgentWorkflowScenarios(t *testing.T) {
	for _, scenario := range []struct {
		name, goal               string
		fields, visual, keyboard bool
	}{
		{"browser_like", "Page ready", false, true, false},
		{"form_like", "Form saved", true, false, false},
		{"terminal_like", "Command complete", false, false, true},
		{"cad_like", "Document ready", false, true, false},
	} {
		for _, unified := range []bool{false, true} {
			variant := "separate_tools"
			if unified {
				variant = "unified_tools"
			}
			t.Run(scenario.name+"/"+variant, func(t *testing.T) {
				d := agentFixture(scenario.goal)
				c, svc := agentClient(t, d)
				started := time.Now()
				var view agentData
				var obsID string
				if unified {
					view = agentResult(t, call(t, c, "desktop_observe", map[string]any{"include_fields": scenario.fields, "interval_ms": 250, "min_delivery_ms": 250}, true))
					obsID = view.View.ObservationID
				} else {
					call(t, c, "window_get_active", map[string]any{}, true)
					status := observeStatus(t, call(t, c, "observation_start", map[string]any{"window_id": "mock-window", "watch_region": platform.Rect{Width: 640, Height: 480}, "preview": false, "interval_ms": 250, "min_delivery_ms": 250}, true))
					obsID = status.ID
					if !scenario.keyboard {
						call(t, c, "ui_controls", map[string]any{"window_id": "mock-window", "compact": false}, true)
					}
					if scenario.fields {
						view.Fields = fieldsData(t, call(t, c, "ui_snapshot", map[string]any{"window_id": "mock-window", "compact": true}, true))
					}
				}
				if scenario.fields {
					call(t, c, "ui_fill_batch", map[string]any{"snapshot_id": view.Fields["snapshot_id"], "fields": batchFields()}, true)
				}
				step := map[string]any{"action": "click", "x": 90, "y": 55}
				if scenario.keyboard {
					step = map[string]any{"action": "press", "key": "SPACE"}
				} else if unified {
					step = map[string]any{"action": "click", "target_id": view.Controls[0].ID}
				}
				mode := "auto"
				if scenario.visual {
					mode = "image"
				}
				args := map[string]any{"steps": []map[string]any{step}, "expect": map[string]any{"title_contains": scenario.goal}, "result_mode": mode, "settle_ms": 100, "wait_ms": 2500}
				tool := "desktop_batch"
				if unified {
					tool = "desktop_act"
					args["view_id"] = view.View.ID
				} else {
					args["observation_id"] = obsID
					args["window_id"] = "mock-window"
				}
				out := agentResult(t, call(t, c, tool, args, true))
				if !out.Verification.Matched || out.Completed != 1 {
					t.Fatal("postcondition not verified", out)
				}
				metrics := svc.metrics.snapshot()
				report := map[string]any{"scenario": scenario.name, "variant": variant, "environment": "synthetic scripted fixture", "passed": true, "tool_calls": metrics["tool_calls"], "images": metrics["images_returned"], "result_content_bytes": metrics["result_content_bytes"], "tool_errors": metrics["tool_errors"], "posted_actions": len(d.actions()), "elapsed_ms": time.Since(started).Milliseconds(), "scripted_manual_interventions": 0, "verification": "exact synthetic window-title predicate"}
				encoded, _ := json.Marshal(report)
				t.Logf("EVAL_RESULT %s", encoded)
			})
		}
	}
}

// Decode only structural field data, never values, for the scripted baseline.
func fieldsData(t *testing.T, r *mcp.CallToolResult) map[string]any {
	t.Helper()
	// Implemented through the same typed MCP result helper as the unified path.
	data := r
	var out map[string]any
	if err := json.Unmarshal([]byte(data.Content[0].(*mcp.TextContent).Text), &out); err != nil {
		t.Fatal(err)
	}
	return out
}
