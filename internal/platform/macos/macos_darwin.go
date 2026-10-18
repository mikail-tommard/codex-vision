//go:build darwin && cgo

// Package macos bridges native Quartz/AppKit APIs. Desktop coordinates are
// Quartz logical points; every captured display is normalized to that scale.
package macos

/*
#cgo CFLAGS: -x objective-c -fobjc-arc -Wno-deprecated-declarations
#cgo LDFLAGS: -framework Foundation -framework AppKit -framework ApplicationServices -framework CoreGraphics -framework ImageIO -framework ScreenCaptureKit -framework Security
#include <stdlib.h>
#include "bridge.h"
*/
import "C"
import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"example.com/codex-vision/internal/platform"
	"example.com/codex-vision/internal/vision"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"strconv"
	"sync"
	"time"
	"unsafe"
)

type nativeWindow struct {
	OnScreen *bool         `json:"on_screen,omitempty"`
	NativeID uint32        `json:"native_id"`
	PID      int           `json:"pid"`
	Title    string        `json:"title"`
	Process  string        `json:"process"`
	Bounds   platform.Rect `json:"bounds"`
}
type entry struct {
	ID      string
	PID     int
	Process string
}
type Desktop struct {
	mu                sync.Mutex
	ids               map[uint32]entry
	held              []releaseEvent
	verifyProcess     func(context.Context, int, string, string) (bool, error)
	readWindows       func(context.Context, bool) ([]nativeWindow, error)
	readPermissions   func() PermissionState
	requestPermission func()
}

func New() (*Desktop, error)  { return &Desktop{ids: map[uint32]entry{}}, nil }
func (*Desktop) Name() string { return "macos-quartz" }
func (*Desktop) CoordinateSpace() string {
	return "quartz logical points; top-left main display origin; captures normalized to 1 pixel per point"
}
func decode(p *C.char, out any) error {
	if p == nil {
		return fmt.Errorf("native macOS allocation failed")
	}
	defer C.free(unsafe.Pointer(p))
	b := []byte(C.GoString(p))
	var problem struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(b, &problem) == nil && problem.Error != "" {
		return fmt.Errorf("%s", problem.Error)
	}
	return json.Unmarshal(b, out)
}
func (d *Desktop) Monitors(ctx context.Context) ([]platform.Monitor, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	var ms []platform.Monitor
	e := decode(C.cv_monitors(), &ms)
	return ms, e
}
func (d *Desktop) windows(ctx context.Context, active bool) ([]platform.Window, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	mode := C.int(0)
	if active {
		mode = 1
	}
	var ws []nativeWindow
	if d.readWindows != nil {
		var err error
		ws, err = d.readWindows(ctx, active)
		if err != nil {
			return nil, err
		}
	} else if e := decode(C.cv_windows(mode), &ws); e != nil {
		return nil, e
	}
	out := make([]platform.Window, 0, len(ws))
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, w := range ws {
		token, ok := d.ids[w.NativeID]
		if !ok || token.PID != w.PID || token.Process != w.Process {
			if len(d.ids) >= 4096 {
				return nil, fmt.Errorf("window token limit reached; restart locally")
			}
			var b [16]byte
			if _, e := rand.Read(b[:]); e != nil {
				return nil, e
			}
			token = entry{hex.EncodeToString(b[:]), w.PID, w.Process}
			d.ids[w.NativeID] = token
		}
		out = append(out, platform.Window{ID: token.ID, Title: w.Title, Process: w.Process, Bounds: w.Bounds, OnScreen: w.OnScreen})
	}
	return out, ctx.Err()
}
func (d *Desktop) Windows(ctx context.Context) ([]platform.Window, error) {
	return d.windows(ctx, false)
}
func (d *Desktop) Active(ctx context.Context) (platform.Window, error) {
	ws, e := d.windows(ctx, true)
	if e != nil {
		return platform.Window{}, e
	}
	if len(ws) == 0 {
		return platform.Window{}, fmt.Errorf("no visible foreground window")
	}
	if len(ws) != 1 {
		return platform.Window{}, fmt.Errorf("focused Accessibility window matches %d Quartz windows; refusing ambiguous foreground", len(ws))
	}
	return ws[0], nil
}
func (d *Desktop) Capture(ctx context.Context, r platform.Rect) (image.Image, error) {
	if !r.Valid() {
		return nil, fmt.Errorf("invalid capture bounds")
	}
	if e := vision.Validate(image.Rect(0, 0, r.Width, r.Height)); e != nil {
		return nil, e
	}
	ms, e := d.Monitors(ctx)
	if e != nil {
		return nil, e
	}
	bounds, e := platform.Bounds(ms)
	if e != nil || !bounds.Includes(r) {
		return nil, fmt.Errorf("region outside virtual desktop")
	}
	out := image.NewRGBA(image.Rect(0, 0, r.Width, r.Height))
	draw.Draw(out, out.Bounds(), image.Black, image.Point{}, draw.Src)
	for _, m := range ms {
		intersection := image.Rect(r.X, r.Y, r.X+r.Width, r.Y+r.Height).Intersect(image.Rect(m.Bounds.X, m.Bounds.Y, m.Bounds.X+m.Bounds.Width, m.Bounds.Y+m.Bounds.Height))
		if intersection.Empty() {
			continue
		}
		if e = ctx.Err(); e != nil {
			return nil, e
		}
		id, e := strconv.ParseUint(m.ID, 10, 32)
		if e != nil {
			return nil, e
		}
		timeout := 5000
		if deadline, ok := ctx.Deadline(); ok {
			timeout = max(1, int(time.Until(deadline)/time.Millisecond))
		}
		var data unsafe.Pointer
		var n C.size_t
		var message *C.char
		ok := C.cv_capture(C.uint32_t(id), C.int(timeout), &data, &n, &message)
		if message != nil {
			reason := C.GoString(message)
			C.free(unsafe.Pointer(message))
			return nil, fmt.Errorf("%s", reason)
		}
		if ok == 0 || data == nil {
			return nil, fmt.Errorf("native capture failed")
		}
		encoded := C.GoBytes(data, C.int(n))
		C.free(data)
		im, e := png.Decode(bytes.NewReader(encoded))
		if e != nil {
			return nil, e
		}
		if e = vision.Composite(ctx, out, image.Rect(r.X, r.Y, r.X+r.Width, r.Y+r.Height), image.Rect(m.Bounds.X, m.Bounds.Y, m.Bounds.X+m.Bounds.Width, m.Bounds.Y+m.Bounds.Height), im); e != nil {
			return nil, e
		}
	}
	return out, ctx.Err()
}
func (d *Desktop) Execute(ctx context.Context, a platform.Action) (err error) {
	if e := ctx.Err(); e != nil {
		return e
	}
	state := d.permissionState()
	permissionError := state.InputError()
	if a.Tool == "window_focus" || a.Tool == "app_launch" || a.Tool == "ui_fill" {
		permissionError = state.AccessibilityError()
	}
	if permissionError != nil {
		return permissionError
	}
	if e := d.release(); e != nil {
		return e
	}
	defer func() {
		if e := d.release(); err == nil {
			err = e
		}
	}()
	if e := ctx.Err(); e != nil {
		return e
	}
	var message *C.char
	var ok C.int
	switch a.Tool {
	case "ui_fill":
		return d.fillUI(ctx, a)
	case "app_launch":
		return d.launch(ctx, a)
	case "mouse_click":
		return d.click(ctx, a)
	case "mouse_drag":
		return d.drag(ctx, a)
	case "keyboard_type":
		return d.typeText(ctx, a.Text)
	case "keyboard_press", "keyboard_hotkey":
		return d.keys(ctx, a.Keys)
	case "mouse_move":
		_, err := d.movePointer(ctx, a)
		return err
	case "mouse_scroll":
		if a.Delta%120 != 0 {
			return fmt.Errorf("macOS line scroll requires delta to be a multiple of 120")
		}
		h := C.int(0)
		if a.Horizontal {
			h = 1
		}
		target, err := d.Active(ctx)
		if err != nil {
			return err
		}
		if a.WindowID != "" && target.ID != a.WindowID {
			return fmt.Errorf("target window is not foreground; stop scroll")
		}
		var nativeID uint32
		var pid int
		d.mu.Lock()
		for id, token := range d.ids {
			if token.ID == target.ID && token.Process == target.Process {
				nativeID, pid = id, token.PID
				break
			}
		}
		d.mu.Unlock()
		if nativeID == 0 || pid <= 0 {
			return fmt.Errorf("scroll window identity is stale; refresh window_list")
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		ok = C.cv_event(2, 0, 0, C.int(a.Delta/120), h, C.uint32_t(nativeID), C.int(pid), &message)
	case "window_focus":
		ws, e := d.Windows(ctx)
		if e != nil {
			return e
		}
		found := false
		for _, w := range ws {
			if w.ID == a.WindowID {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("stale window ID")
		}
		d.mu.Lock()
		var id uint32
		for n, t := range d.ids {
			if t.ID == a.WindowID {
				id = n
				break
			}
		}
		d.mu.Unlock()
		if e = ctx.Err(); e != nil {
			return e
		}
		ok = C.cv_focus(C.uint32_t(id), &message)
	default:
		return fmt.Errorf("macOS action requires unavailable local approval")
	}
	if message != nil {
		reason := C.GoString(message)
		C.free(unsafe.Pointer(message))
		return fmt.Errorf("%s", reason)
	}
	if ok == 0 {
		return fmt.Errorf("macOS rejected action")
	}
	if a.Tool == "window_focus" {
		// AppKit activation is asynchronous. Wait for the exact window rather
		// than treating an accepted activation request as confirmed focus.
		waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		ticker := time.NewTicker(25 * time.Millisecond)
		defer ticker.Stop()
		var observed string
		for {
			active, e := d.Active(waitCtx)
			if e != nil {
				observed = e.Error()
			} else {
				observed = fmt.Sprintf("window_id=%s process=%q bounds=%+v", active.ID, active.Process, active.Bounds)
			}
			if e == nil && active.ID == a.WindowID {
				break
			}
			select {
			case <-waitCtx.Done():
				return fmt.Errorf("macOS did not confirm requested foreground window %s; observed %s; stop input: %w", a.WindowID, observed, waitCtx.Err())
			case <-ticker.C:
			}
		}
	}
	return ctx.Err()
}
func (d *Desktop) Release() error { return d.release() }
