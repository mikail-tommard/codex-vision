//go:build darwin && cgo

package macos

/*
#include "bridge.h"
*/
import "C"
import (
	"context"
	"example.com/codex-vision/internal/input"
	"example.com/codex-vision/internal/platform"
	"fmt"
	"time"
)

func (d *Desktop) CursorPosition(ctx context.Context) (platform.Point, error) {
	if err := ctx.Err(); err != nil {
		return platform.Point{}, err
	}
	var point platform.Point
	if err := decode(C.cv_cursor(), &point); err != nil {
		return platform.Point{}, err
	}
	return point, ctx.Err()
}

func (d *Desktop) checkPointerTarget(ctx context.Context, target platform.Window, x, y int) error {
	current, err := d.Active(ctx)
	if err != nil {
		return err
	}
	if current.ID != target.ID || current.Process != target.Process || current.Bounds != target.Bounds {
		return fmt.Errorf("foreground window changed during pointer action; input stopped")
	}
	if !current.Bounds.Contains(x, y) {
		return fmt.Errorf("cursor path outside allowed foreground window")
	}
	return ctx.Err()
}

func (d *Desktop) movePointer(ctx context.Context, a platform.Action) (platform.Window, error) {
	started := time.Now()
	defer func() {
		if a.Metrics != nil {
			a.Metrics.PointerTime += time.Since(started)
			a.Metrics.PointerMeasured = true
		}
	}()
	target, err := d.Active(ctx)
	if err != nil {
		return target, err
	}
	if a.WindowID != "" && target.ID != a.WindowID {
		return target, fmt.Errorf("target window is not foreground")
	}
	if !target.Bounds.Contains(a.X, a.Y) {
		return target, fmt.Errorf("cursor target outside foreground window")
	}
	point, err := d.CursorPosition(ctx)
	if err != nil {
		return target, err
	}
	// Keep all interpolated points in the approved window even when the mouse
	// starts outside it. The first event enters at the nearest window edge.
	startX := max(target.Bounds.X, min(point.X, target.Bounds.X+target.Bounds.Width-1))
	startY := max(target.Bounds.Y, min(point.Y, target.Bounds.Y+target.Bounds.Height-1))
	duration := a.Duration
	if duration == 0 {
		duration = a.ApproachDuration
	}
	if duration == 0 {
		duration = 300 * time.Millisecond
	}
	if err = input.MovePointer(ctx, startX, startY, a.X, a.Y, duration, func(x, y int) error {
		if err := d.checkPointerTarget(ctx, target, x, y); err != nil {
			return err
		}
		var message *C.char
		ok := C.cv_event(1, C.double(x), C.double(y), 0, 0, &message)
		return nativeError(ok, message)
	}); err != nil {
		return target, err
	}
	// CGEventPost is asynchronous. Confirm real pointer arrival before a caller
	// can press a button, rather than treating event creation as delivery.
	waitCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := d.checkPointerTarget(waitCtx, target, a.X, a.Y); err != nil {
			return target, err
		}
		point, err = d.CursorPosition(waitCtx)
		if err == nil && point.X == a.X && point.Y == a.Y {
			return target, nil
		}
		select {
		case <-waitCtx.Done():
			return target, fmt.Errorf("cursor arrival was not confirmed: %w", waitCtx.Err())
		case <-ticker.C:
		}
	}
}
