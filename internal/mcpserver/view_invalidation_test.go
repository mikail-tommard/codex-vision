package mcpserver

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Use the public MCP path: invalidation must apply to both catalog profiles,
// including separate semantic writes and failed attempts with uncertain effects.
func TestSeparateInputInvalidatesAgentView(t *testing.T) {
	for _, tc := range []struct {
		name, tool, profile string
		denied, nativeFail  bool
	}{
		{name: "key", tool: "keyboard_press", profile: "full"},
		{name: "pointer", tool: "mouse_move", profile: "full"},
		{name: "focus", tool: "window_focus", profile: "full"},
		{name: "compact_focus", tool: "window_focus", profile: "compact"},
		{name: "fields", tool: "ui_fill_batch", profile: "full"},
		{name: "compact_fields", tool: "ui_fill_batch", profile: "compact"},
		{name: "denied", tool: "keyboard_press", profile: "full", denied: true},
		{name: "native_failure", tool: "ui_fill_batch", profile: "compact", nativeFail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := agentFixture("Saved")
			cfg := uiConfig()
			cfg.Allow = []string{"mouse_click", "mouse_move", "window_focus", "ui_fill"}
			if !tc.denied {
				cfg.Allow = append(cfg.Allow, "keyboard_press")
			}
			if tc.nativeFail {
				d.failAt = 1
			}
			c, svc := connectProfileDesktop(t, cfg, d, tc.profile)
			view := agentResult(t, call(t, c, "desktop_observe", map[string]any{"include_fields": true, "labels": []string{"Name"}}, true))
			args := map[string]any{}
			switch tc.tool {
			case "keyboard_press":
				args["key"] = "SPACE"
			case "mouse_move":
				args["x"], args["y"] = 50, 50
			case "window_focus":
				args["window_id"] = "mock-window"
			case "ui_fill_batch":
				args["snapshot_id"] = view.Fields["snapshot_id"]
				args["fields"] = []map[string]any{{"label": "Name", "text": "Synthetic"}}
			}
			call(t, c, tc.tool, args, !tc.denied && !tc.nativeFail)
			before := len(d.actions())
			imagesBefore := svc.metrics.snapshot()["images_returned"]
			result := call(t, c, "desktop_act", map[string]any{
				"view_id": view.View.ID, "steps": []map[string]any{{"action": "click", "x": 50, "y": 50}},
				"settle_ms": 100, "wait_ms": 100,
			}, false)
			stale := agentResult(t, result)
			if stale.Failure.Code != "stale_reference" || len(d.actions()) != before {
				t.Fatal("separate input left an actionable old view", stale, d.actions())
			}
			if len(result.Content) != 1 || svc.metrics.snapshot()["images_returned"] != imagesBefore {
				t.Fatal("stale rejection returned an unnecessary image")
			}
			t.Logf("separate_actions=%d stale_view_extra_actions=%d rejection_images=0", before, len(d.actions())-before)
		})
	}
}

func TestSeparateReadPreservesAgentView(t *testing.T) {
	d := agentFixture("Saved")
	c, svc := agentClient(t, d)
	view := agentResult(t, call(t, c, "desktop_observe", map[string]any{}, true))
	call(t, c, "session_status", map[string]any{}, true)
	call(t, c, "window_get_active", map[string]any{}, true)
	svc.views.mu.Lock()
	retained := svc.views.current != nil && svc.views.current.ID == view.View.ID
	svc.views.mu.Unlock()
	if !retained {
		t.Fatal("read invalidated view")
	}
	out := agentResult(t, call(t, c, "desktop_act", agentClick(view.View.ID, view.Controls[0].ID, "Saved 1"), true))
	if !out.Verification.Matched || len(d.actions()) != 1 {
		t.Fatal("read prevented subsequent verified action", out)
	}
}

func TestFreshObserveAfterSeparateInputRestoresAction(t *testing.T) {
	d := agentFixture("Saved")
	c, _ := agentClient(t, d)
	old := agentResult(t, call(t, c, "desktop_observe", map[string]any{}, true))
	call(t, c, "keyboard_press", map[string]any{"key": "SPACE"}, true)
	fresh := agentResult(t, call(t, c, "desktop_observe", map[string]any{}, true))
	if fresh.View.ID == "" || fresh.View.ID == old.View.ID {
		t.Fatal("fresh observation did not replace view", fresh)
	}
	out := agentResult(t, call(t, c, "desktop_act", agentClick(fresh.View.ID, fresh.Controls[0].ID, "Saved 2"), true))
	if !out.Verification.Matched || len(d.actions()) != 2 {
		t.Fatal("fresh observation did not restore verified input", out, d.actions())
	}
}

func TestObserveAndInputShareLane(t *testing.T) {
	for _, observeFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "input_first", true: "observe_first"}[observeFirst], func(t *testing.T) {
			_, svc := agentClient(t, agentFixture("Saved"))
			first, second := "keyboard_press", "desktop_observe"
			if observeFirst {
				first, second = second, first
			}
			release, err := svc.lockTool(context.Background(), first)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
			defer cancel()
			otherRelease, err := svc.lockTool(ctx, second)
			if otherRelease != nil {
				otherRelease()
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("observe overlapped input", err)
			}
			release()
			// Waiting for the second lane must release the first on cancellation.
			retry, retryCancel := context.WithTimeout(context.Background(), time.Second)
			defer retryCancel()
			unlock, err := svc.lockTool(retry, "desktop_observe")
			if err != nil {
				t.Fatal("cancelled waiter leaked a lane", err)
			}
			unlock()
		})
	}
}

func TestStopBypassesObservationAndInputLanes(t *testing.T) {
	c, svc := agentClient(t, agentFixture("Saved"))
	release, err := svc.lockTool(context.Background(), "desktop_act")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	call(t, c, "session_stop", map[string]any{}, true)
	if svc.Session.ObservationError() == nil {
		t.Fatal("stop did not latch while lanes were occupied")
	}
}
