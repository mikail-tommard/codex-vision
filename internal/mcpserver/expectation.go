package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"time"

	"example.com/codex-vision/internal/platform"
)

// Only observable structural predicates, never arbitrary code or field values.
type ExpectedState struct {
	TitleContains     string `json:"title_contains,omitempty"`
	ControlLabel      string `json:"control_label,omitempty"`
	ControlRole       string `json:"control_role,omitempty"`
	Present           *bool  `json:"present,omitempty" jsonschema:"Control predicate: default true; false requires a complete tree proving absence"`
	Enabled           *bool  `json:"enabled,omitempty"`
	RequireTransition *bool  `json:"require_transition,omitempty" jsonschema:"Default true: refuse input if the expected state is already satisfied before the batch"`
}

type verificationReport struct {
	Configured        bool   `json:"configured"`
	Matched           bool   `json:"matched"`
	Checks            int    `json:"checks"`
	RequireTransition bool   `json:"require_transition"`
	Scope             string `json:"scope"`
}

type expectationGuard struct {
	v      *Service
	w      platform.Window
	spec   ExpectedState
	since  time.Time
	settle time.Duration
	report verificationReport
}

func (v *Service) prepareExpectation(ctx context.Context, w platform.Window, s *ExpectedState, settle time.Duration) (*expectationGuard, error) {
	if s == nil {
		return nil, nil
	}
	if (s.TitleContains == "") == (s.ControlLabel == "") || len(s.TitleContains) > 512 || len(s.ControlLabel) > 512 || len(s.ControlRole) > 128 {
		return nil, fmt.Errorf("expect requires exactly one title_contains or control_label (up to 512 bytes)")
	}
	if s.TitleContains != "" && (s.ControlRole != "" || s.Present != nil || s.Enabled != nil) {
		return nil, fmt.Errorf("title expectation cannot include control options")
	}
	if s.Present != nil && !*s.Present && s.Enabled != nil {
		return nil, fmt.Errorf("absent control cannot have enabled expectation")
	}
	g := &expectationGuard{v: v, w: w, spec: *s, settle: settle, report: verificationReport{Configured: true, RequireTransition: s.RequireTransition == nil || *s.RequireTransition, Scope: "explicit native UI predicate only; not application semantics or game response"}}
	matched, err := g.read(ctx)
	if err != nil {
		return nil, err
	}
	if matched && g.report.RequireTransition {
		return nil, fmt.Errorf("expectation already satisfied before input; inspect current state instead of replaying actions")
	}
	return g, nil
}

func (g *expectationGuard) read(ctx context.Context) (bool, error) {
	g.report.Checks++
	if g.spec.TitleContains != "" {
		var w platform.Window
		err := g.v.withLocalRead(ctx, func() error { var e error; w, e = g.v.attentionWindow(ctx, g.w.ID); return e })
		if err != nil {
			return false, err
		}
		if w.Process != g.w.Process || w.Bounds != g.w.Bounds {
			return false, fmt.Errorf("window changed during expectation check")
		}
		return strings.Contains(w.Title, g.spec.TitleContains), nil
	}
	raw, err := g.v.readControls(ctx, g.w, 500)
	if err != nil {
		return false, err
	}
	if raw.Truncated {
		return false, fmt.Errorf("expectation cannot be verified from an incomplete control tree")
	}
	var selected platform.UIControl
	count := 0
	for _, c := range raw.Controls {
		if c.Label == g.spec.ControlLabel && (g.spec.ControlRole == "" || c.Role == g.spec.ControlRole) {
			count++
			selected = c
		}
	}
	if count > 1 {
		return false, fmt.Errorf("ambiguous expectation control")
	}
	present := g.spec.Present == nil || *g.spec.Present
	if !present {
		return count == 0, nil
	}
	if count == 0 {
		return false, nil
	}
	if g.spec.Enabled != nil {
		if selected.Enabled == nil {
			return false, fmt.Errorf("expectation enabled state unknown")
		}
		return *selected.Enabled == *g.spec.Enabled, nil
	}
	return true, nil
}

func (g *expectationGuard) observe(ctx context.Context) (bool, error) {
	matched, err := g.read(ctx)
	if err != nil {
		return false, err
	}
	if !matched {
		g.since = time.Time{}
		return false, nil
	}
	if g.since.IsZero() {
		g.since = time.Now()
		return false, nil
	}
	if time.Since(g.since) < g.settle {
		return false, nil
	}
	g.report.Matched = true
	return true, nil
}
