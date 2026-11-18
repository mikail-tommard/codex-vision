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
	out := map[string]any{"backend": d.Name(), "capture": true, "controls": controls, "fields": fields, "ui_context": context, "cursor_position": cursor, "local_review": approval, "input": inputs, "native_ui_events": false, "scope": "implemented interfaces; OS permissions, session and policy still apply"}
	if _, ok := d.(ConsentedScreenshot); ok {
		out["capture_mode"] = "consented_portal_single_shot"
		out["coordinate_mapping_available"] = false
		out["continuous_observation"] = false
		out["window_discovery"] = false
		out["monitor_geometry"] = false
	}
	if _, ok := d.(ScreenCaster); ok {
		out["portal_screencast"] = true
		out["screencast_requires"] = "ScreenCast v6, stable PipeWire serial, installed GStreamer plugins and system consent; no desktop mapping/input"
	}
	return out
}
