package mcpserver

import (
	"context"
	"errors"
	"example.com/codex-vision/internal/platform"
	"fmt"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"time"
)

type ReadyWhen struct {
	Label          string `json:"label" jsonschema:"One exact control label observed via ui_controls; never guess"`
	Role           string `json:"role,omitempty" jsonschema:"Optional exact AX role to disambiguate"`
	RequireCycle   *bool  `json:"require_cycle,omitempty" jsonschema:"Default true: require ready before input, then absent/disabled, then ready again. False waits only for readiness, without proof of a cycle"`
	VisualFallback *bool  `json:"visual_fallback,omitempty" jsonschema:"Default true: use unverified visual wait if control metadata unavailable BEFORE input; false refuses input. Never falls back on focus/permission errors"`
	MaxNodes       int    `json:"max_nodes,omitempty"`
}
type readinessReport struct {
	Mode         string `json:"mode"`
	Label        string `json:"label,omitempty"`
	Role         string `json:"role,omitempty"`
	Checks       int    `json:"checks"`
	BusyObserved bool   `json:"busy_observed"`
	Matched      bool   `json:"matched"`
	RequireCycle bool   `json:"require_cycle"`
	Reason       string `json:"reason,omitempty"`
}
type readyGuard struct {
	v      *Service
	window platform.Window
	spec   ReadyWhen
	report readinessReport
	since  time.Time
	settle time.Duration
}

var errControlsUnavailable = errors.New("control metadata unavailable")

func (v *Service) readControls(ctx context.Context, w platform.Window, n int) (platform.ControlSnapshot, error) {
	var raw platform.ControlSnapshot
	reader, ok := v.Desktop.(platform.ControlReader)
	if !ok {
		return raw, errControlsUnavailable
	}
	err := v.withLocalRead(ctx, func() error {
		if e := v.Session.ObservationError(); e != nil {
			return e
		}
		active, e := v.attentionWindow(ctx, w.ID)
		if e != nil {
			return e
		}
		if active.Process != w.Process || active.Bounds != w.Bounds {
			return fmt.Errorf("window changed during control read")
		}
		raw, e = reader.ReadControls(ctx, w, n)
		if e != nil {
			return e
		}
		active, e = v.attentionWindow(ctx, w.ID)
		if e != nil {
			return e
		}
		if active.Process != w.Process || active.Bounds != w.Bounds {
			return fmt.Errorf("window changed during control read")
		}
		return nil
	})
	return raw, err
}
func controlReady(raw platform.ControlSnapshot, s ReadyWhen) (ready, present bool, err error) {
	if raw.Truncated {
		return false, false, fmt.Errorf("%w: incomplete AX tree", errControlsUnavailable)
	}
	count := 0
	var selected platform.UIControl
	for _, c := range raw.Controls {
		if c.Label == s.Label && (s.Role == "" || c.Role == s.Role) {
			count++
			selected = c
		}
	}
	if count > 1 {
		return false, false, fmt.Errorf("%w: ambiguous control", errControlsUnavailable)
	}
	if count == 0 {
		return false, false, nil
	}
	if selected.Enabled == nil {
		return false, true, fmt.Errorf("%w: enabled state unknown", errControlsUnavailable)
	}
	return *selected.Enabled, true, nil
}
func (v *Service) prepareReady(ctx context.Context, w platform.Window, s *ReadyWhen, settle time.Duration) (*readyGuard, error) {
	if s == nil {
		return nil, nil
	}
	if s.Label == "" || len(s.Label) > 512 || len(s.Role) > 128 {
		return nil, fmt.Errorf("ready_when requires an exact label up to 512 bytes and optional role up to 128 bytes")
	}
	copySpec := *s
	if copySpec.MaxNodes == 0 {
		copySpec.MaxNodes = 1000
	}
	if copySpec.MaxNodes < 1 || copySpec.MaxNodes > 2000 {
		return nil, fmt.Errorf("max_nodes must be 1..2000")
	}
	cycle := s.RequireCycle == nil || *s.RequireCycle
	fallback := s.VisualFallback == nil || *s.VisualFallback
	g := &readyGuard{v: v, window: w, spec: copySpec, settle: settle, report: readinessReport{Mode: "ax_ready", Label: s.Label, Role: s.Role, RequireCycle: cycle}}
	raw, err := v.readControls(ctx, w, copySpec.MaxNodes)
	g.report.Checks++
	var ready, present bool
	if err == nil {
		ready, present, err = controlReady(raw, copySpec)
		if err == nil && !present {
			err = fmt.Errorf("%w: initial control not found", errControlsUnavailable)
		}
	}
	if err != nil {
		if fallback && errors.Is(err, errControlsUnavailable) {
			g.report.Mode = "visual_fallback"
			g.report.Reason = err.Error()
			return g, nil
		}
		return nil, err
	}
	if cycle && !ready {
		return nil, fmt.Errorf("ready_when control is not ready before input; wait without replaying input")
	}
	return g, nil
}
func (g *readyGuard) observe(ctx context.Context) (bool, error) {
	raw, err := g.v.readControls(ctx, g.window, g.spec.MaxNodes)
	g.report.Checks++
	if err != nil {
		return false, err
	}
	ready, _, err := controlReady(raw, g.spec)
	if err != nil {
		return false, err
	}
	if !ready {
		g.report.BusyObserved = true
		g.since = time.Time{}
		return false, nil
	}
	if g.report.RequireCycle && !g.report.BusyObserved {
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

type ControlsArgs struct {
	WindowID string `json:"window_id"`
	MaxNodes int    `json:"max_nodes,omitempty"`
	Compact  *bool  `json:"compact,omitempty" jsonschema:"Default true omits bounds; labels, roles and enabled state remain"`
}

func (v *Service) registerControls(s *mcp.Server) {
	bind(s, v, "ui_controls", "Read bounded native AX control labels/roles/enabled states in one confirmed window, without images or field values. Use exact labels to configure desktop_batch ready_when. Missing/ambiguous/truncated/unknown-enabled controls do not prove readiness. Does not replace fillable field snapshots.", true, func(ctx context.Context, a ControlsArgs) (*mcp.CallToolResult, error) {
		if a.MaxNodes == 0 {
			a.MaxNodes = 1000
		}
		if a.MaxNodes < 1 || a.MaxNodes > 2000 {
			return nil, fmt.Errorf("max_nodes must be 1..2000")
		}
		w, e := v.attentionWindow(ctx, a.WindowID)
		if e != nil {
			return nil, e
		}
		// bind's read lane is already held; release/reacquire is unnecessary here.
		reader, ok := v.Desktop.(platform.ControlReader)
		if !ok {
			return nil, errControlsUnavailable
		}
		raw, e := reader.ReadControls(ctx, w, a.MaxNodes)
		if e != nil {
			return nil, e
		}
		after, e := v.attentionWindow(ctx, w.ID)
		if e != nil {
			return nil, e
		}
		if after.Process != w.Process || after.Bounds != w.Bounds {
			return nil, fmt.Errorf("window changed during control read")
		}
		compact := a.Compact == nil || *a.Compact
		controls := make([]map[string]any, 0, len(raw.Controls))
		for _, control := range raw.Controls {
			item := map[string]any{"label": control.Label, "role": control.Role, "enabled": control.Enabled}
			if !compact {
				item["bounds"] = control.Bounds
			}
			controls = append(controls, item)
		}
		return result(map[string]any{"window_id": w.ID, "controls": controls, "truncated": raw.Truncated, "visited": raw.Visited, "content_trust": "untrusted UI metadata"}), nil
	})
}
