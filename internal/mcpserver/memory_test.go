package mcpserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMemoryLearnsOnlyVerifiedNativeLocators(t *testing.T) {
	d := agentFixture("Saved")
	cfg := uiConfig()
	cfg.UIMemoryDirectory, cfg.UIMemoryNamespace, cfg.UIMemoryTTLHours = filepath.Join(t.TempDir(), "private"), "test-session", 1
	cfg.Allow = []string{"mouse_click"}
	c, svc := connectDesktop(t, cfg, d)
	if svc.memory == nil {
		t.Fatal("memory startup failed", svc.memoryStatus())
	}
	view := agentResult(t, call(t, c, "desktop_observe", map[string]any{"include_controls": true}, true))
	args := map[string]any{"view_id": view.View.ID, "steps": []map[string]any{{"action": "click", "target": map[string]any{"label": "Execute", "role": "AXButton"}}}, "expect": map[string]any{"title_contains": "Saved 1"}, "settle_ms": 100, "wait_ms": 1500}
	call(t, c, "desktop_act", args, true)
	if svc.memory.Stats()["entries"].(int) != 1 {
		t.Fatal("verified locator not remembered", svc.memoryStatus())
	}
	files, err := os.ReadDir(cfg.UIMemoryDirectory)
	if err != nil || len(files) != 1 {
		t.Fatal(files, err)
	}
	data, err := os.ReadFile(filepath.Join(cfg.UIMemoryDirectory, files[0].Name()))
	if err != nil || strings.Contains(string(data), "Execute") || strings.Contains(string(data), "mock.exe") || strings.Contains(string(data), "bounds") {
		t.Fatal("UI content persisted", string(data), err)
	}
	// A known locator still needs fresh geometry; stored receipts cannot permit
	// clicking its old position after an external layout change.
	fresh := agentResult(t, call(t, c, "desktop_observe", map[string]any{"include_controls": true}, true))
	d.stateMu.Lock()
	d.offset = 20
	d.stateMu.Unlock()
	args["view_id"], args["expect"] = fresh.View.ID, map[string]any{"title_contains": "Saved 2"}
	call(t, c, "desktop_act", args, false)
	if len(d.actions()) != 1 {
		t.Fatal("memory bypassed freshness check")
	}
	call(t, c, "ui_memory_control", map[string]any{"action": "clear"}, true)
	if svc.memory.Stats()["entries"].(int) != 0 {
		t.Fatal("memory clear failed")
	}
}
