package mcpserver

// A discovery profile, not an input grant. Primitive actions still pass their
// existing allowlists, window checks, budgets and local approval policy.
func compactTool(name string) bool {
	switch name {
	case "desktop_observe", "desktop_act", "window_list", "window_focus", "window_get_active",
		"app_launch", "observation_control", "session_status", "session_stop",
		"ui_fill_batch", "macos_permission_diagnostics", "screen_region", "advisor":
		return true
	}
	return false
}
