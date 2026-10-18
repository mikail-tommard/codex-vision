package mcpserver

import (
	"context"
	"encoding/json"
	"example.com/codex-vision/internal/platform"
	"example.com/codex-vision/internal/platform/mock"
	"fmt"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type focusDesktop struct {
	mock.Desktop
	broken      atomic.Bool
	other       atomic.Bool
	activeCalls atomic.Int32
}

func (d *focusDesktop) Active(ctx context.Context) (platform.Window, error) {
	d.activeCalls.Add(1)
	if d.broken.Load() {
		return platform.Window{}, fmt.Errorf("cannot read focused application (AX error -25212)")
	}
	w, err := d.Desktop.Active(ctx)
	if d.other.Load() {
		w.ID = "other-window"
	}
	return w, err
}
func TestFocusGuardPreventsRepeatedNativeInputAndImages(t *testing.T) {
	d := &focusDesktop{}
	d.broken.Store(true)
	cfg := uiConfig()
	cfg.Allow = []string{"mouse_click"}
	c, svc := connectDesktop(t, cfg, d)
	start := call(t, c, "observation_start", map[string]any{"mode": "continuous", "preview": false}, true)
	obs := observeStatus(t, start)
	call(t, c, "mouse_click", map[string]any{"window_id": "mock-window", "x": 10, "y": 10}, false)
	if svc.recovery.status().Required {
		t.Fatal("guard opened after first failure")
	}
	r := call(t, c, "window_get_active", map[string]any{}, false)
	if !strings.Contains(r.Content[0].(*mcp.TextContent).Text, "focus_recovery_required") {
		t.Fatal("repeated failure did not explain recovery")
	}
	before := d.activeCalls.Load()
	blockedStart := call(t, c, "observation_start", map[string]any{"preview": false}, false)
	if len(blockedStart.Content) != 1 || !strings.Contains(blockedStart.Content[0].(*mcp.TextContent).Text, "focus_recovery_required") {
		t.Fatal("start bypassed recovery guard", blockedStart)
	}
	for i := 0; i < 5; i++ {
		call(t, c, "mouse_click", map[string]any{"window_id": "mock-window", "x": 10, "y": 10}, false)
	}
	r = call(t, c, "observation_next", map[string]any{"observation_id": obs.ID, "wait_ms": 0}, false)
	if len(r.Content) != 1 || d.activeCalls.Load() != before || len(d.Actions) != 0 || svc.Observer.Status().State != "running" {
		t.Fatal("guard performed input, captured model image or stopped local preview")
	}
	call(t, c, "window_list", map[string]any{}, true)
	d.broken.Store(false)
	d.other.Store(true)
	call(t, c, "window_get_active", map[string]any{}, true)
	if !svc.recovery.status().Required {
		t.Fatal("unrelated foreground cleared guard")
	}
	d.other.Store(false)
	call(t, c, "window_get_active", map[string]any{}, true)
	call(t, c, "mouse_click", map[string]any{"window_id": "mock-window", "x": 10, "y": 10}, true)
	if svc.recovery.status().Required || len(d.Actions) != 1 {
		t.Fatal("manual recovery failed")
	}
}
func TestRecoveryLocalWaitAndIndependentStop(t *testing.T) {
	d := &focusDesktop{}
	c, svc := connectDesktop(t, uiConfig(), d)
	svc.recovery.failure("mock-window", "cannot read focused application")
	svc.recovery.failure("mock-window", "no visible foreground window")
	call(t, c, "ui_wait", map[string]any{"condition": "window_active", "window_id": "mock-window", "timeout_ms": 100}, true)
	if svc.recovery.status().Required {
		t.Fatal("local recovery check did not clear guard")
	}
	svc.recovery.failure("mock-window", "no visible foreground window")
	svc.recovery.failure("mock-window", "no visible foreground window")
	call(t, c, "session_stop", map[string]any{}, true)
	if svc.Session.Status()["stopped"] != true {
		t.Fatal("guard blocked emergency stop")
	}
}
func TestFocusFailureWindowAndClassification(t *testing.T) {
	var g recoveryGuard
	g.failure("window", "bad URL")
	if g.status().Failures != 0 {
		t.Fatal("validation error tripped guard")
	}
	g.failure("window", "no visible foreground window")
	g.mu.Lock()
	g.last = time.Now().Add(-time.Minute)
	g.mu.Unlock()
	g.failure("window", "no visible foreground window")
	if g.status().Required || g.status().Failures != 1 {
		t.Fatal("unrelated old failure carried over")
	}
}
func TestObservationSequenceResyncWithoutImage(t *testing.T) {
	c, _ := connect(t, uiConfig())
	s := observeStatus(t, call(t, c, "observation_start", map[string]any{"preview": false}, true))
	r := call(t, c, "observation_next", map[string]any{"observation_id": s.ID, "after_sequence": 999, "wait_ms": 0}, true)
	var out struct {
		Resync map[string]any `json:"sequence_resync"`
	}
	if err := json.Unmarshal([]byte(r.Content[0].(*mcp.TextContent).Text), &out); err != nil || out.Resync == nil || len(r.Content) != 1 {
		t.Fatal("resync missing or image returned", err)
	}
}

func TestNativeFocusFallbackErrorsEnterRecoveryGuard(t *testing.T) {
	for _, message := range []string{"cannot confirm foreground process (native error -1)", "foreground changed during focus confirmation (native error 0)", "no accessible focused window (native error -25212; AX missing-value fallback used)"} {
		var g recoveryGuard
		g.failure("chrome", message)
		g.failure("chrome", message)
		if !g.status().Required {
			t.Fatal("unclassified focus failure", message)
		}
	}
}

func TestSessionStatusIdentifiesRunningServer(t *testing.T) {
	c, _ := connect(t, uiConfig())
	r := call(t, c, "session_status", map[string]any{}, true)
	var out struct {
		Server struct {
			Version    string `json:"version"`
			PID        int    `json:"pid"`
			Executable string `json:"executable"`
			Started    string `json:"started_at"`
		} `json:"server"`
	}
	if err := json.Unmarshal([]byte(r.Content[0].(*mcp.TextContent).Text), &out); err != nil {
		t.Fatal(err)
	}
	if out.Server.Version != serverVersion || out.Server.PID <= 0 || out.Server.Executable == "" || out.Server.Started == "" {
		t.Fatal("missing running server identity", out)
	}
}
