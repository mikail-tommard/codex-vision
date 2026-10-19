package mcpserver

import (
	"context"
	"errors"
	"strings"
)

type failureInfo struct {
	Code        string `json:"code"`
	NextAction  string `json:"next_action"`
	RetryAction bool   `json:"retry_action"` // Never authorizes replay.
}

// Classification is advisory. It never changes the existing authorization or
// retries an input; unrecognized errors remain conservative and explicit.
func classifyFailure(err error) failureInfo {
	f := failureInfo{Code: "operation_failed", NextAction: "inspect_error_without_replaying"}
	s := strings.ToLower(err.Error())
	switch {
	case errors.Is(err, context.Canceled):
		f.Code = "cancelled"
		f.NextAction = "stop"
	case errors.Is(err, context.DeadlineExceeded):
		f.Code = "timeout"
		f.NextAction = "wait_or_inspect_without_replaying"
	case strings.Contains(s, "stop latched") || strings.Contains(s, "session stopped") || strings.Contains(s, "emergency stop"):
		f.Code = "session_stopped"
		f.NextAction = "operator_restart"
	case strings.Contains(s, "permission") || strings.Contains(s, "allowlist") || strings.Contains(s, "observe mode") || strings.Contains(s, "approval_required"):
		f.Code = "access_denied"
		f.NextAction = "operator_review_permissions"
	case strings.Contains(s, "stale") || strings.Contains(s, "expired") || strings.Contains(s, "consumed"):
		f.Code = "stale_reference"
		f.NextAction = "desktop_observe"
	case strings.Contains(s, "outside") || strings.Contains(s, "gap between monitors"):
		f.Code = "coordinates_outside_target"
		f.NextAction = "inspect_bounds_and_coordinate_scale"
	case strings.Contains(s, "ambiguous") || strings.Contains(s, "cannot uniquely match window") || strings.Contains(s, "matched 0") || strings.Contains(s, "matched 2"):
		f.Code = "ambiguous_target"
		f.NextAction = "desktop_observe"
	case strings.Contains(s, "already satisfied"):
		f.Code = "expectation_already_satisfied"
		f.NextAction = "inspect_current_state_no_action_posted"
	case strings.Contains(s, "foreground") || strings.Contains(s, "window changed") || strings.Contains(s, "focused") || strings.Contains(s, "focus_recovery"):
		f.Code = "focus_unconfirmed"
		f.NextAction = "verify_focus_once_then_stop_if_unavailable"
	case strings.Contains(s, "incomplete") || strings.Contains(s, "state unknown"):
		f.Code = "ui_evidence_incomplete"
		f.NextAction = "inspect_visual_context_without_guessing"
	case strings.Contains(s, "unsupported") || strings.Contains(s, "unavailable by this backend") || strings.Contains(s, "control metadata unavailable"):
		f.Code = "unsupported_capability"
		f.NextAction = "use_supported_visual_workflow"
	case strings.Contains(s, "budget") || strings.Contains(s, "rate exceeded"):
		f.Code = "local_limit"
		f.NextAction = "respect_local_budget"
	}
	return f
}
