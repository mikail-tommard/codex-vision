package mcpserver

import (
	"fmt"
	"time"
	"unicode"
	"unicode/utf8"

	"example.com/codex-vision/internal/platform"
)

// ControlSelector uses exact observed metadata. UI text is data, never policy.
type ControlSelector struct {
	Label string `json:"label" jsonschema:"Exact observed control label; no fuzzy matching"`
	Role  string `json:"role,omitempty" jsonschema:"Optional exact native role to disambiguate"`
}

func (s ControlSelector) validate() error {
	if s.Label == "" || len(s.Label) > 512 || len(s.Role) > 128 ||
		!utf8.ValidString(s.Label) || !utf8.ValidString(s.Role) ||
		unicodeControl(s.Label) || unicodeControl(s.Role) {
		return fmt.Errorf("target requires an exact label up to 512 bytes and optional role up to 128 bytes without control characters")
	}
	return nil
}

func unicodeControl(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

func selectControl(raw platform.ControlSnapshot, s ControlSelector, w platform.Window) (platform.UIControl, int, error) {
	if raw.Truncated {
		return platform.UIControl{}, -1, fmt.Errorf("incomplete control tree cannot resolve target")
	}
	index, count := -1, 0
	for i, c := range raw.Controls {
		if c.Label == s.Label && (s.Role == "" || c.Role == s.Role) {
			index, count = i, count+1
		}
	}
	if count > 1 {
		return platform.UIControl{}, -1, fmt.Errorf("ambiguous target: selector matched %d controls", count)
	}
	if count == 0 {
		return platform.UIControl{}, -1, fmt.Errorf("stale target: selector not found")
	}
	c := raw.Controls[index]
	if c.Enabled == nil || !*c.Enabled || !c.Bounds.Valid() || !w.Bounds.Includes(c.Bounds) {
		return platform.UIControl{}, -1, fmt.Errorf("target is disabled, unavailable or outside pinned window")
	}
	return c, index, nil
}

func (v *Service) selectControl(raw platform.ControlSnapshot, s ControlSelector, w platform.Window) (platform.UIControl, int, error) {
	defer v.performance.record("target_lookup", time.Now())
	if v.memory != nil {
		// Receipts supply evidence about previous verified use, never a cached
		// uniqueness decision or coordinates. Always run the complete scan below.
		_, err := v.memory.Known(v.locatorKey(w, raw, s))
		v.memoryFailure(err)
	}
	return selectControl(raw, s, w)
}

func sameControl(a, b platform.UIControl) bool {
	return platform.MatchControl(a, b)
}

func validateStepTargets(steps []DesktopStep) error {
	for i, s := range steps {
		if s.Target == nil && s.TargetID == "" {
			continue
		}
		if (s.Action != "click" && s.Action != "move") || s.X != 0 || s.Y != 0 || (s.Target != nil && s.TargetID != "") {
			return fmt.Errorf("step %d: choose target or target_id for click/move without x/y", i+1)
		}
		if s.Target != nil {
			if err := s.Target.validate(); err != nil {
				return err
			}
		}
	}
	return nil
}
