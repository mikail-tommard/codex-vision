package macos

import (
	"errors"
	"testing"
)

func flag(v bool) *bool { return &v }
func allowedState() PermissionState {
	uid := uint32(501)
	return PermissionState{AccessibilityPermission: true, PostEventPermission: true, ScreenCapturePermission: true, SessionAvailable: true, OnConsole: flag(true), LoginDone: flag(true), ScreenLocked: flag(false), SessionUID: &uid, EffectiveUID: 501}
}

type permissionCase struct {
	name   string
	mutate func(*PermissionState)
	code   string
}

func permissionCases() []permissionCase {
	return []permissionCase{
		{"granted", func(*PermissionState) {}, ""},
		{"no_ax", func(s *PermissionState) { s.AccessibilityPermission = false }, "accessibility_permission_denied"},
		{"no_post_event", func(s *PermissionState) { s.PostEventPermission = false }, "event_post_permission_denied"},
		{"no_gui", func(s *PermissionState) { s.SessionAvailable = false }, "console_session_unavailable"},
		{"no_console_flag", func(s *PermissionState) { s.OnConsole = nil }, "console_state_unknown"},
		{"off_console", func(s *PermissionState) { s.OnConsole = flag(false) }, "console_not_active"},
		{"unknown_login", func(s *PermissionState) { s.LoginDone = nil }, "console_login_state_unknown"},
		{"login_incomplete", func(s *PermissionState) { s.LoginDone = flag(false) }, "console_login_incomplete"},
		{"unknown_user", func(s *PermissionState) { s.SessionUID = nil }, "console_user_unknown"},
		{"different_user", func(s *PermissionState) { s.EffectiveUID = 502 }, "console_user_mismatch"},
		{"unknown_lock", func(s *PermissionState) { s.ScreenLocked = nil }, "console_lock_state_unknown"},
		{"locked_ax_granted", func(s *PermissionState) { s.ScreenLocked = flag(true) }, "console_locked"},
		{"multiple_blockers", func(s *PermissionState) {
			s.ScreenLocked = flag(true)
			s.AccessibilityPermission = false
			s.PostEventPermission = false
		}, "console_locked"},
		{"screen_recording_not_needed_for_keyboard", func(s *PermissionState) { s.ScreenCapturePermission = false }, ""},
	}
}
func TestPermissionReasons(t *testing.T) {
	for _, tc := range permissionCases() {
		t.Run(tc.name, func(t *testing.T) {
			s := allowedState()
			tc.mutate(&s)
			e := s.InputError()
			if tc.code == "" {
				if e != nil || !s.InputAvailable() {
					t.Fatal(e)
				}
				return
			}
			var reason PermissionReason
			if !errors.As(e, &reason) || reason.Code != tc.code || s.InputAvailable() {
				t.Fatal(e, tc.code)
			}
			if tc.name == "multiple_blockers" && len(s.BlockingReasons()) != 3 {
				t.Fatal(s.BlockingReasons())
			}
		})
	}
}
