//go:build darwin && cgo

package macos

/*
#include <stdlib.h>
#include "bridge.h"
*/
import "C"
import (
	"context"
	"encoding/json"
	"example.com/codex-vision/internal/platform"
	"fmt"
	"time"
	"unsafe"
)

func (d *Desktop) uiWindow(ctx context.Context, w platform.Window) (uint32, error) {
	active, err := d.Active(ctx)
	if err != nil {
		return 0, err
	}
	if active.ID != w.ID || active.Process != w.Process || active.Bounds != w.Bounds {
		return 0, fmt.Errorf("UI window is no longer foreground")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for native, e := range d.ids {
		if e.ID == w.ID && e.Process == w.Process {
			return native, nil
		}
	}
	return 0, fmt.Errorf("stale UI window")
}
func uiTimeout(ctx context.Context) int {
	timeout := 2000
	if deadline, ok := ctx.Deadline(); ok {
		timeout = min(timeout, max(1, int(time.Until(deadline)/time.Millisecond)))
	}
	return timeout
}
func (d *Desktop) ReadUI(ctx context.Context, w platform.Window, maxNodes int) (platform.UISnapshot, error) {
	var out platform.UISnapshot
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if err := d.permissionState().AccessibilityError(); err != nil {
		return out, err
	}
	if maxNodes < 1 || maxNodes > 2000 {
		return out, fmt.Errorf("invalid traversal bound")
	}
	native, err := d.uiWindow(ctx, w)
	if err != nil {
		return out, err
	}
	if err = decode(C.cv_ui_snapshot(C.uint32_t(native), C.int(maxNodes), C.int(uiTimeout(ctx))), &out); err != nil {
		return out, err
	}
	return out, ctx.Err()
}
func (d *Desktop) fillUI(ctx context.Context, a platform.Action) error {
	if a.UIExpected == nil {
		return fmt.Errorf("missing UI target")
	}
	w, err := d.Active(ctx)
	if err != nil {
		return err
	}
	if w.ID != a.WindowID {
		return fmt.Errorf("UI target not foreground")
	}
	native, err := d.uiWindow(ctx, w)
	if err != nil {
		return err
	}
	expected, err := json.Marshal(struct {
		*platform.UIElement
		Context *platform.UIContext `json:"context,omitempty"`
	}{a.UIExpected, a.ContextExpected})
	if err != nil {
		return err
	}
	e := C.CString(string(expected))
	defer C.free(unsafe.Pointer(e))
	text := C.CString(a.Text)
	defer C.free(unsafe.Pointer(text))
	if err = ctx.Err(); err != nil {
		return err
	}
	flag := C.cv_ui_cancel_new()
	if flag == nil {
		return fmt.Errorf("native cancellation allocation failed")
	}
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { C.cv_ui_cancel(flag); close(done) })
	defer func() {
		if !stop() {
			<-done
		}
		C.free(flag)
	}()
	if err = ctx.Err(); err != nil {
		return err
	}
	var message *C.char
	ok := C.cv_ui_fill(C.uint32_t(native), e, text, C.int(uiTimeout(ctx)), flag, &message)
	err = nativeError(ok, message)
	if err != nil {
		return err
	}
	return ctx.Err()
}

func (d *Desktop) ReadControls(ctx context.Context, w platform.Window, maxNodes int) (platform.ControlSnapshot, error) {
	var out platform.ControlSnapshot
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if err := d.permissionState().AccessibilityError(); err != nil {
		return out, err
	}
	if maxNodes < 1 || maxNodes > 2000 {
		return out, fmt.Errorf("invalid traversal bound")
	}
	native, err := d.uiWindow(ctx, w)
	if err != nil {
		return out, err
	}
	if err = decode(C.cv_ui_controls(C.uint32_t(native), C.int(maxNodes), C.int(uiTimeout(ctx))), &out); err != nil {
		return out, err
	}
	return out, ctx.Err()
}

func (d *Desktop) ReadUIContext(ctx context.Context, w platform.Window) (platform.UIContext, error) {
	var out platform.UIContext
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if err := d.permissionState().AccessibilityError(); err != nil {
		return out, err
	}
	native, err := d.uiWindow(ctx, w)
	if err != nil {
		return out, err
	}
	if err := decode(C.cv_ui_context(C.uint32_t(native)), &out); err != nil {
		return out, err
	}
	return out, ctx.Err()
}

func (d *Desktop) validateUITarget(ctx context.Context, a platform.Action, w platform.Window) error {
	control := a.ControlExpected
	if control != nil && control.Identity == "" {
		control = nil
	}
	if a.ContextExpected == nil && control == nil {
		return nil
	}
	native, err := d.uiWindow(ctx, w)
	if err != nil {
		return err
	}
	proof, err := json.Marshal(struct {
		Context *platform.UIContext `json:"context,omitempty"`
		Control *platform.UIControl `json:"control,omitempty"`
	}{a.ContextExpected, control})
	if err != nil {
		return err
	}
	payload := C.CString(string(proof))
	defer C.free(unsafe.Pointer(payload))
	if err := ctx.Err(); err != nil {
		return err
	}
	var message *C.char
	ok := C.cv_ui_validate(C.uint32_t(native), payload, C.double(a.X), C.double(a.Y), C.int(uiTimeout(ctx)), &message)
	if err := nativeError(ok, message); err != nil {
		return err
	}
	return ctx.Err()
}

func (d *Desktop) validateActionContext(ctx context.Context, a platform.Action) error {
	if a.ContextExpected == nil {
		return nil
	}
	w, err := d.Active(ctx)
	if err != nil {
		return err
	}
	if a.WindowID != "" && a.WindowID != w.ID {
		return fmt.Errorf("native UI context window is no longer foreground")
	}
	return d.validateUITarget(ctx, a, w)
}
