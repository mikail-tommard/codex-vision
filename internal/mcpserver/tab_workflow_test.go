package mcpserver

import (
	"context"
	"image"
	"testing"

	"example.com/codex-vision/internal/observation"
	"example.com/codex-vision/internal/platform"
)

// Synthetic tab title changes with identical pixels: native title verification
// must finish without a visual timeout, another capture or a stream reset.
type tabFixture struct{ *agentDesktop }

func (d *tabFixture) Capture(ctx context.Context, r platform.Rect) (image.Image, error) {
	return image.NewRGBA(image.Rect(0, 0, r.Width, r.Height)), ctx.Err()
}

func TestTabTitleVerificationNeedsNoExtraObservation(t *testing.T) {
	d := &tabFixture{agentFixture("Telegram")}
	cfg := uiConfig()
	cfg.Allow = []string{"mouse_click"}
	cfg.TimeoutMS = 6000
	c, svc := connectProfileDesktop(t, cfg, d, "compact")
	v := agentResult(t, call(t, c, "desktop_observe", map[string]any{"include_controls": true, "interval_ms": 250, "min_delivery_ms": 250}, true))
	out := call(t, c, "desktop_act", agentClick(v.View.ID, v.Controls[0].ID, "Telegram 1"), true)
	verified := agentResult(t, out)
	if !verified.Verification.Matched || !verified.Interaction.ReadyConfirmed || verified.Interaction.TimedOut || len(out.Content) != 1 {
		t.Fatal("title not verified without image", verified)
	}
	if svc.metrics.snapshot()["tool_calls"] != int64(2) || svc.metrics.snapshot()["images_returned"] != int64(1) || svc.Observer.Status().ID != v.View.ObservationID {
		t.Fatal("extra tool/image/stream restart", svc.metrics.snapshot())
	}
}

func TestContinuationDoesNotSuggestResetOrInputReplay(t *testing.T) {
	r := observation.ActionWaitResult{Posted: true, TimedOut: true}
	continuation := actionContinuation(r, false, nil)
	if continuation["next"] != "waiting" || continuation["restart_observation"] != false || continuation["repeat_action"] != false || continuation["verification_only"] != true {
		t.Fatal(continuation)
	}
	withImage := actionContinuation(r, true, nil)
	if withImage["next"] != "inspect_result" || withImage["immediate_observation_needed"] != false {
		t.Fatal(withImage)
	}
}
