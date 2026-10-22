package windows

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func idleFocusOps() focusOps {
	return focusOps{
		check:      func(ctx context.Context) error { return ctx.Err() },
		foreground: func() uintptr { return 1 },
		minimized:  func() bool { return false },
		restore:    func() bool { panic("unexpected restore") },
		activate:   func() bool { return true },
	}
}

func TestFocusWaitsForAsynchronousActivationOnce(t *testing.T) {
	for _, accepted := range []bool{true, false} {
		ops := idleFocusOps()
		activations, reads := 0, 0
		ops.activate = func() bool { activations++; return accepted }
		ops.foreground = func() uintptr {
			reads++
			if reads >= 4 {
				return 2
			}
			return 1
		}
		if err := focusWindow(context.Background(), 2, time.Second, ops); err != nil {
			t.Fatal(err)
		}
		if activations != 1 || reads < 4 {
			t.Fatal("activation was retried or not confirmed", activations, reads)
		}
	}
}

func TestFocusRestoresMinimizedWindowBeforeActivation(t *testing.T) {
	ops := idleFocusOps()
	restores, activations, minimizedReads := 0, 0, 0
	ops.minimized = func() bool { minimizedReads++; return minimizedReads < 3 }
	ops.restore = func() bool { restores++; return true }
	ops.activate = func() bool {
		if minimizedReads < 3 {
			t.Fatal("activated before restore completed")
		}
		activations++
		return true
	}
	ops.foreground = func() uintptr {
		if activations == 1 {
			return 2
		}
		return 1
	}
	if err := focusWindow(context.Background(), 2, time.Second, ops); err != nil {
		t.Fatal(err)
	}
	if restores != 1 || activations != 1 {
		t.Fatal(restores, activations)
	}
}

func TestFocusDenialDoesNotRetryOrSucceedOnWrongWindow(t *testing.T) {
	ops := idleFocusOps()
	activations := 0
	ops.activate = func() bool { activations++; return false }
	err := focusWindow(context.Background(), 2, 65*time.Millisecond, ops)
	if err == nil || !strings.Contains(err.Error(), "foreground not confirmed") || !strings.Contains(err.Error(), "set_foreground_returned=false") || activations != 1 {
		t.Fatal(err, activations)
	}
}

func TestFocusAlreadyActiveNeedsNoRequest(t *testing.T) {
	ops := idleFocusOps()
	ops.raise = func() bool { t.Fatal("unexpected raise"); return false }
	ops.activate = func() bool { t.Fatal("unexpected activation"); return false }
	if err := focusWindow(context.Background(), 1, time.Second, ops); err != nil {
		t.Fatal(err)
	}
}

func TestFocusRaiseCanActivateWithoutSecondRequest(t *testing.T) {
	ops := idleFocusOps()
	active, raises := uintptr(1), 0
	ops.foreground = func() uintptr { return active }
	ops.raise = func() bool { raises++; active = 2; return true }
	ops.activate = func() bool { t.Fatal("already focused after raise"); return false }
	if err := focusWindow(context.Background(), 2, time.Second, ops); err != nil || raises != 1 {
		t.Fatal(err, raises)
	}
}

func TestFocusRaiseDoesNotProveFocusOrBypassRevalidation(t *testing.T) {
	for _, mode := range []string{"denied", "stale", "cancelled", "fallback"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ops := idleFocusOps()
			raises, activations := 0, 0
			active := uintptr(1)
			stale := errors.New("stale window ID after raise")
			ops.check = func(ctx context.Context) error {
				if mode == "stale" && raises > 0 {
					return stale
				}
				return ctx.Err()
			}
			ops.foreground = func() uintptr { return active }
			ops.raise = func() bool {
				raises++
				if mode == "cancelled" {
					cancel()
				}
				return true
			}
			ops.activate = func() bool {
				activations++
				if mode == "fallback" {
					active = 2
					return true
				}
				return false
			}
			err := focusWindow(ctx, 2, 100*time.Millisecond, ops)
			if raises != 1 {
				t.Fatal("raise repeated", raises)
			}
			switch mode {
			case "fallback":
				if err != nil || activations != 1 {
					t.Fatal(err, activations)
				}
			case "denied":
				if err == nil || activations != 1 || !strings.Contains(err.Error(), "bring_window_to_top_returned=true") || !strings.Contains(err.Error(), "set_foreground_returned=false") {
					t.Fatal(err, activations)
				}
			case "stale":
				if !errors.Is(err, stale) || activations != 0 {
					t.Fatal(err, activations)
				}
			case "cancelled":
				if !errors.Is(err, context.Canceled) || activations != 0 {
					t.Fatal(err, activations)
				}
			}
		})
	}
}

func TestFocusRaiseWaitsForRestore(t *testing.T) {
	ops := idleFocusOps()
	minimized, raises := true, 0
	ops.minimized = func() bool { return minimized }
	ops.restore = func() bool { minimized = false; return true }
	ops.raise = func() bool {
		if minimized {
			t.Fatal("raised minimized window")
		}
		raises++
		return true
	}
	ops.foreground = func() uintptr {
		if raises > 0 {
			return 2
		}
		return 1
	}
	ops.activate = func() bool { t.Fatal("unexpected activation"); return false }
	if err := focusWindow(context.Background(), 2, time.Second, ops); err != nil || raises != 1 {
		t.Fatal(err, raises)
	}
}

func TestFocusCancellationAndStaleIdentityStopImmediately(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ops := idleFocusOps()
	activations := 0
	ops.activate = func() bool { activations++; cancel(); return true }
	if err := focusWindow(ctx, 2, time.Second, ops); !errors.Is(err, context.Canceled) || activations != 1 {
		t.Fatal(err, activations)
	}

	ops = idleFocusOps()
	stale := errors.New("stale window ID")
	checks := 0
	ops.check = func(context.Context) error {
		checks++
		if checks > 1 {
			return stale
		}
		return nil
	}
	if err := focusWindow(context.Background(), 2, time.Second, ops); !errors.Is(err, stale) {
		t.Fatal(err)
	}
}

func TestUnrestoredWindowDoesNotReceiveActivation(t *testing.T) {
	ops := idleFocusOps()
	ops.minimized = func() bool { return true }
	restores := 0
	ops.restore = func() bool { restores++; return false }
	ops.activate = func() bool { t.Fatal("activation while minimized"); return false }
	if err := focusWindow(context.Background(), 2, 65*time.Millisecond, ops); err == nil || restores != 1 {
		t.Fatal(err, restores)
	}
}
