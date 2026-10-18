package mcpserver

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"os"
	"strings"
	"sync"
	"time"
)

// recoveryGuard limits repeated native attempts after focus failures, not total
// session usage. Only a confirmed foreground read clears an open guard.
type recoveryGuard struct {
	mu       sync.Mutex
	failures int
	last     time.Time
	target   string
	blocked  bool
}
type recoveryState struct {
	Required bool   `json:"required"`
	Failures int    `json:"focus_failures"`
	WindowID string `json:"expected_window_id,omitempty"`
}

func (g *recoveryGuard) status() recoveryState {
	g.mu.Lock()
	defer g.mu.Unlock()
	return recoveryState{g.blocked, g.failures, g.target}
}
func focusFailure(message string) bool {
	message = strings.ToLower(message)
	for _, part := range []string{"cannot read focused application", "cannot confirm foreground process", "foreground changed during focus confirmation", "no accessible focused window", "no visible foreground window", "cannot read focused window bounds", "did not confirm requested foreground window", "cannot uniquely match window to accessibility", "foreground not confirmed", "application launch requested but foreground not confirmed"} {
		if strings.Contains(message, part) {
			return true
		}
	}
	return false
}
func (g *recoveryGuard) failure(target, message string) {
	if !focusFailure(message) {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.blocked {
		return
	}
	if time.Since(g.last) > 30*time.Second {
		g.failures = 0
		g.target = ""
	}
	if g.target == "" {
		g.target = target
	}
	g.failures++
	g.last = time.Now()
	g.blocked = g.failures >= 2
}
func (g *recoveryGuard) confirm(id string) {
	if id == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.target != "" && g.target != id {
		return
	}
	g.failures = 0
	g.target = ""
	g.blocked = false
	g.last = time.Time{}
}
func recoveryResult(state recoveryState) *mcp.CallToolResult {
	out := result(map[string]any{"code": "focus_recovery_required", "retryable": false, "recovery": state, "next_action": "Stop this turn and report the focus failure. Do not cycle focus, launch, input, diagnostics or screenshots. After the user restores the expected window, verify it once with window_get_active or ui_wait(window_active). No server restart required. Local preview can continue."})
	out.IsError = true
	return out
}
func inputTool(name string) bool {
	switch name {
	case "mouse_move", "mouse_click", "mouse_drag", "mouse_scroll", "keyboard_type", "keyboard_press", "keyboard_hotkey", "window_focus", "app_launch", "ui_fill", "ui_fill_batch", "mouse_action_wait", "desktop_batch", "desktop_act":
		return true
	}
	return false
}
func (v *Service) failureTarget(args any) string {
	switch a := args.(type) {
	case DesktopObserveArgs:
		return a.WindowID
	case DesktopActArgs:
		v.views.mu.Lock()
		defer v.views.mu.Unlock()
		if v.views.current != nil && v.views.current.ID == a.ViewID {
			return v.views.current.Window.ID
		}
		return ""
	case DesktopBatchArgs:
		return a.WindowID
	case MouseActionWaitArgs:
		return a.WindowID
	case FocusArgs:
		return a.WindowID
	case ControlsArgs:
		return a.WindowID
	case ScrollArgs:
		return a.WindowID
	case PointArgs:
		return a.WindowID
	case ClickArgs:
		return a.WindowID
	case DragArgs:
		return a.WindowID
	case TextArgs:
		return a.WindowID
	case KeyArgs:
		return a.WindowID
	case HotkeyArgs:
		return a.WindowID
	case UISnapshotArgs:
		return a.WindowID
	case UIWaitArgs:
		return a.WindowID
	}
	snapshotID := ""
	switch a := args.(type) {
	case UIFillArgs:
		snapshotID = a.SnapshotID
	case UIBatchArgs:
		snapshotID = a.SnapshotID
	default:
		return ""
	}
	v.uiMu.Lock()
	defer v.uiMu.Unlock()
	if v.ui != nil && v.ui.ID == snapshotID {
		return v.ui.Window.ID
	}
	return ""
}
func (v *Service) recordFailure(name string, args any, out *mcp.CallToolResult, err error) {
	if err != nil {
		v.recovery.failure(v.failureTarget(args), err.Error())
		return
	}
	if out != nil && out.IsError {
		if meta, ok := out.StructuredContent.(map[string]any); ok {
			if message, ok := meta["error"].(string); ok {
				target, _ := meta["window_id"].(string)
				v.recovery.failure(target, message)
			}
		}
		if report, ok := out.StructuredContent.(*batchReport); ok {
			for _, field := range report.Fields {
				if field.Error != "" {
					v.recovery.failure(report.WindowID, field.Error)
					break
				}
			}
		}
	}
}
func (v *Service) sessionStatus() *mcp.CallToolResult {
	status := v.Session.Status()
	executable, _ := os.Executable()
	status["server"] = map[string]any{"version": serverVersion, "pid": os.Getpid(), "executable": executable, "started_at": v.started.UTC().Format(time.RFC3339), "tool_profile": v.Config.ToolProfile}
	status["recovery"] = v.recovery.status()
	status["metrics"] = v.metrics.snapshot()
	return result(status)
}
