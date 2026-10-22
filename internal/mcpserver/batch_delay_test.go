package mcpserver

import (
	"context"
	"fmt"
	"testing"
	"time"

	"example.com/codex-vision/internal/platform"
)

// Synthetic known two-click menu: its second button accepts input only after
// a short transition. This is not a Minecraft/Pokeworld integration test.
type delayedMenuDesktop struct {
	*agentDesktop
	readyAt   time.Time
	loseFocus bool
	cancel    context.CancelFunc
}

func (d *delayedMenuDesktop) Execute(ctx context.Context, a platform.Action) error {
	if a.Tool == "mouse_click" && !d.readyAt.IsZero() && time.Now().Before(d.readyAt) {
		return fmt.Errorf("menu not ready")
	}
	if err := d.agentDesktop.Execute(ctx, a); err != nil {
		return err
	}
	if a.Tool == "mouse_click" && d.readyAt.IsZero() {
		d.readyAt = time.Now().Add(80 * time.Millisecond)
		if d.loseFocus {
			d.stateMu.Lock()
			d.window.ID = "other-window"
			d.stateMu.Unlock()
		}
		if d.cancel != nil {
			d.cancel()
		}
	}
	return nil
}

func TestKnownMenuPairInOneCallAndDelayStopsOnFocusLoss(t *testing.T) {
	for _, loseFocus := range []bool{false, true} {
		t.Run(fmt.Sprint(loseFocus), func(t *testing.T) {
			d := &delayedMenuDesktop{agentDesktop: agentFixture("Menu"), loseFocus: loseFocus}
			cfg := uiConfig()
			cfg.Allow = []string{"mouse_click"}
			cfg.TimeoutMS = 6000
			c, _ := connectDesktop(t, cfg, d)
			initial := agentResult(t, call(t, c, "desktop_observe", map[string]any{"include_controls": false}, true))
			args := map[string]any{"view_id": initial.View.ID, "steps": []map[string]any{{"action": "click", "x": 50, "y": 50}, {"action": "click", "x": 50, "y": 50, "delay_before_ms": 150}}, "expect": map[string]any{"title_contains": "Menu 2"}, "settle_ms": 100, "wait_ms": 1500}
			r := call(t, c, "desktop_act", args, !loseFocus)
			out := agentResult(t, r)
			if loseFocus {
				if out.Completed != 1 || !out.Interaction.MayHaveChanged || len(d.actions()) != 1 {
					t.Fatal(out, d.actions())
				}
			} else if out.Completed != 2 || !out.Verification.Matched || len(d.actions()) != 2 || len(r.Content) != 1 || out.View.ID == "" {
				t.Fatal(out, d.actions(), len(r.Content))
			}
		})
	}
}

func TestMenuDelayCancellationKeepsPartialReport(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := &delayedMenuDesktop{agentDesktop: agentFixture("Menu"), cancel: cancel}
	cfg := uiConfig()
	cfg.Allow = []string{"mouse_click"}
	c, svc := connectDesktop(t, cfg, d)
	initial := agentResult(t, call(t, c, "desktop_observe", map[string]any{"include_controls": false}, true))
	out, err := svc.desktopAct(ctx, DesktopActArgs{ViewID: initial.View.ID, Steps: []DesktopStep{{Action: "click", X: 50, Y: 50}, {Action: "click", X: 50, Y: 50, DelayBeforeMS: 1000}}, SettleMS: 100, WaitMS: 1000})
	if err != nil {
		t.Fatal(err)
	}
	r := agentResult(t, out)
	if !out.IsError || r.Completed != 1 || !r.Interaction.MayHaveChanged || len(d.actions()) != 1 {
		t.Fatal(r, d.actions())
	}
}

func TestInvalidMenuDelayBeforeRefocusPreservesView(t *testing.T) {
	for _, delays := range [][]int{{-1}, {1001}, {1000, 1000, 1}} {
		d := agentFixture("Menu")
		c, svc := agentClient(t, d)
		initial := agentResult(t, call(t, c, "desktop_observe", map[string]any{}, true))
		d.stateMu.Lock()
		d.window.ID = "other-window"
		d.stateMu.Unlock()
		steps := []DesktopStep{}
		for _, delay := range delays {
			steps = append(steps, DesktopStep{Action: "click", X: 50, Y: 50, DelayBeforeMS: delay})
		}
		if _, err := svc.desktopAct(context.Background(), DesktopActArgs{ViewID: initial.View.ID, Steps: steps}); err == nil || len(d.actions()) != 0 {
			t.Fatal(err, d.actions())
		}
		svc.views.mu.Lock()
		retained := svc.views.current != nil && svc.views.current.ID == initial.View.ID && !svc.views.current.FocusAttempted
		svc.views.mu.Unlock()
		if !retained {
			t.Fatal("invalid delays changed view")
		}
	}
}
