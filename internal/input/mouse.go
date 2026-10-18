package input

import (
	"example.com/codex-vision/internal/platform"
	"fmt"
)

func Validate(a platform.Action, b platform.Rect) error {
	switch a.Tool {
	case "mouse_move", "mouse_click", "mouse_drag":
		if !b.Contains(a.X, a.Y) {
			return fmt.Errorf("coordinates outside virtual-desktop bounds")
		}
		if a.Tool == "mouse_move" && a.Duration != 0 && (a.Duration < 10_000_000 || a.Duration > 5_000_000_000) {
			return fmt.Errorf("mouse move duration must be 0 (default) or 10..5000 ms")
		}
		if a.Tool == "mouse_drag" {
			if !b.Contains(a.EndX, a.EndY) || a.Duration < 10_000_000 || a.Duration > 5_000_000_000 {
				return fmt.Errorf("invalid drag endpoint or duration (10..5000 ms)")
			}
		}
	case "mouse_scroll":
		if a.Delta == 0 || a.Delta < -1200 || a.Delta > 1200 {
			return fmt.Errorf("scroll delta must be -1200..1200 and nonzero; 120 is one wheel notch")
		}
	case "ui_fill":
		if a.WindowID == "" || a.UIExpected == nil || a.UIExpected.Secure || !a.UIExpected.Editable || !a.UIExpected.Enabled {
			return fmt.Errorf("ui_fill requires an enabled non-secure editable field from ui_snapshot")
		}
		fallthrough
	case "keyboard_type":
		return ValidateText(a.Text)
	case "keyboard_press", "keyboard_hotkey":
		return ValidateKeys(a.Keys)
	case "app_launch":
		return ValidateLaunch(a.Process, a.URL)
	case "window_focus":
		if a.WindowID == "" {
			return fmt.Errorf("window_id is required")
		}
	default:
		return fmt.Errorf("unsupported action")
	}
	if a.Tool == "mouse_click" || a.Tool == "mouse_drag" {
		if a.Button != "left" && a.Button != "right" && a.Button != "middle" {
			return fmt.Errorf("button must be left, right or middle")
		}
		if a.Tool == "mouse_click" && (a.Count < 1 || a.Count > 2) {
			return fmt.Errorf("count must be 1 or 2")
		}
	}
	return nil
}
