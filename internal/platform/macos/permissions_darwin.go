//go:build darwin && cgo

package macos

/*
#include <stdlib.h>
#include "bridge.h"
*/
import "C"
import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func tri(v C.int) *bool {
	if v != 0 && v != 1 {
		return nil
	}
	b := v == 1
	return &b
}
func nativePermissionState() PermissionState {
	var s C.cv_permission_state
	C.cv_read_permissions(&s)
	out := PermissionState{AccessibilityPermission: s.accessibility_permission == 1, PostEventPermission: s.post_event_permission == 1, ScreenCapturePermission: s.screen_capture_permission == 1, SessionAvailable: s.session_available == 1, OnConsole: tri(s.on_console), LoginDone: tri(s.login_done), ScreenLocked: tri(s.screen_locked), LockIndicatorPresent: s.lock_indicator_present == 1, EffectiveUID: uint32(s.effective_uid)}
	if s.session_uid_known == 1 {
		uid := uint32(s.session_uid)
		out.SessionUID = &uid
	}
	return out
}
func (d *Desktop) permissionState() PermissionState {
	if d.readPermissions != nil {
		return d.readPermissions()
	}
	return nativePermissionState()
}
func (d *Desktop) requestAccessibility() {
	if d.requestPermission != nil {
		d.requestPermission()
		return
	}
	C.cv_request_accessibility()
}
func (d *Desktop) MacOSPermissionDiagnostics(ctx context.Context, request bool) (any, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	state := d.permissionState()
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	requested := false
	if request && !state.AccessibilityPermission {
		d.requestAccessibility()
		requested = true
		state = d.permissionState()
	}
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	var identity map[string]any
	if e := decode(C.cv_process_identity(), &identity); e != nil {
		return nil, e
	}
	executable, e := os.Executable()
	if e != nil {
		executable = ""
	}
	realPath, _ := filepath.EvalSymlinks(executable)
	reasons := state.BlockingReasons()
	code := ""
	message := ""
	if len(reasons) > 0 {
		code = reasons[0].Code
		message = reasons[0].Message
	}
	out := map[string]any{
		"supported": true, "backend": d.Name(), "pid": os.Getpid(), "parent_pid": os.Getppid(), "uid": os.Getuid(), "effective_uid": os.Geteuid(), "executable_path": executable, "executable_real_path": realPath, "architecture": runtime.GOARCH,
		"accessibility_permission": state.AccessibilityPermission, "post_event_permission": state.PostEventPermission, "screen_capture_permission": state.ScreenCapturePermission, "console_unlocked": state.ConsoleReason() == nil, "input_available": state.InputAvailable(), "input_delivery_verified": false, "native_guard_reason": nativeStateReason(state),
		"input_scope": "OS preflight only; server mode, stop, allowlists, bounds and action budgets also apply", "blocking_reason": code, "blocking_message": message, "blocking_reasons": reasons, "session_state": state, "checked_at": time.Now().UTC().Format(time.RFC3339Nano), "permission_state_cached": false,
		"accessibility_prompt_requested": requested, "accessibility_prompt_asynchronous": true, "prompt_note": "AXIsProcessTrustedWithOptions requests OS consent; it does not grant permission and may return false until the user changes settings. Query this same MCP process again afterwards.",
		"native_checks": []string{"AXIsProcessTrusted", "CGPreflightPostEventAccess", "CGPreflightScreenCaptureAccess", "CGSessionCopyCurrentDictionary"}, "coordinate_space": d.CoordinateSpace(),
		"tcc_attribution": map[string]any{"verified": false, "responsible_process": nil, "note": "macOS exposes no supported TCC attribution lookup. process_chain contains launch context, not proof of the responsible identity. Use the OS consent prompt/Settings or TCC logs from this exact MCP launch. Terminal permission alone does not imply permission for a server launched by Codex/another IDE."},
	}
	for k, v := range identity {
		out[k] = v
	}
	// Compatibility for the existing --doctor consumers. New MCP clients use the
	// explicit fields above, which separate session, Accessibility and PostEvent.
	out["accessibility"] = state.AccessibilityPermission
	out["screen_capture"] = state.ScreenCapturePermission
	out["unlocked_console"] = state.ConsoleReason() == nil
	out["session_available"] = state.SessionAvailable
	return out, ctx.Err()
}
func Permissions(request bool) (map[string]any, error) {
	d, _ := New()
	if request {
		C.cv_request_screen_capture()
	}
	report, e := d.MacOSPermissionDiagnostics(context.Background(), request)
	if e != nil {
		return nil, e
	}
	return report.(map[string]any), nil
}

func nativeStateReason(s PermissionState) string {
	c := C.cv_permission_state{accessibility_permission: C.int(boolInt(s.AccessibilityPermission)), post_event_permission: C.int(boolInt(s.PostEventPermission)), screen_capture_permission: C.int(boolInt(s.ScreenCapturePermission)), session_available: C.int(boolInt(s.SessionAvailable)), on_console: C.int(triInt(s.OnConsole)), login_done: C.int(triInt(s.LoginDone)), screen_locked: C.int(triInt(s.ScreenLocked)), effective_uid: C.uint(s.EffectiveUID)}
	if s.SessionUID != nil {
		c.session_uid_known = 1
		c.session_uid = C.uint(*s.SessionUID)
	}
	reason := C.cv_permission_failure(&c, 2)
	if reason == nil {
		return ""
	}
	code, _, _ := strings.Cut(C.GoString(reason), ":")
	return code
}
func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
func triInt(v *bool) int {
	if v == nil {
		return -1
	}
	return boolInt(*v)
}
