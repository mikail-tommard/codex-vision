package platform

// Capabilities describe implemented backend contracts, not current OS consent
// or local policy. Unsupported optional interfaces are never simulated.
func Capabilities(d Desktop) map[string]any {
	_, fields := d.(UIReader)
	_, controls := d.(ControlReader)
	_, cursor := d.(CursorReporter)
	_, approval := d.(LocalApprover)
	_, context := d.(UIContextReader)
	inputs := make(map[string]bool)
	for _, name := range []string{"window_focus", "app_launch", "mouse_move", "mouse_click", "mouse_drag", "mouse_scroll", "keyboard_type", "keyboard_press", "keyboard_hotkey", "ui_fill"} {
		supported := Supports(d, name)
		if name == "ui_fill" && !fields {
			supported = false
		}
		inputs[name] = supported
	}
	return map[string]any{"backend": d.Name(), "capture": true, "controls": controls, "fields": fields, "ui_context": context, "cursor_position": cursor, "local_review": approval, "input": inputs, "native_ui_events": false, "scope": "implemented interfaces; OS permissions, session and policy still apply"}
}
