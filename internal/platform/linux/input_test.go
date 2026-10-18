package linux

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"example.com/codex-vision/internal/config"
	"example.com/codex-vision/internal/platform"
	"example.com/codex-vision/internal/safety"
)

func TestX11ClicksKeysTextAndDrag(t *testing.T) {
	d, f := testDesktop(t)
	ctx := context.Background()
	w, err := d.Active(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range []platform.Action{
		{Tool: "mouse_click", X: 30, Y: 40, Button: "left", Count: 2},
		{Tool: "mouse_click", X: 40, Y: 50, Button: "right", Count: 1},
		{Tool: "keyboard_press", Keys: []string{"ENTER"}},
		{Tool: "keyboard_hotkey", Keys: []string{"CTRL", "L"}},
		{Tool: "keyboard_type", Text: "Привет 🐧 --window 100; $(do-not-run)"},
		{Tool: "mouse_drag", X: 40, Y: 50, EndX: 80, EndY: 90, Button: "left", Duration: 10 * time.Millisecond},
	} {
		a.WindowID = w.ID
		if !d.Supports(a.Tool) {
			t.Fatal("missing capability", a.Tool)
		}
		if err := d.Execute(ctx, a); err != nil {
			t.Fatal(a.Tool, err)
		}
	}
	joined := strings.Join(f.calls, "\n")
	for _, required := range []string{"xdotool click 1", "xdotool click 3", "xdotool key --delay 0 Return", "xdotool key --delay 0 Control_L+l", "xdotool type --delay 0 --file -", "xdotool mousedown 1", "xdotool mouseup 1"} {
		if !strings.Contains(joined, required) {
			t.Fatal("missing command", required, joined)
		}
	}
	if !strings.Contains(f.stdin, "Привет 🐧") || strings.Contains(joined, "do-not-run") || strings.Contains(joined, "--window") {
		t.Fatal("text leaked into argv or targeted background input", joined)
	}
	if f.x != 80 || f.y != 90 || len(d.heldButtons) != 0 || len(d.heldKeys) != 0 {
		t.Fatal("drag did not finish/release")
	}
}

func TestX11NoClickAfterFocusOrPointerMismatch(t *testing.T) {
	for _, mode := range []string{"focus", "pointer", "position"} {
		t.Run(mode, func(t *testing.T) {
			d, f := testDesktop(t)
			w, _ := d.Active(context.Background())
			f.hook = func(name string, args []string) error {
				if name == "xdotool" && args[0] == "mousemove" {
					switch mode {
					case "focus":
						f.activeID = "200"
					case "pointer":
						f.pointerWindow = "200"
					case "position":
						return nil
					}
				}
				if mode == "position" && name == "xdotool" && args[0] == "getmouselocation" {
					f.x = 500
					f.y = 400
				}
				return nil
			}
			err := d.Execute(context.Background(), platform.Action{Tool: "mouse_click", WindowID: w.ID, X: 20, Y: 30, Button: "left", Count: 1})
			if err == nil {
				t.Fatal("unsafe click accepted")
			}
			for _, c := range f.calls {
				if strings.Contains(c, "xdotool click ") {
					t.Fatal("posted unsafe click", c)
				}
			}
		})
	}
}

func TestX11DragReleasesOnLock(t *testing.T) {
	d, f := testDesktop(t)
	f.hook = func(name string, args []string) error {
		if name == "xdotool" && args[0] == "mousedown" {
			f.locked = true
		}
		return nil
	}
	err := d.Execute(context.Background(), platform.Action{Tool: "mouse_drag", X: 10, Y: 10, EndX: 100, EndY: 100, Button: "left", Duration: 10 * time.Millisecond})
	if err == nil || !strings.Contains(strings.Join(f.calls, "\n"), "xdotool mouseup 1") || len(d.heldButtons) != 0 {
		t.Fatal("drag was not aborted/released", err, f.calls)
	}
}

func TestX11KeyboardReleaseUsesIndependentCancellationContext(t *testing.T) {
	d, f := testDesktop(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.hook = func(name string, args []string) error {
		if name == "xdotool" && args[0] == "key" {
			cancel()
			return ctx.Err()
		}
		return nil
	}
	err := d.Execute(ctx, platform.Action{Tool: "keyboard_hotkey", Keys: []string{"CTRL", "L"}})
	if !errors.Is(err, context.Canceled) || !strings.Contains(strings.Join(f.calls, "\n"), "xdotool keyup --delay 0 Control_L l") || len(d.heldKeys) != 0 {
		t.Fatal("cancelled key not released", err, f.calls)
	}
}

func TestX11InvalidInputNeverPosts(t *testing.T) {
	for _, a := range []platform.Action{
		{Tool: "keyboard_press", Keys: []string{"A;touch /tmp/no"}},
		{Tool: "keyboard_type", Text: ""},
		{Tool: "mouse_click", X: 9999, Y: 1, Button: "left", Count: 1},
		{Tool: "mouse_click", X: 1, Y: 1, Button: "invalid", Count: 1},
		{Tool: "mouse_scroll", Delta: 121},
		{Tool: "app_launch", Process: "/usr/bin/anything"},
	} {
		d, f := testDesktop(t)
		if err := d.Execute(context.Background(), a); err == nil {
			t.Fatal("invalid input accepted", a)
		}
		for _, c := range f.calls {
			for _, prefix := range []string{"xdotool key ", "xdotool type ", "xdotool click ", "xdotool mousedown ", "xdotool mousemove "} {
				if strings.HasPrefix(c, prefix) {
					t.Fatal("posted invalid input", a, c)
				}
			}
		}
	}
}

func TestLinuxInputStillRequiresOperatorGrant(t *testing.T) {
	for _, mode := range []string{"observe", "no_grant", "confirmation_required", "wrong_process", "assist"} {
		t.Run(mode, func(t *testing.T) {
			d, f := testDesktop(t)
			cfg := config.Default()
			cfg.StopFile = filepath.Join(t.TempDir(), "stop")
			cfg.Mode = "assist"
			cfg.Allow = []string{"keyboard_type"}
			cfg.AllowedProcesses = []string{"/usr/bin/test-app"}
			cfg.LocalApproval = true
			cfg.ConfirmEachAction = false
			switch mode {
			case "observe":
				cfg.Mode = "observe"
			case "no_grant":
				cfg.LocalApproval = false
			case "confirmation_required":
				cfg.ConfirmEachAction = true
			case "wrong_process":
				cfg.AllowedProcesses = []string{"/usr/bin/other"}
			}
			s := safety.New(cfg, d, slog.New(slog.NewTextHandler(io.Discard, nil)))
			err := s.Execute(context.Background(), platform.Action{Tool: "keyboard_type", Text: "local test"})
			if (err == nil) != (mode == "assist") || (f.stdin != "") != (mode == "assist") {
				t.Fatal("incorrect permission gate", mode, err)
			}
		})
	}
}

func TestX11FocusMustBeConfirmed(t *testing.T) {
	d, f := testDesktop(t)
	ctx := context.Background()
	w, err := d.Active(ctx)
	if err != nil {
		t.Fatal(err)
	}
	f.activeID = "200"
	if err := d.Execute(ctx, platform.Action{Tool: "window_focus", WindowID: w.ID}); err == nil {
		t.Fatal("unconfirmed focus accepted")
	}
	activations := 0
	for _, c := range f.calls {
		if strings.Contains(c, "xdotool windowactivate ") {
			activations++
		}
	}
	if activations != 1 {
		t.Fatal("activation retried", activations)
	}
}

func TestCanonicalX11HotkeyOrder(t *testing.T) {
	if got := strings.Join(keySequence([]string{"L", "CTRL"}), "+"); got != "Control_L+l" {
		t.Fatal(got)
	}
}
