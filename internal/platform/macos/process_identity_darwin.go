//go:build darwin && cgo

package macos

/*
#include <stdlib.h>
#include "bridge.h"
*/
import "C"
import (
	"context"
	"example.com/codex-vision/internal/platform"
	"fmt"
	"unsafe"
)

func (d *Desktop) MatchesWindowProcess(ctx context.Context, w platform.Window, expected string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if !codeSignCloneCandidate(w.Process, expected) {
		return false, nil
	}
	d.mu.Lock()
	pid := 0
	for _, token := range d.ids {
		if token.ID == w.ID && token.Process == w.Process {
			pid = token.PID
			break
		}
	}
	d.mu.Unlock()
	if pid <= 0 {
		return false, fmt.Errorf("window process identity is stale; refresh window_list")
	}
	if d.verifyProcess != nil {
		return d.verifyProcess(ctx, pid, w.Process, expected)
	}
	raw, canonical := C.CString(w.Process), C.CString(expected)
	defer C.free(unsafe.Pointer(raw))
	defer C.free(unsafe.Pointer(canonical))
	var message *C.char
	ok := C.cv_process_matches(C.int(pid), raw, canonical, &message)
	if message != nil {
		return false, nativeError(ok, message)
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return ok == 1, nil
}

func executableCopyMatches(raw, expected string) bool {
	left, right := C.CString(raw), C.CString(expected)
	defer C.free(unsafe.Pointer(left))
	defer C.free(unsafe.Pointer(right))
	return C.cv_executable_copy_matches(left, right) == 1
}
