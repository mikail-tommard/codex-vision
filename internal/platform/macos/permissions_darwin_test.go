//go:build darwin && cgo

package macos

import (
	"context"
	"example.com/codex-vision/internal/platform"
	"os"
	"strings"
	"testing"
)

func TestNativeAndGoPermissionReasonParity(t *testing.T) {
	for _, tc := range permissionCases() {
		t.Run(tc.name, func(t *testing.T) {
			s := allowedState()
			tc.mutate(&s)
			if code := nativeStateReason(s); code != tc.code {
				t.Fatal(code, tc.code)
			}
		})
	}
}
func TestPermissionDiagnosticsFreshAndPrompted(t *testing.T) {
	d, _ := New()
	state := allowedState()
	state.AccessibilityPermission = false
	reads, prompts := 0, 0
	d.readPermissions = func() PermissionState { reads++; return state }
	d.requestPermission = func() { prompts++; state.AccessibilityPermission = true }
	ctx := context.Background()
	value, e := d.MacOSPermissionDiagnostics(ctx, false)
	if e != nil {
		t.Fatal(e)
	}
	report := value.(map[string]any)
	if report["accessibility_permission"] != false || report["blocking_reason"] != "accessibility_permission_denied" || reads != 1 || prompts != 0 {
		t.Fatal(report, reads, prompts)
	}
	value, e = d.MacOSPermissionDiagnostics(ctx, true)
	if e != nil {
		t.Fatal(e)
	}
	report = value.(map[string]any)
	if report["input_available"] != true || report["accessibility_prompt_requested"] != true || reads != 3 || prompts != 1 {
		t.Fatal(report, reads, prompts)
	}
	state.ScreenLocked = flag(true)
	value, e = d.MacOSPermissionDiagnostics(ctx, false)
	if e != nil {
		t.Fatal(e)
	}
	report = value.(map[string]any)
	if report["blocking_reason"] != "console_locked" || report["accessibility_permission"] != true || report["console_unlocked"] != false {
		t.Fatal(report)
	}
	state.ScreenLocked = flag(false)
	state.AccessibilityPermission = false
	value, e = d.MacOSPermissionDiagnostics(ctx, false)
	if e != nil || value.(map[string]any)["accessibility_permission"] != false {
		t.Fatal("revocation cached", e)
	}
}
func TestDiagnosticIdentityAndNoUnexpectedPrompt(t *testing.T) {
	d, _ := New()
	prompts := 0
	d.requestPermission = func() { prompts++ }
	value, e := d.MacOSPermissionDiagnostics(context.Background(), false)
	if e != nil {
		t.Fatal(e)
	}
	r := value.(map[string]any)
	path, _ := os.Executable()
	if r["pid"] != os.Getpid() || r["executable_path"] != path || r["permission_state_cached"] != false || prompts != 0 {
		t.Fatal(r)
	}
	if _, ok := r["accessibility_permission"].(bool); !ok {
		t.Fatal("cgo Boolean was not normalized")
	}
	if _, ok := r["console_unlocked"].(bool); !ok {
		t.Fatal("console Boolean was not normalized")
	}
}
func TestCancelledDiagnosticsNeverPrompt(t *testing.T) {
	d, _ := New()
	d.requestPermission = func() { t.Fatal("prompt after cancellation") }
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := d.MacOSPermissionDiagnostics(ctx, true); e == nil {
		t.Fatal("cancellation ignored")
	}
}
func TestKeyboardPermissionErrorsBeforeAnyEvent(t *testing.T) {
	for _, tc := range permissionCases() {
		if tc.code == "" {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			d, _ := New()
			state := allowedState()
			tc.mutate(&state)
			d.readPermissions = func() PermissionState { return state }
			for _, tool := range []string{"keyboard_type", "keyboard_hotkey", "keyboard_press"} {
				e := d.Execute(context.Background(), testAction(tool))
				if e == nil || !strings.HasPrefix(e.Error(), tc.code+":") {
					t.Fatal(tool, e)
				}
			}
		})
	}
}

func testAction(tool string) platform.Action {
	return platform.Action{Tool: tool, Text: "test", Keys: []string{"A"}}
}

func TestPromptDoesNotImplyPermission(t *testing.T) {
	d, _ := New()
	s := allowedState()
	s.AccessibilityPermission = false
	prompts := 0
	d.readPermissions = func() PermissionState { return s }
	d.requestPermission = func() { prompts++ }
	value, e := d.MacOSPermissionDiagnostics(context.Background(), true)
	if e != nil {
		t.Fatal(e)
	}
	r := value.(map[string]any)
	if prompts != 1 || r["input_available"] != false || r["accessibility_permission"] != false || r["accessibility_prompt_requested"] != true {
		t.Fatal(r)
	}
	s.AccessibilityPermission = true
	value, e = d.MacOSPermissionDiagnostics(context.Background(), false)
	if e != nil || value.(map[string]any)["input_available"] != true {
		t.Fatal("later grant was not refreshed", e)
	}
}
func TestCancellationDuringProbeDoesNotPrompt(t *testing.T) {
	d, _ := New()
	ctx, cancel := context.WithCancel(context.Background())
	d.readPermissions = func() PermissionState { cancel(); return PermissionState{} }
	d.requestPermission = func() { t.Fatal("prompt after cancelled probe") }
	if _, e := d.MacOSPermissionDiagnostics(ctx, true); e == nil {
		t.Fatal("cancellation ignored")
	}
}
