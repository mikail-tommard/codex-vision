package macos

// PermissionState contains OS observations, not a grant made by an MCP client.
// Nil session flags mean unavailable/invalid data, never a fabricated false.
type PermissionState struct {
	AccessibilityPermission bool    `json:"accessibility_permission"`
	PostEventPermission     bool    `json:"post_event_permission"`
	ScreenCapturePermission bool    `json:"screen_capture_permission"`
	SessionAvailable        bool    `json:"session_available"`
	OnConsole               *bool   `json:"on_console"`
	LoginDone               *bool   `json:"login_done"`
	ScreenLocked            *bool   `json:"screen_locked"`
	LockIndicatorPresent    bool    `json:"lock_indicator_present"`
	SessionUID              *uint32 `json:"session_uid"`
	EffectiveUID            uint32  `json:"effective_uid"`
}

type PermissionReason struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (r PermissionReason) Error() string { return r.Code + ": " + r.Message }
func (s PermissionState) ConsoleReason() *PermissionReason {
	reason := func(code, message string) *PermissionReason { return &PermissionReason{code, message} }
	if !s.SessionAvailable {
		return reason("console_session_unavailable", "CGSessionCopyCurrentDictionary returned NULL; the MCP process has no Quartz GUI session or WindowServer access")
	}
	if s.OnConsole == nil {
		return reason("console_state_unknown", "on-console flag missing or invalid")
	}
	if !*s.OnConsole {
		return reason("console_not_active", "MCP process is not attached to the active console session")
	}
	if s.LoginDone == nil {
		return reason("console_login_state_unknown", "login-complete flag missing or invalid")
	}
	if !*s.LoginDone {
		return reason("console_login_incomplete", "GUI login has not completed")
	}
	if s.SessionUID == nil {
		return reason("console_user_unknown", "GUI session user ID unavailable")
	}
	if *s.SessionUID != s.EffectiveUID {
		return reason("console_user_mismatch", "GUI session belongs to another effective user")
	}
	if s.ScreenLocked == nil {
		return reason("console_lock_state_unknown", "lock indicator has an invalid type")
	}
	if *s.ScreenLocked {
		return reason("console_locked", "active GUI session is locked")
	}
	return nil
}
func (s PermissionState) BlockingReasons() []PermissionReason {
	reasons := []PermissionReason{}
	if r := s.ConsoleReason(); r != nil {
		reasons = append(reasons, *r)
	}
	if !s.AccessibilityPermission {
		reasons = append(reasons, PermissionReason{"accessibility_permission_denied", "AXIsProcessTrusted returned false for this MCP process; request access from macos_permission_diagnostics and authorize the identity shown by macOS"})
	}
	if !s.PostEventPermission {
		reasons = append(reasons, PermissionReason{"event_post_permission_denied", "CGPreflightPostEventAccess returned false; this process is not authorized to synthesize input events"})
	}
	return reasons
}
func (s PermissionState) AccessibilityError() error {
	if r := s.ConsoleReason(); r != nil {
		return *r
	}
	if !s.AccessibilityPermission {
		return PermissionReason{"accessibility_permission_denied", "AXIsProcessTrusted returned false for this MCP process; request consent from macos_permission_diagnostics"}
	}
	return nil
}
func (s PermissionState) InputError() error {
	if rs := s.BlockingReasons(); len(rs) > 0 {
		return rs[0]
	}
	return nil
}
func (s PermissionState) InputAvailable() bool { return s.InputError() == nil }
