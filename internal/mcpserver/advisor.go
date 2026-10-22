package mcpserver

import (
	"context"
	"fmt"
	"time"

	"example.com/codex-vision/internal/advisor"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type AdvisorArgs struct {
	Action         string `json:"action" jsonschema:"start, publish, status, pause, resume or stop. Advice only; active advisor blocks desktop input"`
	ViewID         string `json:"view_id,omitempty" jsonschema:"start/publish: fresh desktop_observe view with delivered image, scoped to the game; keep panel outside watch_region"`
	Title          string `json:"title,omitempty" jsonschema:"start: panel title, max 80 characters"`
	Recommendation string `json:"recommendation,omitempty" jsonschema:"publish: concise suggested move, 1..240 characters"`
	Reason         string `json:"reason,omitempty" jsonschema:"publish: brief reason, 1..600 characters; never claim certainty without evidence"`
	Alternative    string `json:"alternative,omitempty" jsonschema:"publish: optional fallback, max 300 characters"`
	Urgency        string `json:"urgency,omitempty" jsonschema:"normal (default), important or critical; critical may notify after user opt-in"`
	TTLSeconds     int    `json:"ttl_seconds,omitempty" jsonschema:"3..120 seconds, omitted/0 defaults to 20; scene change invalidates sooner"`
}

func (v *Service) advisorSource() advisor.Source {
	s := v.Observer.Status()
	source := advisor.Source{ObservationID: s.ID, Sequence: s.Sequence, FocusVersion: s.FocusVersion, State: s.State, WindowID: s.WatchWindowID}
	if s.WatchRegion != nil {
		source.Region = *s.WatchRegion
	}
	return source
}

func (v *Service) advisorView(ctx context.Context, id string) (advisor.Source, error) {
	v.views.mu.Lock()
	view := v.views.current
	if view == nil || view.ID != id || time.Now().After(view.Expires) {
		v.views.mu.Unlock()
		return advisor.Source{}, fmt.Errorf("fresh desktop_observe view required for advisor")
	}
	copy := *view
	v.views.mu.Unlock()
	source := v.advisorSource()
	if source.State != "running" || copy.ImageSequence == 0 || source.Sequence != copy.ImageSequence || source.ObservationID != copy.ObservationID || source.FocusVersion != copy.FocusVersion || source.WindowID != copy.Window.ID || source.Region != copy.Region {
		return advisor.Source{}, fmt.Errorf("stale advice evidence; observe current game region")
	}
	w, err := v.agentWindow(ctx, copy.Window.ID)
	if err != nil {
		return advisor.Source{}, err
	}
	if !sameAgentWindow(w, copy.Window) || w.Title != copy.Window.Title {
		return advisor.Source{}, fmt.Errorf("advice window changed; observe again")
	}
	return source, nil
}

func (v *Service) registerAdvisor(s *mcp.Server) {
	bind(s, v, "advisor", "Local on-screen coach: start from a scoped image/view, then publish recommendation/reason/alternative from fresh evidence. Returns a panel URL; user opens floating window/notifications. Scene changes expire advice. Never calls a model itself; continue the observation/publish loop. Active coach blocks desktop input until stop; stop leaves observation/session usable.", false, func(ctx context.Context, a AdvisorArgs) (*mcp.CallToolResult, error) {
		switch a.Action {
		case "start":
			source, err := v.advisorView(ctx, a.ViewID)
			if err != nil {
				return nil, err
			}
			if a.Title == "" {
				a.Title = "Игровой советник"
			}
			url, err := v.Advisor.Start("", a.Title, source)
			if err != nil {
				return nil, err
			}
			return result(map[string]any{"advisor_url": url, "input_blocked": true, "next_action": "Open panel outside watch_region. Continue scoped observe -> advisor publish; local preview cannot awaken model."}), nil
		case "publish":
			source, err := v.advisorView(ctx, a.ViewID)
			if err != nil {
				return nil, err
			}
			err = v.Advisor.Publish(advisor.Advice{Recommendation: a.Recommendation, Reason: a.Reason, Alternative: a.Alternative, Urgency: a.Urgency, TTLSeconds: a.TTLSeconds, Source: source})
			if err != nil {
				return nil, err
			}
			state := v.Advisor.Snapshot()
			var revision uint64
			if state.Advice != nil {
				revision = state.Advice.Revision
			}
			return result(map[string]any{"published": true, "revision": revision, "stale": state.Stale, "input_blocked": true}), nil
		case "status":
			state := v.Advisor.Snapshot()
			return result(map[string]any{"active": state.Active, "paused": state.Paused, "status": state.Status, "stale": state.Stale, "advisor_url": v.Advisor.URL(), "input_blocked": state.Active}), nil
		case "pause", "resume":
			if err := v.Advisor.Pause(a.Action == "pause"); err != nil {
				return nil, err
			}
			return result(map[string]any{"paused": a.Action == "pause", "input_blocked": true}), nil
		case "stop":
			v.Advisor.Close()
			return result(map[string]any{"active": false, "observation_stopped": false, "session_stopped": false}), nil
		default:
			return nil, fmt.Errorf("advisor action must be start, publish, status, pause, resume or stop")
		}
	})
}
