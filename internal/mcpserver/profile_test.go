package mcpserver

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"example.com/codex-vision/internal/config"
	"example.com/codex-vision/internal/platform"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type countedControlsDesktop struct {
	*agentDesktop
	reads atomic.Int64
}

func (d *countedControlsDesktop) ReadControls(ctx context.Context, w platform.Window, n int) (platform.ControlSnapshot, error) {
	d.reads.Add(1)
	return d.agentDesktop.ReadControls(ctx, w, n)
}

func TestCompactCatalogAndWireResponses(t *testing.T) {
	for _, profile := range []string{"compact", "full"} {
		t.Run(profile, func(t *testing.T) {
			c, _ := connectProfileDesktop(t, config.Default(), agentFixture("Ready"), profile)
			listed, err := c.ListTools(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			want := 29
			if profile == "compact" {
				want = 12
			}
			if len(listed.Tools) != want {
				t.Fatalf("got %d tools, want %d", len(listed.Tools), want)
			}
			r := call(t, c, "session_status", map[string]any{}, true)
			if (r.StructuredContent == nil) != (profile == "compact") {
				t.Fatal("unexpected wire encoding", r)
			}
			var status map[string]any
			if err := json.Unmarshal([]byte(r.Content[0].(*mcp.TextContent).Text), &status); err != nil {
				t.Fatal(err)
			}
			if status["server"].(map[string]any)["tool_profile"] != profile {
				t.Fatal(status)
			}
			if profile == "compact" {
				_, err := c.CallTool(context.Background(), &mcp.CallToolParams{Name: "keyboard_press", Arguments: map[string]any{"key": "SPACE"}})
				if err == nil {
					t.Fatal("hidden primitive unexpectedly callable")
				}
			}
		})
	}
}

func TestCompactVisualActionsSkipControlsAndKeepNextView(t *testing.T) {
	d := &countedControlsDesktop{agentDesktop: agentFixture("Saved")}
	cfg := uiConfig()
	cfg.Allow = []string{"keyboard_press", "mouse_click", "window_focus", "ui_fill"}
	cfg.TimeoutMS = 6000
	c, _ := connectProfileDesktop(t, cfg, d, "compact")
	initial := call(t, c, "desktop_observe", map[string]any{}, true)
	v := agentResult(t, initial)
	if d.reads.Load() != 0 || len(v.Controls) != 0 || len(initial.Content) != 2 {
		t.Fatal("visual observe read UI or lost image", d.reads.Load(), v)
	}
	for _, goal := range []string{"Saved 1", "Saved 2"} {
		r := call(t, c, "desktop_act", map[string]any{"view_id": v.View.ID, "steps": []map[string]any{{"action": "press", "key": "SPACE"}}, "expect": map[string]any{"title_contains": goal}, "settle_ms": 100, "wait_ms": 2500}, true)
		v = agentResult(t, r)
		if !v.Verification.Matched || v.View.ID == "" || d.reads.Load() != 0 || r.StructuredContent != nil {
			t.Fatal("lost continuation, read unnecessary controls or duplicate metadata", v, d.reads.Load())
		}
	}
	// Explicit opt-in still supplies fresh targets and checks them before input.
	v = agentResult(t, call(t, c, "desktop_observe", map[string]any{"include_controls": true}, true))
	if d.reads.Load() == 0 || len(v.Controls) != 1 || v.Controls[0].ID == "" {
		t.Fatal("control discovery unavailable", v)
	}
	before := d.reads.Load()
	r := agentResult(t, call(t, c, "desktop_act", agentClick(v.View.ID, v.Controls[0].ID, "Saved 3"), true))
	if !r.Verification.Matched || d.reads.Load() <= before || len(r.Controls) != 1 {
		t.Fatal("opted-in control checks/refresh lost", r)
	}
}

func TestFullCanDisableControlDiscovery(t *testing.T) {
	d := &countedControlsDesktop{agentDesktop: agentFixture("Ready")}
	c, _ := connectProfileDesktop(t, config.Default(), d, "full")
	v := agentResult(t, call(t, c, "desktop_observe", map[string]any{"include_controls": false}, true))
	if len(v.Controls) != 0 || d.reads.Load() != 0 {
		t.Fatal("explicit opt-out ignored", v)
	}
}

func TestCompactPayloadBudget(t *testing.T) {
	sizes := map[string]int{}
	for _, profile := range []string{"full", "compact"} {
		c, _ := connectProfileDesktop(t, config.Default(), agentFixture("Ready"), profile)
		listed, err := c.ListTools(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(listed)
		if err != nil {
			t.Fatal(err)
		}
		sizes[profile] = len(encoded)
		r := call(t, c, "session_status", map[string]any{}, true)
		wire, _ := json.Marshal(r)
		t.Logf("profile=%s tools=%d catalog_bytes=%d status_wire_bytes=%d instructions_bytes=%d", profile, len(listed.Tools), len(encoded), len(wire), len(agentInstructions))
	}
	// Prevent accidental catalog growth from undoing the compact profile.
	if sizes["compact"]*2 >= sizes["full"] || len(agentInstructions) > 3000 {
		t.Fatal("compact discovery budget exceeded", sizes, len(agentInstructions))
	}
}

func TestCompactContinuousObservationAndRestartWithoutSessionStop(t *testing.T) {
	c, svc := connectProfileDesktop(t, config.Default(), agentFixture("Ready"), "compact")
	first := agentResult(t, call(t, c, "desktop_observe", map[string]any{"mode": "continuous"}, true))
	if svc.Observer.Status().Mode != "continuous" {
		t.Fatal("compact lost continuous capture")
	}
	call(t, c, "desktop_observe", map[string]any{"mode": "task"}, false)
	if svc.Observer.Status().ID != first.View.ObservationID {
		t.Fatal("mode mismatch replaced existing stream")
	}
	call(t, c, "observation_control", map[string]any{"action": "stop", "observation_id": first.View.ObservationID}, true)
	next := agentResult(t, call(t, c, "desktop_observe", map[string]any{}, true))
	if next.View.ID == "" || svc.Observer.Status().Mode != "task" {
		t.Fatal("normal stop prevented new task", next)
	}
}
