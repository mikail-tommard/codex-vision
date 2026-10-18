package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"example.com/codex-vision/internal/config"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Run the real entry point in a child test executable. os.Exit prevents the Go
// testing PASS banner from entering protocol stdout after the server exits.
func TestStdioHelper(t *testing.T) {
	if os.Getenv("CODEX_VISION_TEST_HELPER") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Args = append([]string{os.Args[0]}, os.Args[i+1:]...)
			break
		}
	}
	flag.CommandLine = flag.NewFlagSet("codex-vision", flag.ExitOnError)
	if err := run(); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

func TestRealStdioProcess(t *testing.T) {
	dir := t.TempDir()
	c := config.Default()
	c.AuditFile = filepath.Join(dir, "audit.jsonl")
	c.StopFile = filepath.Join(dir, "stop")
	cfg := filepath.Join(dir, "config.json")
	data, _ := json.Marshal(c)
	if e := os.WriteFile(cfg, data, 0600); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestStdioHelper$", "--", "--mock", "--config", cfg)
	cmd.Env = append(os.Environ(), "CODEX_VISION_TEST_HELPER=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	client := mcp.NewClient(&mcp.Implementation{Name: "stdio-contract-test", Version: "1"}, nil)
	session, e := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer session.Close()
	tools, e := session.ListTools(ctx, nil)
	if e != nil || len(tools.Tools) != 29 {
		t.Fatal(e)
	}
	r, e := session.CallTool(ctx, &mcp.CallToolParams{Name: "screen_capture", Arguments: map[string]any{"monitor_id": "mock-primary", "max_width": 160}})
	if e != nil || r.IsError {
		t.Fatal(e, r)
	}
	im, ok := r.Content[1].(*mcp.ImageContent)
	if !ok {
		t.Fatal("missing image")
	}
	decoded, _, e := image.Decode(bytes.NewReader(im.Data))
	if e != nil || decoded.Bounds().Dx() != 160 {
		t.Fatal(e)
	}
	r, e = session.CallTool(ctx, &mcp.CallToolParams{Name: "desktop_observe", Arguments: map[string]any{}})
	if e != nil || r.IsError || len(r.Content) != 2 {
		t.Fatal("unified stdio observation failed", e, r)
	}
	var unified struct {
		View struct {
			ID string `json:"view_id"`
		} `json:"view"`
	}
	if e = json.Unmarshal([]byte(r.Content[0].(*mcp.TextContent).Text), &unified); e != nil || unified.View.ID == "" {
		t.Fatal("missing unified view", e)
	}
	r, e = session.CallTool(ctx, &mcp.CallToolParams{Name: "desktop_act", Arguments: map[string]any{"view_id": unified.View.ID, "steps": []map[string]any{{"action": "click", "x": 20, "y": 20}}, "wait_ms": 500}})
	if e != nil || !r.IsError || !bytes.Contains([]byte(r.Content[0].(*mcp.TextContent).Text), []byte("access_denied")) {
		t.Fatal("unified tool bypassed observe policy", e, r)
	}
	r, e = session.CallTool(ctx, &mcp.CallToolParams{Name: "mouse_move", Arguments: map[string]any{"x": 1, "y": 1}})
	if e != nil || !r.IsError {
		t.Fatal("observe input was not blocked", e)
	}
	if e = session.Close(); e != nil {
		t.Fatal(e)
	}
	audit, e := os.ReadFile(c.AuditFile)
	if e != nil || !bytes.Contains(audit, []byte("screen_capture")) {
		t.Fatal("audit missing", e)
	}
	if !bytes.Contains(stderr.Bytes(), []byte("audit")) {
		t.Fatal("stderr logs missing")
	}
}
