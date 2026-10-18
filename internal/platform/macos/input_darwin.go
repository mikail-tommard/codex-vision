//go:build darwin && cgo

package macos

/*
#include <stdlib.h>
#include "bridge.h"
*/
import "C"
import (
	"bytes"
	"context"
	"example.com/codex-vision/internal/platform"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
	"unicode/utf16"
	"unsafe"
)

type releaseEvent struct {
	kind         int
	button       int
	x, y         int
	count        int
	code         uint16
	flags        uint64
	releaseFlags uint64
	units        []uint16
}

func (*Desktop) Supports(tool string) bool {
	switch tool {
	case "app_launch", "mouse_move", "mouse_scroll", "window_focus", "mouse_click", "mouse_drag", "keyboard_type", "keyboard_press", "keyboard_hotkey", "ui_fill":
		return true
	}
	return false
}
func (d *Desktop) Approve(ctx context.Context, a platform.Action, w platform.Window) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	state := d.permissionState()
	permissionError := state.InputError()
	if a.Tool == "app_launch" || a.Tool == "ui_fill" {
		permissionError = state.AccessibilityError()
	}
	if permissionError != nil {
		return permissionError
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/osascript", "-l", "JavaScript", "-")
	cmd.Stdin = strings.NewReader(reviewScript(a, w))
	cmd.Stderr = io.Discard
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.WaitDelay = 250 * time.Millisecond
	if e := cmd.Run(); e != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("local approval unavailable or cancelled")
	}
	if string(bytes.TrimSpace(out.Bytes())) != "ALLOW_ONCE" {
		return fmt.Errorf("local action approval declined or timed out")
	}
	return ctx.Err()
}
func nativeError(ok C.int, message *C.char) error {
	if message != nil {
		reason := C.GoString(message)
		C.free(unsafe.Pointer(message))
		return fmt.Errorf("%s", reason)
	}
	if ok == 0 {
		return fmt.Errorf("native input rejected")
	}
	return nil
}
func (d *Desktop) post(ctx context.Context, e releaseEvent, down bool) error {
	if down {
		if err := ctx.Err(); err != nil {
			return err
		}
		d.mu.Lock()
		d.held = append(d.held, e)
		d.mu.Unlock()
	}
	var msg *C.char
	var ok C.int
	v := C.int(0)
	if down {
		v = 1
	}
	switch e.kind {
	case 1:
		ok = C.cv_button(C.int(e.button), v, C.double(e.x), C.double(e.y), C.int(e.count), &msg)
	case 2:
		ok = C.cv_key(C.uint16_t(e.code), v, C.uint64_t(e.flags), &msg)
	case 3:
		ok = C.cv_unicode((*C.uint16_t)(unsafe.Pointer(&e.units[0])), C.int(len(e.units)), v, &msg)
	}
	return nativeError(ok, msg)
}
func (d *Desktop) release() error {
	d.mu.Lock()
	events := d.held
	d.held = nil
	d.mu.Unlock()
	var first error
	for i := len(events) - 1; i >= 0; i-- {
		e := events[i]
		e.flags = e.releaseFlags
		if err := d.post(context.Background(), e, false); err != nil {
			if first == nil {
				first = err
			}
			d.mu.Lock()
			d.held = append(d.held, events[i])
			d.mu.Unlock()
		}
	}
	return first
}
func (d *Desktop) click(ctx context.Context, a platform.Action) error {
	target, err := d.movePointer(ctx, platform.Action{Metrics: a.Metrics, ApproachDuration: a.ApproachDuration, X: a.X, Y: a.Y, WindowID: a.WindowID})
	if err != nil {
		return err
	}
	button := 0
	if a.Button == "right" {
		button = 1
	}
	if a.Button == "middle" {
		button = 2
	}
	for i := 1; i <= a.Count; i++ {
		if err := d.checkPointerTarget(ctx, target, a.X, a.Y); err != nil {
			return err
		}
		if e := d.post(ctx, releaseEvent{kind: 1, button: button, x: a.X, y: a.Y, count: i}, true); e != nil {
			return e
		}
		if e := d.release(); e != nil {
			return e
		}
	}
	return ctx.Err()
}
func (d *Desktop) drag(ctx context.Context, a platform.Action) error {
	target, err := d.movePointer(ctx, platform.Action{Metrics: a.Metrics, ApproachDuration: a.ApproachDuration, X: a.X, Y: a.Y, WindowID: a.WindowID})
	if err != nil {
		return err
	}
	if err = d.checkPointerTarget(ctx, target, a.X, a.Y); err != nil {
		return err
	}
	button := 0
	if a.Button == "right" {
		button = 1
	}
	if a.Button == "middle" {
		button = 2
	}
	if e := d.post(ctx, releaseEvent{kind: 1, button: button, x: a.X, y: a.Y, count: 1}, true); e != nil {
		return e
	}
	steps := max(1, int(a.Duration/(10*time.Millisecond)))
	ticker := time.NewTicker(a.Duration / time.Duration(steps))
	defer ticker.Stop()
	for i := 1; i <= steps; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
		x, y := a.X+(a.EndX-a.X)*i/steps, a.Y+(a.EndY-a.Y)*i/steps
		if err := d.checkPointerTarget(ctx, target, x, y); err != nil {
			return err
		}
		var message *C.char
		ok := C.cv_button(C.int(button), 2, C.double(x), C.double(y), 1, &message)
		if e := nativeError(ok, message); e != nil {
			return e
		}
		d.mu.Lock()
		d.held[0].x = x
		d.held[0].y = y
		d.mu.Unlock()
	}
	return d.release()
}
func (d *Desktop) typeText(ctx context.Context, text string) error {
	for _, r := range text {
		units := utf16.Encode([]rune{r})
		if e := d.post(ctx, releaseEvent{kind: 3, units: units}, true); e != nil {
			return e
		}
		if e := d.release(); e != nil {
			return e
		}
	}
	return ctx.Err()
}
func (d *Desktop) keys(ctx context.Context, keys []string) error {
	var flags uint64
	for _, k := range orderKeys(keys) {
		entry, ok := keyCodes[strings.ToUpper(k)]
		if !ok {
			return fmt.Errorf("key unsupported by macOS")
		}
		previous := flags
		flags |= entry.flag
		if e := d.post(ctx, releaseEvent{kind: 2, code: entry.code, flags: flags, releaseFlags: previous}, true); e != nil {
			return e
		}
	}
	return d.release()
}
