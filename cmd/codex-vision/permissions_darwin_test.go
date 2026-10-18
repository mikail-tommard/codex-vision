//go:build darwin && cgo

package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"example.com/codex-vision/internal/config"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// This launches the real native entry point, not --mock, and only reads
// permissions. It is safe without a GUI or Accessibility consent.
func TestNativePermissionToolUsesServerPID(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.AuditFile = filepath.Join(dir, "audit.jsonl")
	cfg.StopFile = filepath.Join(dir, "stop")
	path := filepath.Join(dir, "config.json")
	data, _ := json.Marshal(cfg)
	if e := os.WriteFile(path, data, 0600); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestStdioHelper$", "--", "--config", path)
	cmd.Env = append(os.Environ(), "CODEX_VISION_TEST_HELPER=1")
	client := mcp.NewClient(&mcp.Implementation{Name: "native-permission-test", Version: "1"}, nil)
	session, e := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer session.Close()
	read := func() map[string]any {
		t.Helper()
		r, e := session.CallTool(ctx, &mcp.CallToolParams{Name: "macos_permission_diagnostics", Arguments: map[string]any{}})
		if e != nil || r.IsError {
			t.Fatal(r, e)
		}
		var out map[string]any
		if e = json.Unmarshal([]byte(r.Content[0].(*mcp.TextContent).Text), &out); e != nil {
			t.Fatal(e)
		}
		return out
	}
	a := read()
	b := read()
	if a["supported"] != true || int(a["pid"].(float64)) != cmd.Process.Pid {
		t.Fatal("diagnostic did not come from MCP process", a)
	}
	if a["checked_at"] == b["checked_at"] || a["permission_state_cached"] != false {
		t.Fatal("diagnostics cached")
	}
	for _, field := range []string{"accessibility_permission", "console_unlocked", "input_available", "post_event_permission"} {
		if _, ok := a[field].(bool); !ok {
			t.Fatal(field, a[field])
		}
	}
	if a["accessibility_prompt_requested"] != false {
		t.Fatal("unexpected consent prompt")
	}
	if _, e = session.CallTool(ctx, &mcp.CallToolParams{Name: "session_stop", Arguments: map[string]any{}}); e != nil {
		t.Fatal(e)
	}
	read()
}
