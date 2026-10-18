package mcpserver

import (
	"context"
	"encoding/json"
	"example.com/codex-vision/internal/config"
	"example.com/codex-vision/internal/platform"
	"example.com/codex-vision/internal/platform/mock"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"strings"
	"testing"
	"time"
)

type semanticDesktop struct {
	mock.Desktop
	fields []platform.UIElement
}

func (d *semanticDesktop) ReadUI(ctx context.Context, w platform.Window, n int) (platform.UISnapshot, error) {
	return platform.UISnapshot{Elements: append([]platform.UIElement(nil), d.fields...), Visited: len(d.fields)}, ctx.Err()
}
func fieldsDesktop() *semanticDesktop {
	return &semanticDesktop{fields: []platform.UIElement{
		{Label: "Email", Role: "AXTextField", Enabled: true, Editable: true, Path: []int{0}, Identity: "private-handle", Bounds: platform.Rect{X: 20, Y: 40, Width: 200, Height: 30}},
		{Label: "Password", Role: "AXTextField", Enabled: true, Secure: true, Path: []int{1}},
		{Label: "Duplicate", Role: "AXTextField", Enabled: true, Editable: true, Path: []int{2}},
		{Label: "Duplicate", Role: "AXTextField", Enabled: true, Editable: true, Path: []int{3}},
	}}
}
func uiConfig() config.Config {
	c := config.Default()
	c.Mode = "assist"
	c.LocalApproval = true
	c.ConfirmEachAction = false
	c.AllowedProcesses = []string{"mock.exe"}
	c.Allow = []string{"ui_fill"}
	return c
}
func snapshotData(t *testing.T, r *mcp.CallToolResult) (string, []platform.UIElement) {
	t.Helper()
	var out struct {
		ID       string               `json:"snapshot_id"`
		Elements []platform.UIElement `json:"elements"`
	}
	if err := json.Unmarshal([]byte(r.Content[0].(*mcp.TextContent).Text), &out); err != nil {
		t.Fatal(err)
	}
	return out.ID, out.Elements
}
func TestSemanticFillContract(t *testing.T) {
	d := fieldsDesktop()
	c, svc := connectDesktop(t, uiConfig(), d)
	r := call(t, c, "ui_snapshot", map[string]any{"window_id": "mock-window"}, true)
	id, elements := snapshotData(t, r)
	if id == "" || len(elements) != 4 || elements[0].Identity != "" || elements[0].Path != nil {
		t.Fatal("private handles exposed", elements)
	}
	call(t, c, "ui_fill", map[string]any{"snapshot_id": id, "label": "Duplicate", "text": "test"}, false)
	call(t, c, "ui_fill", map[string]any{"snapshot_id": id, "label": "Password", "text": "test"}, false)
	r = call(t, c, "ui_fill", map[string]any{"snapshot_id": id, "label": "Email", "text": "user@example.test"}, true)
	if len(d.Actions) != 1 || d.Actions[0].Tool != "ui_fill" || d.Actions[0].WindowID != "mock-window" || d.Actions[0].UIExpected.Identity != "private-handle" {
		t.Fatal("wrong action", d.Actions)
	}
	if strings.Contains(r.Content[0].(*mcp.TextContent).Text, "user@example.test") {
		t.Fatal("fill echoed contents")
	}
	call(t, c, "ui_fill", map[string]any{"snapshot_id": id, "element_id": elements[0].ID, "text": "duplicate write"}, false)
	r = call(t, c, "ui_snapshot", map[string]any{"window_id": "mock-window", "query": "email"}, true)
	next, filtered := snapshotData(t, r)
	if len(filtered) != 1 {
		t.Fatal(filtered)
	}
	call(t, c, "ui_fill", map[string]any{"snapshot_id": id, "element_id": elements[2].ID, "text": "stale"}, false)
	svc.uiMu.Lock()
	svc.ui.Expires = time.Now().Add(-time.Second)
	svc.uiMu.Unlock()
	call(t, c, "ui_fill", map[string]any{"snapshot_id": next, "element_id": filtered[0].ID, "text": "expired"}, false)
	call(t, c, "session_stop", map[string]any{}, true)
	call(t, c, "ui_snapshot", map[string]any{"window_id": "mock-window"}, false)
}
func TestSemanticFillPolicyAndSnapshotBounds(t *testing.T) {
	for _, alter := range []func(*config.Config){func(c *config.Config) { c.Mode = "observe" }, func(c *config.Config) { c.Allow = nil }, func(c *config.Config) { c.AllowedProcesses = []string{"other.exe"} }, func(c *config.Config) { c.LocalApproval = false; c.Allow = nil }} {
		cfg := uiConfig()
		alter(&cfg)
		d := fieldsDesktop()
		client, _ := connectDesktop(t, cfg, d)
		r := call(t, client, "ui_snapshot", map[string]any{"window_id": "mock-window"}, true)
		id, elements := snapshotData(t, r)
		call(t, client, "ui_fill", map[string]any{"snapshot_id": id, "element_id": elements[0].ID, "text": "blocked"}, false)
		if len(d.Actions) != 0 {
			t.Fatal("policy bypass")
		}
	}
	client, _ := connect(t, config.Default())
	call(t, client, "ui_snapshot", map[string]any{"window_id": "mock-window"}, false)
	client, _ = connectDesktop(t, uiConfig(), fieldsDesktop())
	call(t, client, "ui_snapshot", map[string]any{"window_id": "wrong"}, false)
	call(t, client, "ui_snapshot", map[string]any{"window_id": "mock-window", "max_nodes": 2001}, false)
}
