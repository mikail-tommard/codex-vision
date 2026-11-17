package mcpserver

import (
	"context"
	"fmt"
	"testing"

	"example.com/codex-vision/internal/observation"
)

func TestActionOutcomeRequiresEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, status string
		meta         map[string]any
		err          error
		verified     bool
	}{
		{name: "posted_only", status: "ambiguous", meta: map[string]any{"interaction": observation.ActionWaitResult{Posted: true}}},
		{name: "visual_change", status: "ambiguous", meta: map[string]any{"interaction": observation.ActionWaitResult{Posted: true, OwnChangeObserved: true}}},
		{name: "predicate", status: "success", verified: true, meta: map[string]any{"interaction": observation.ActionWaitResult{Posted: true}, "verification": verificationReport{Configured: true, Matched: true, Scope: "native predicate"}}},
		{name: "wait_timeout", status: "timeout", meta: map[string]any{"interaction": observation.ActionWaitResult{Posted: true, TimedOut: true}}},
		{name: "ambiguous_target", status: "ambiguous", err: fmt.Errorf("ambiguous target")},
		{name: "confirmation", status: "needs_confirmation", err: fmt.Errorf("approval_required: operator review")},
		{name: "cancelled", status: "failed", err: context.Canceled},
		{name: "deadline", status: "timeout", err: context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := result(tc.meta)
			if tc.err != nil {
				out = fail(tc.err)
			}
			out = withActionOutcome("desktop_act", out)
			r := out.StructuredContent.(map[string]any)["outcome"].(ActionOutcome)
			if r.Status != tc.status || r.Verified != tc.verified || r.RetryAction {
				t.Fatal(r)
			}
		})
	}
}

func TestOutcomeSurvivesMCPCompactAndFailurePaths(t *testing.T) {
	for _, profile := range []string{"full", "compact"} {
		t.Run(profile, func(t *testing.T) {
			d := agentFixture("Saved")
			cfg := uiConfig()
			cfg.Allow = []string{"mouse_click"}
			c, _ := connectProfileDesktop(t, cfg, d, profile)
			view := agentResult(t, call(t, c, "desktop_observe", map[string]any{"include_controls": true}, true))
			good := fieldsData(t, call(t, c, "desktop_act", agentClick(view.View.ID, view.Controls[0].ID, "Saved"), true))
			if good["outcome"].(map[string]any)["status"] != "success" {
				t.Fatal(good)
			}
			bad := fieldsData(t, call(t, c, "desktop_act", agentClick(view.View.ID, view.Controls[0].ID, "Saved"), false))
			if bad["outcome"].(map[string]any)["status"] != "failed" {
				t.Fatal(bad)
			}
		})
	}
}
