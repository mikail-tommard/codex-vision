package windows

import (
	"context"
	"fmt"
	"time"
)

// Kept platform-independent so activation sequencing can be tested without
// sending input on the developer's desktop. Native callbacks retain PID and
// interactive-desktop validation on every poll.
type focusOps struct {
	check      func(context.Context) error
	foreground func() uintptr
	minimized  func() bool
	restore    func() bool
	raise      func() bool
	activate   func() bool
}

func focusWindow(ctx context.Context, target uintptr, timeout time.Duration, ops focusOps) error {
	if target == 0 || timeout <= 0 {
		return fmt.Errorf("invalid focus target or timeout")
	}
	waiting, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var restored, restoreAccepted, raised, raiseAccepted, activated, activationAccepted bool
	var observed uintptr
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := waiting.Err(); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("Windows foreground not confirmed: restore_requested=%t restore_accepted=%t raise_requested=%t bring_window_to_top_returned=%t activation_requested=%t set_foreground_returned=%t observed_hwnd=0x%x; activation may be restricted or delayed; stop input and activate the target manually", restored, restoreAccepted, raised, raiseAccepted, activated, activationAccepted, observed)
		}
		if err := ops.check(waiting); err != nil {
			return err
		}
		observed = ops.foreground()
		minimized := ops.minimized()
		if observed == target && !minimized {
			return nil
		}
		if minimized {
			if !restored {
				restored = true
				restoreAccepted = ops.restore()
			}
		} else if !raised && ops.raise != nil {
			// Raising an obscured top-level window may itself activate it.
			// Revalidate identity and inspect the actual foreground before making
			// any further request. This never changes topmost/lock settings.
			raised = true
			raiseAccepted = ops.raise()
			continue
		} else if !activated {
			// Exactly one request. A cross-thread activation may finish later;
			// neither BOOL alone nor an immediate HWND check proves the result.
			activated = true
			activationAccepted = ops.activate()
			continue
		}
		select {
		case <-waiting.Done():
		case <-ticker.C:
		}
	}
}
