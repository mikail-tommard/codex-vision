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
	Elements  []UIElement `json:"elements"`
	Truncated bool        `json:"truncated"`
	Visited   int         `json:"visited"`
}
type UIReader interface {
	ReadUI(context.Context, Window, int) (UISnapshot, error)
}

// Controls are read-only and do not replace native field reference maps.
type UIControl struct {
	Role    string `json:"role"`
	Label   string `json:"label"`
	Bounds  Rect   `json:"bounds"`
	Enabled *bool  `json:"enabled"`
}
type ControlSnapshot struct {
	Controls  []UIControl `json:"controls"`
	Truncated bool        `json:"truncated"`
	Visited   int         `json:"visited"`
}
type ControlReader interface {
	ReadControls(context.Context, Window, int) (ControlSnapshot, error)
}
