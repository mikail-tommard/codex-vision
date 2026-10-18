// Package platform defines physical desktop coordinates and OS boundary contracts.
package platform

import (
	"context"
	"fmt"
	"image"
	"time"
)

type Rect struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

func (r Rect) Valid() bool {
	return r.Width > 0 && r.Height > 0 && r.Width <= 32768 && r.Height <= 32768 && r.X >= -65536 && r.Y >= -65536 && r.X <= 65536 && r.Y <= 65536
}
func (r Rect) Contains(x, y int) bool {
	return r.Valid() && x >= r.X && y >= r.Y && x-r.X < r.Width && y-r.Y < r.Height
}
func (r Rect) Includes(s Rect) bool {
	return s.Valid() && r.Contains(s.X, s.Y) && r.Contains(s.X+s.Width-1, s.Y+s.Height-1)
}
func Bounds(ms []Monitor) (Rect, error) {
	if len(ms) == 0 {
		return Rect{}, fmt.Errorf("no interactive monitors available")
	}
	x, y, xx, yy := ms[0].Bounds.X, ms[0].Bounds.Y, ms[0].Bounds.X, ms[0].Bounds.Y
	for _, m := range ms {
		r := m.Bounds
		if !r.Valid() {
			return Rect{}, fmt.Errorf("invalid monitor bounds")
		}
		x = min(x, r.X)
		y = min(y, r.Y)
		xx = max(xx, r.X+r.Width)
		yy = max(yy, r.Y+r.Height)
	}
	r := Rect{x, y, xx - x, yy - y}
	if !r.Valid() {
		return Rect{}, fmt.Errorf("virtual desktop exceeds supported bounds")
	}
	return r, nil
}
func Normalize(x, y int, r Rect) (int32, int32, error) {
	if !r.Contains(x, y) {
		return 0, 0, fmt.Errorf("coordinates outside virtual desktop")
	}
	return int32(int64(x-r.X) * 65535 / int64(max(1, r.Width-1))), int32(int64(y-r.Y) * 65535 / int64(max(1, r.Height-1))), nil
}

type Point struct {
	X int `json:"x"`
	Y int `json:"y"`
}

// CursorReporter reads the real desktop pointer; capture may draw a marker.
type CursorReporter interface {
	CursorPosition(context.Context) (Point, error)
}

type Monitor struct {
	ID          string `json:"id"`
	Bounds      Rect   `json:"bounds"`
	Primary     bool   `json:"primary"`
	DPI         int    `json:"dpi,omitempty"`
	PixelWidth  int    `json:"pixel_width,omitempty"`
	PixelHeight int    `json:"pixel_height,omitempty"`
}
type Window struct {
	ProcessAllowed *bool  `json:"process_allowed,omitempty"`
	AllowedProcess string `json:"allowed_process,omitempty"`
	IdentityError  string `json:"identity_error,omitempty"`
	// nil means the backend cannot report current-screen membership.
	// false includes other Spaces, minimized and hidden windows; true does
	// not guarantee pixels are unobscured by another window.
	OnScreen *bool  `json:"on_screen,omitempty"`
	ID       string `json:"id"`
	Title    string `json:"title"`
	Process  string `json:"process,omitempty"`
	Bounds   Rect   `json:"bounds"`
}
type InputMetrics struct {
	PointerTime     time.Duration
	PointerMeasured bool
}

type Action struct {
	Metrics          *InputMetrics `json:"-"`
	Motion           string        `json:"motion,omitempty"`
	ApproachDuration time.Duration `json:"-"`
	UIExpected       *UIElement    `json:"-"`
	Process          string        `json:"process,omitempty"`
	URL              string        `json:"url,omitempty"`
	Tool             string        `json:"tool"`
	X                int           `json:"x,omitempty"`
	Y                int           `json:"y,omitempty"`
	EndX             int           `json:"end_x,omitempty"`
	EndY             int           `json:"end_y,omitempty"`
	Button           string        `json:"button,omitempty"`
	Count            int           `json:"count,omitempty"`
	Delta            int           `json:"delta,omitempty"`
	Horizontal       bool          `json:"horizontal,omitempty"`
	Text             string        `json:"-"`
	Keys             []string      `json:"keys,omitempty"`
	WindowID         string        `json:"window_id,omitempty"`
	Duration         time.Duration `json:"-"`
}
type Desktop interface {
	Name() string
	Monitors(context.Context) ([]Monitor, error)
	Capture(context.Context, Rect) (image.Image, error)
	Windows(context.Context) ([]Window, error)
	Active(context.Context) (Window, error)
	Execute(context.Context, Action) error
	Release() error
}

// CoordinateSpace lets a backend describe Retina/logical coordinate units.
func CoordinateSpace(d Desktop) string {
	if v, ok := d.(interface{ CoordinateSpace() string }); ok {
		return v.CoordinateSpace()
	}
	return "physical virtual-desktop pixels; negative origins supported"
}

// Supports reports backend-specific capability restrictions before input executes.
func Supports(d Desktop, tool string) bool {
	if v, ok := d.(interface{ Supports(string) bool }); ok {
		return v.Supports(tool)
	}
	return true
}

// LocalApprover is implemented only by a native backend with a local review UI.
// Client-provided booleans, text and elicitation are never approval tokens.
type LocalApprover interface {
	Approve(context.Context, Action, Window) error
}

func NeedsApproval(tool string) bool {
	switch tool {
	case "mouse_click", "mouse_drag", "keyboard_type", "keyboard_press", "keyboard_hotkey", "app_launch", "ui_fill":
		return true
	}
	return false
}

// MacOSPermissionDiagnoser runs in the actual MCP process, not a Terminal helper.
type MacOSPermissionDiagnoser interface {
	MacOSPermissionDiagnostics(context.Context, bool) (any, error)
}
