package platform

import "context"

// UIElement contains structural metadata only; values/passwords are never read.
type UIElement struct {
	Identity    string `json:"identity,omitempty"`
	ID          string `json:"element_id,omitempty"`
	Role        string `json:"role"`
	Label       string `json:"label"`
	Placeholder string `json:"placeholder,omitempty"`
	Bounds      Rect   `json:"bounds"`
	Enabled     bool   `json:"enabled"`
	Editable    bool   `json:"editable"`
	Secure      bool   `json:"secure"`
	Path        []int  `json:"path,omitempty"`
}
type UISnapshot struct {
	Context   *UIContext  `json:"context,omitempty"`
	Elements  []UIElement `json:"elements"`
	Truncated bool        `json:"truncated"`
	Visited   int         `json:"visited"`
}
type UIReader interface {
	ReadUI(context.Context, Window, int) (UISnapshot, error)
}

// Controls are read-only and do not replace native field reference maps.
type UIControl struct {
	Identity string `json:"identity,omitempty"`
	Role     string `json:"role"`
	Label    string `json:"label"`
	Bounds   Rect   `json:"bounds"`
	Enabled  *bool  `json:"enabled"`
}
type ControlSnapshot struct {
	Context   *UIContext  `json:"context,omitempty"`
	Controls  []UIControl `json:"controls"`
	Truncated bool        `json:"truncated"`
	Visited   int         `json:"visited"`
}
type ControlReader interface {
	ReadControls(context.Context, Window, int) (ControlSnapshot, error)
}

// UIContext identifies the native window/document generation, never its contents.
// IDs are opaque process-local evidence, not persistent selectors or permissions.
type UIContext struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
}

type UIContextReader interface {
	ReadUIContext(context.Context, Window) (UIContext, error)
}

func MatchUIContext(expected, current *UIContext) bool {
	return expected == nil || (current != nil && expected.ID != "" && expected.Kind != "" && *expected == *current)
}

// MatchControl retains structural checks even when native identity is known.
// Losing native identity after discovery is a stale target, never fallback.
func MatchControl(expected, current UIControl) bool {
	return expected.Role == current.Role && expected.Label == current.Label && expected.Bounds == current.Bounds &&
		current.Enabled != nil && *current.Enabled && (expected.Identity == "" || expected.Identity == current.Identity)
}
