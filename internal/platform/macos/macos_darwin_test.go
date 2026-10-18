//go:build darwin && cgo

package macos

import (
	"context"
	"encoding/json"
	"example.com/codex-vision/internal/platform"
	"image"
	"os"
	"testing"
	"time"
)

func statusBool(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case float64:
		return x != 0
	}
	return false
}
func TestPermissionFailureIsExplicit(t *testing.T) {
	p, e := Permissions(false)
	if e != nil {
		t.Fatal(e)
	}
	d, e := New()
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if !statusBool(p["unlocked_console"]) {
		if _, e = d.Monitors(ctx); e == nil {
			t.Fatal("no session must not pretend monitors exist")
		}
	}
	if !statusBool(p["screen_capture"]) || !statusBool(p["unlocked_console"]) {
		if _, e = d.Capture(ctx, platform.Rect{Width: 1, Height: 1}); e == nil {
			t.Fatal("capture succeeded without session/permission")
		}
	}
	if !statusBool(p["accessibility"]) || !statusBool(p["unlocked_console"]) {
		if e = d.Execute(ctx, platform.Action{Tool: "mouse_move"}); e == nil {
			t.Fatal("input succeeded without permission")
		}
	}
}
func TestNativeMacReadIntegration(t *testing.T) {
	if os.Getenv("CODEX_VISION_NATIVE_INTEGRATION") != "1" {
		t.Skip("set CODEX_VISION_NATIVE_INTEGRATION=1 in an unlocked macOS session")
	}
	p, e := Permissions(false)
	if e != nil {
		t.Fatal(e)
	}
	if !statusBool(p["screen_capture"]) || !statusBool(p["unlocked_console"]) {
		t.Skipf("native desktop unavailable: %+v", p)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	d, _ := New()
	ms, e := d.Monitors(ctx)
	if e != nil || len(ms) == 0 {
		t.Fatal(ms, e)
	}
	b := ms[0].Bounds
	im, e := d.Capture(ctx, platform.Rect{X: b.X, Y: b.Y, Width: 32, Height: 32})
	if e != nil || im.Bounds() != image.Rect(0, 0, 32, 32) {
		t.Fatal(e)
	}
	if _, e = d.Windows(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e = d.Active(ctx); e != nil {
		t.Fatal(e)
	}
}

func TestWindowDiscoveryIncludesOffscreenAndPreservesIdentity(t *testing.T) {
	ctx := context.Background()
	d, _ := New()
	visible, offscreen := true, false
	ws := []nativeWindow{
		{NativeID: 101, PID: 20, Process: "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome", Title: "Chrome on another Space", Bounds: platform.Rect{Width: 900, Height: 700}, OnScreen: &offscreen},
		{NativeID: 102, PID: 20, Process: "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome", Title: "Minimized Chrome", Bounds: platform.Rect{Width: 800, Height: 600}, OnScreen: &offscreen},
		{NativeID: 103, PID: 21, Process: "/Applications/Test.app/Contents/MacOS/Test", Title: "Current window", Bounds: platform.Rect{Width: 640, Height: 480}, OnScreen: &visible},
	}
	d.readWindows = func(ctx context.Context, active bool) ([]nativeWindow, error) {
		if active {
			return []nativeWindow{ws[2]}, ctx.Err()
		}
		return ws, ctx.Err()
	}
	all, err := d.Windows(ctx)
	if err != nil || len(all) != 3 {
		t.Fatalf("discovery omitted windows: %v %v", all, err)
	}
	if all[0].OnScreen == nil || *all[0].OnScreen || all[1].OnScreen == nil || *all[1].OnScreen {
		t.Fatal("offscreen metadata lost", all)
	}
	active, err := d.Active(ctx)
	if err != nil || active.ID != all[2].ID {
		t.Fatal("offscreen window treated as foreground", active, err)
	}
	// Restoring/switching a window must retain the observed opaque ID so
	// window_focus's exact-window confirmation can succeed.
	ws[0].OnScreen = &visible
	ws[2] = ws[0]
	active, err = d.Active(ctx)
	if err != nil || active.ID != all[0].ID || active.OnScreen == nil || !*active.OnScreen {
		t.Fatal("window identity changed with visibility", active, err)
	}
	// Reuse of the native handle by another process must not inherit authority.
	ws[2].PID++
	reused, err := d.Active(ctx)
	if err != nil || reused.ID == active.ID {
		t.Fatal("reused native window inherited token", reused, err)
	}
}

func TestWindowVisibilityFalseSurvivesJSON(t *testing.T) {
	var native []nativeWindow
	if err := json.Unmarshal([]byte(`[{"native_id":1,"pid":2,"process":"Chrome","title":"Other Space","bounds":{"x":0,"y":0,"width":640,"height":480},"on_screen":false}]`), &native); err != nil {
		t.Fatal(err)
	}
	d, _ := New()
	d.readWindows = func(context.Context, bool) ([]nativeWindow, error) { return native, nil }
	windows, err := d.Windows(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(windows)
	if err != nil {
		t.Fatal(err)
	}
	var response []map[string]any
	if err = json.Unmarshal(data, &response); err != nil {
		t.Fatal(err)
	}
	if val, ok := response[0]["on_screen"]; !ok || val != false {
		t.Fatalf("false visibility omitted: %s", data)
	}
}

func TestPointerGuardRejectsChangedWindowBeforePosting(t *testing.T) {
	for _, kind := range []string{"same", "different_window", "different_process", "geometry_changed", "outside_window", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			d, _ := New()
			window := nativeWindow{NativeID: 90, PID: 23, Process: "/Applications/Test.app/Contents/MacOS/Test", Bounds: platform.Rect{Width: 100, Height: 100}}
			d.readWindows = func(ctx context.Context, active bool) ([]nativeWindow, error) {
				return []nativeWindow{window}, ctx.Err()
			}
			target, err := d.Active(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			x, y := 20, 30
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch kind {
			case "different_window":
				window.NativeID++
			case "different_process":
				window.Process = "/Applications/Denied.app/Contents/MacOS/Denied"
			case "geometry_changed":
				window.Bounds.Width++
			case "outside_window":
				x = 101
			case "cancelled":
				cancel()
			}
			err = d.checkPointerTarget(ctx, target, x, y)
			if (err == nil) != (kind == "same") {
				t.Fatal(kind, err)
			}
		})
	}
}
