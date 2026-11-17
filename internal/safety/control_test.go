package safety

import (
	"context"
	"testing"

	"example.com/codex-vision/internal/platform"
	"example.com/codex-vision/internal/platform/mock"
)

type reviewControlDesktop struct {
	*mock.Desktop
	mode     string
	reviewed bool
}

func (d *reviewControlDesktop) Approve(ctx context.Context, _ platform.Action, _ platform.Window) error {
	d.reviewed = true
	return ctx.Err()
}

func (d *reviewControlDesktop) ReadControls(ctx context.Context, _ platform.Window, _ int) (platform.ControlSnapshot, error) {
	enabled := true
	c := platform.UIControl{Identity: "native-object", Label: "Save", Role: "AXButton", Bounds: platform.Rect{X: 10, Y: 10, Width: 100, Height: 30}, Enabled: &enabled}
	raw := platform.ControlSnapshot{Controls: []platform.UIControl{c}}
	if !d.reviewed {
		return raw, ctx.Err()
	}
	switch d.mode {
	case "moved":
		raw.Controls[0].Bounds.X += 20
	case "disabled":
		enabled = false
	case "relabelled":
		raw.Controls[0].Label = "Delete"
	case "duplicate":
		raw.Controls = append(raw.Controls, c)
	case "other_role":
		other := c
		other.Role = "AXMenuItem"
		raw.Controls = append(raw.Controls, other)
	case "truncated":
		raw.Truncated = true
	case "replaced":
		raw.Controls[0].Identity = "another-native-object"
	case "identity_lost":
		raw.Controls[0].Identity = ""
	}
	return raw, ctx.Err()
}

func TestSemanticControlRevalidatedAfterLocalApproval(t *testing.T) {
	for _, mode := range []string{"same", "moved", "disabled", "relabelled", "duplicate", "other_role", "truncated", "replaced", "identity_lost"} {
		t.Run(mode, func(t *testing.T) {
			cfg, base, log := setup(t)
			cfg.Allow, cfg.LocalApproval, cfg.ConfirmEachAction = []string{"mouse_click"}, true, true
			d := &reviewControlDesktop{Desktop: base, mode: mode}
			enabled := true
			target := platform.UIControl{Identity: "native-object", Label: "Save", Role: "AXButton", Bounds: platform.Rect{X: 10, Y: 10, Width: 100, Height: 30}, Enabled: &enabled}
			err := New(cfg, d, log).Execute(context.Background(), platform.Action{Tool: "mouse_click", X: 20, Y: 20, Button: "left", Count: 1, ControlExpected: &target, ControlUniqueSelector: true, ControlLabelOnly: true, ControlMaxNodes: 500})
			if (err == nil) != (mode == "same") || !d.reviewed {
				t.Fatal("post-approval control evidence ignored", mode, err)
			}
			if mode != "same" && len(base.Actions) != 0 {
				t.Fatal("changed control received click", base.Actions)
			}
		})
	}
}

type reviewContextDesktop struct {
	*reviewControlDesktop
}

func (d *reviewContextDesktop) ReadUIContext(ctx context.Context, _ platform.Window) (platform.UIContext, error) {
	id := "document-A"
	if d.reviewed && d.mode == "document_changed" {
		id = "document-B"
	}
	return platform.UIContext{ID: id, Kind: "native_window_document"}, ctx.Err()
}

func TestDocumentContextRecheckedAfterApprovalForKeyboard(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "same", true: "changed"}[changed], func(t *testing.T) {
			cfg, base, log := setup(t)
			cfg.Allow, cfg.LocalApproval, cfg.ConfirmEachAction = []string{"keyboard_press"}, true, true
			d := &reviewContextDesktop{&reviewControlDesktop{Desktop: base}}
			if changed {
				d.mode = "document_changed"
			}
			err := New(cfg, d, log).Execute(context.Background(), platform.Action{Tool: "keyboard_press", Keys: []string{"SPACE"}, ContextExpected: &platform.UIContext{ID: "document-A", Kind: "native_window_document"}})
			if (err == nil) == changed || !d.reviewed || (changed && len(base.Actions) != 0) {
				t.Fatal("same-title document changed during review", err, base.Actions)
			}
		})
	}
}
