package mcpserver

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestInvalidActPreservesViewForCorrectedRequest(t *testing.T) {
	for _, tc := range []struct {
		name    string
		invalid map[string]any
	}{
		{"settle_too_small", map[string]any{"settle_ms": 50}},
		{"settle_too_large", map[string]any{"settle_ms": 2001}},
		{"wait_negative", map[string]any{"wait_ms": -1}},
		{"wait_too_small", map[string]any{"wait_ms": 99}},
		{"wait_too_large", map[string]any{"wait_ms": 30001}},
		{"wait_mode", map[string]any{"wait_for": "unknown"}},
		{"result_mode", map[string]any{"result_mode": "unknown"}},
		{"motion", map[string]any{"motion": "unknown"}},
		{"invalid_key", map[string]any{"steps": []map[string]any{{"action": "press", "key": "NO_KEY"}}}},
		{"invalid_expectation", map[string]any{"expect": map[string]any{"title_contains": "Saved", "control_label": "Execute"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := agentFixture("Saved")
			c, svc := agentClient(t, d)
			v := agentResult(t, call(t, c, "desktop_observe", map[string]any{}, true))
			args := agentClick(v.View.ID, v.Controls[0].ID, "Saved 1")
			for k, value := range tc.invalid {
				args[k] = value
			}
			r, err := c.CallTool(context.Background(), &mcp.CallToolParams{Name: "desktop_act", Arguments: args})
			if err == nil && (r == nil || !r.IsError) {
				t.Fatal("invalid request accepted", r)
			}
			svc.views.mu.Lock()
			retained := svc.views.current != nil && svc.views.current.ID == v.View.ID
			svc.views.mu.Unlock()
			if !retained || len(d.actions()) != 0 {
				t.Fatal("validation consumed view or posted input", retained, d.actions())
			}
			out := agentResult(t, call(t, c, "desktop_act", agentClick(v.View.ID, v.Controls[0].ID, "Saved 1"), true))
			if !out.Verification.Matched || len(d.actions()) != 1 {
				t.Fatal("corrected request could not use same view", out)
			}
		})
	}
}

func TestInvalidWaitNeverRefocusesOrConsumesView(t *testing.T) {
	d := agentFixture("Saved")
	c, svc := agentClient(t, d)
	v := agentResult(t, call(t, c, "desktop_observe", map[string]any{}, true))
	d.stateMu.Lock()
	d.window.ID = "other-window"
	d.stateMu.Unlock()
	// Call handler directly as well: transport/schema validation is not the
	// only protection against invalid options reaching input/refocus.
	_, err := svc.desktopAct(context.Background(), DesktopActArgs{ViewID: v.View.ID, Steps: []DesktopStep{{Action: "press", Key: "SPACE"}}, WaitMS: 99})
	if err == nil || len(d.actions()) != 0 {
		t.Fatal("invalid wait performed focus/input", err, d.actions())
	}
	svc.views.mu.Lock()
	defer svc.views.mu.Unlock()
	if svc.views.current == nil || svc.views.current.ID != v.View.ID || svc.views.current.FocusAttempted {
		t.Fatal("invalid wait changed view")
	}
}

func TestActionWaitSchemasAndDefaults(t *testing.T) {
	d := agentFixture("Saved")
	c, svc := agentClient(t, d)
	listed, err := c.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range listed.Tools {
		if tool.Name != "desktop_act" && tool.Name != "desktop_batch" {
			continue
		}
		// Round-trip the public schema to inspect what an MCP client sees.
		encoded, _ := json.Marshal(tool.InputSchema)
		var schema struct {
			Properties map[string]struct {
				AnyOf []struct {
					Minimum, Maximum *float64
					Const            *float64
				}
			}
		}
		if err := json.Unmarshal(encoded, &schema); err != nil {
			t.Fatal(err)
		}
		for name, max := range map[string]float64{"settle_ms": 2000, "wait_ms": 30000} {
			ranges := schema.Properties[name].AnyOf
			if len(ranges) != 2 || ranges[0].Const == nil || *ranges[0].Const != 0 || ranges[1].Minimum == nil || *ranges[1].Minimum != 100 || ranges[1].Maximum == nil || *ranges[1].Maximum != max {
				t.Fatal("missing public wait bounds", tool.Name, name, string(encoded))
			}
		}
	}
	for _, a := range []DesktopBatchArgs{
		{Steps: []DesktopStep{{Action: "press", Key: "SPACE"}}},
		{Steps: []DesktopStep{{Action: "press", Key: "SPACE"}}, SettleMS: 100, WaitMS: 100},
		{Steps: []DesktopStep{{Action: "press", Key: "SPACE"}}, SettleMS: 2000, WaitMS: 30000},
	} {
		if err := svc.prepareBatchOptions(&a); err != nil || a.WaitMS < 100 || a.SettleMS < 100 {
			t.Fatal("valid bounds/defaults rejected", a, err)
		}
	}
}
