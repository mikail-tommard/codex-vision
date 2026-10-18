//go:build darwin && cgo

package macos

/*
#include <stdlib.h>
#include "bridge.h"
*/
import "C"
import (
	"context"
	"example.com/codex-vision/internal/input"
	"example.com/codex-vision/internal/platform"
	"fmt"
	"time"
	"unsafe"
)

func (d *Desktop) launch(ctx context.Context, a platform.Action) error {
	if err := input.ValidateLaunch(a.Process, a.URL); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	process, url := C.CString(a.Process), C.CString(a.URL)
	defer C.free(unsafe.Pointer(process))
	defer C.free(unsafe.Pointer(url))
	var message *C.char
	ok := C.cv_launch(process, url, &message)
	if err := nativeError(ok, message); err != nil {
		return err
	}
	// Cold launches need longer than switching an existing window. The outer
	// MCP timeout and stop cancellation still bound this wait.
	waitCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	observed := "no foreground observation"
	for {
		if err := waitCtx.Err(); err != nil {
			return fmt.Errorf("application launch requested but foreground not confirmed for %q; observed %s: %w", a.Process, observed, err)
		}
		if err := d.permissionState().AccessibilityError(); err != nil {
			return err
		}
		w, err := d.Active(waitCtx)
		if err == nil {
			matched, matchErr := platform.ProcessMatches(waitCtx, d, w, a.Process)
			if matchErr != nil {
				return matchErr
			}
			if matched || freeCADLaunchRuntime(a.Process, w.Process) {
				return nil
			}
			observed = fmt.Sprintf("window_id=%s process=%q", w.ID, w.Process)
		} else {
			observed = err.Error()
		}
		select {
		case <-waitCtx.Done():
		case <-ticker.C:
		}
	}
}
