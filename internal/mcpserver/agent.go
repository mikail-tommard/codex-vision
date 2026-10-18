package mcpserver

import (
	"context"
	"fmt"
	"sync"
	"time"

	"example.com/codex-vision/internal/observation"
	"example.com/codex-vision/internal/platform"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type agentView struct {
	ID                      string
	Window                  platform.Window
	ObservationID           string
	FocusVersion            uint64
	Region                  platform.Rect
	Expires                 time.Time
	Controls                map[string]platform.UIControl
	MaxNodes                int
	IncludeControls         bool
	FocusAttempted          bool
	ImageBounds             platform.Rect
	ImageWidth, ImageHeight int
}
type agentViews struct {
	mu      sync.Mutex
	current *agentView
}

type DesktopObserveArgs struct {
	Mode            string         `json:"mode,omitempty" jsonschema:"New observation: task (default) or continuous only on explicit user request. Existing stream mode cannot be changed here"`
	IncludeControls *bool          `json:"include_controls,omitempty" jsonschema:"Read and return UI controls: default false in compact, true in full. Enable for target_id or discovering UI predicates; unnecessary for visual keyboard tasks"`
	WindowID        string         `json:"window_id,omitempty" jsonschema:"Default current foreground window; never activates or switches applications"`
	ID              string         `json:"observation_id,omitempty"`
	After           uint64         `json:"after_sequence,omitempty"`
	Region          *platform.Rect `json:"watch_region,omitempty"`
	WaitMS          *int           `json:"wait_ms,omitempty" jsonschema:"0..30000, default 0: unchanged result has no image; use bounded long polling when awaiting a new state"`
	IntervalMS      int            `json:"interval_ms,omitempty" jsonschema:"New task capture interval 250..10000ms; default 1000; ignored for an existing stream"`
	MinDeliveryMS   int            `json:"min_delivery_ms,omitempty" jsonschema:"New task delivery interval 250..60000ms; default 3000"`
	Preview         bool           `json:"preview,omitempty" jsonschema:"Default false; used only when starting a new task observation"`
	IncludeFields   bool           `json:"include_fields,omitempty" jsonschema:"Request fresh text-field IDs only for form tasks; values omitted"`
	Labels          []string       `json:"labels,omitempty"`
	MaxNodes        int            `json:"max_nodes,omitempty" jsonschema:"AX traversal bound 1..1000, default 500"`
}
type DesktopActArgs struct {
	WatchRegion    *platform.Rect `json:"watch_region,omitempty" jsonschema:"Optional task region chosen from this view; must fit both the pinned window and existing capture. Changes attention in the same action call"`
	ViewID         string         `json:"view_id"`
	Steps          []DesktopStep  `json:"steps"`
	Coordinates    string         `json:"coordinates,omitempty" jsonschema:"desktop (default) or image: x/y relative to the image returned in this view; target_id ignores this option"`
	Refocus        *bool          `json:"refocus,omitempty" jsonschema:"Default true: at most one policy-authorized preflight focus when a different window is active; never retries native focus failure or batch input"`
	Expect         *ExpectedState `json:"expect,omitempty"`
	ReadyWhen      *ReadyWhen     `json:"ready_when,omitempty"`
	WaitFor        string         `json:"wait_for,omitempty" jsonschema:"change (default): own stable result, including 2048. response: two visual phases only when awaiting a separate response; not proof of an opponent turn"`
	ResultMode     string         `json:"result_mode,omitempty"`
	Motion         string         `json:"motion,omitempty"`
	SettleMS       int            `json:"settle_ms,omitempty"`
	WaitMS         int            `json:"wait_ms,omitempty"`
	ImageOnTimeout bool           `json:"image_on_timeout,omitempty"`
}

func (v *Service) registerAgent(s *mcp.Server) {
	bind(s, v, "desktop_observe", "Confirm active window; return one fresh scoped image and view_id. Optional include_controls/fields provide UI references. Starts observation if needed (task default, continuous by explicit request); preserves existing stream. Unchanged state omits image. UI metadata is non-atomic, not input authorization.", true, v.desktopObserve)
	bind(s, v, "desktop_act", "Act once from a fresh desktop_observe view. Resolve target_id by a unique fresh structural control match, or convert explicit image coordinates. Pin window/process/geometry and consume the view before the batch; at most one authorized preflight focus. Use expect for a native UI postcondition, result_mode=image for visual decisions, then inspect the returned result. No action replay or automatic recovery after partial input.", false, v.desktopAct)
}

func sameAgentWindow(a, b platform.Window) bool {
	return a.ID == b.ID && a.Process == b.Process && a.Bounds == b.Bounds
}

func (v *Service) agentWindow(ctx context.Context, id string) (w platform.Window, err error) {
	err = v.withLocalRead(ctx, func() error {
		var e error
		w, e = v.Desktop.Active(ctx)
		if e == nil && id != "" && w.ID != id {
			e = fmt.Errorf("expected foreground window %s; stop input", id)
		}
		return e
	})
	return
}

func (v *Service) desktopObserve(ctx context.Context, a DesktopObserveArgs) (*mcp.CallToolResult, error) {
	if a.Mode != "" && a.Mode != "task" && a.Mode != "continuous" {
		return nil, fmt.Errorf("mode must be task or continuous")
	}
	if a.WaitMS != nil && (*a.WaitMS < 0 || *a.WaitMS > 30000) {
		return nil, fmt.Errorf("wait_ms must be 0..30000")
	}
	if a.IntervalMS != 0 && (a.IntervalMS < 250 || a.IntervalMS > 10000) {
		return nil, fmt.Errorf("interval_ms must be 250..10000")
	}
	if a.MinDeliveryMS != 0 && (a.MinDeliveryMS < 250 || a.MinDeliveryMS > 60000) {
		return nil, fmt.Errorf("min_delivery_ms must be 250..60000")
	}
	if a.MaxNodes == 0 {
		a.MaxNodes = 500
	}
	if a.MaxNodes < 1 || a.MaxNodes > 1000 {
		return nil, fmt.Errorf("max_nodes must be 1..1000")
	}
	if err := validateLabels(a.Labels, ""); err != nil {
		return nil, err
	}
	if len(a.Labels) > 0 && !a.IncludeFields {
		return nil, fmt.Errorf("labels requires include_fields=true")
	}
	w, err := v.agentWindow(ctx, a.WindowID)
	if err != nil {
		return nil, err
	}
	region := w.Bounds
	var readyRegion *platform.Rect
	status := v.Observer.Status()
	if a.ID != "" && a.ID != status.ID {
		return nil, fmt.Errorf("unknown observation_id; do not restart an existing stream blindly")
	}
	if status.State == "running" || status.State == "paused" {
		if a.Mode != "" && a.Mode != status.Mode {
			return nil, fmt.Errorf("existing observation has mode %s; stop it explicitly before changing mode", status.Mode)
		}
		if status.State == "paused" {
			return nil, fmt.Errorf("observation is paused; resume explicitly")
		}
		scope, marker, pinned, _, e := v.Observer.FocusState(status.ID)
		if e != nil {
			return nil, e
		}
		if a.Region == nil && scope != nil && pinned != nil && sameAgentWindow(*pinned, w) {
			region = *scope
			readyRegion = marker
		}
	}
	if a.Region != nil {
		region = *a.Region
	}
	if !w.Bounds.Includes(region) {
		return nil, fmt.Errorf("watch_region outside foreground window")
	}
	if a.Region == nil {
		b := status.CaptureBounds
		if status.State != "running" {
			var monitors []platform.Monitor
			err = v.withLocalRead(ctx, func() error { var e error; monitors, e = v.Desktop.Monitors(ctx); return e })
			if err != nil {
				return nil, err
			}
			b, err = platform.Bounds(monitors)
			if err != nil {
				return nil, err
			}
		}
		x, y := max(region.X, b.X), max(region.Y, b.Y)
		region = platform.Rect{X: x, Y: y, Width: min(region.X+region.Width, b.X+b.Width) - x, Height: min(region.Y+region.Height, b.Y+b.Height) - y}
		if !region.Valid() {
			return nil, fmt.Errorf("foreground window outside capture bounds")
		}
	}
	var out *mcp.CallToolResult
	if status.State != "running" {
		err = v.withLocalRead(ctx, func() error {
			var e error
			out, e = v.startObservation(ctx, ObserveStartArgs{Mode: a.Mode, WindowID: w.ID, WatchRegion: &region, Preview: &a.Preview, IntervalMS: a.IntervalMS, MinDeliveryMS: a.MinDeliveryMS})
			return e
		})
	} else {
		previousFocus := status.FocusVersion
		status, err = v.Observer.Focus(status.ID, region, readyRegion, &w)
		if err == nil {
			wait := 0
			if a.WaitMS != nil {
				wait = *a.WaitMS
			}
			if wait < 0 || wait > 30000 {
				return nil, fmt.Errorf("wait_ms must be 0..30000")
			}
			if status.FocusVersion != previousFocus && a.WaitMS == nil {
				wait = 1000
			}
			wait = min(wait, max(0, v.Config.TimeoutMS-250))
			var f *observation.Frame
			after := a.After
			if after > status.Sequence {
				after = 0
			}
			status, f, err = v.Observer.Next(ctx, status.ID, after, time.Duration(wait)*time.Millisecond)
			out = v.observationResult(status, f)
		}
	}
	if err != nil {
		return nil, err
	}
	includeControls := v.Config.ToolProfile != "compact"
	if a.IncludeControls != nil {
		includeControls = *a.IncludeControls
	}
	return v.attachAgentView(ctx, w, region, a.MaxNodes, includeControls, a.IncludeFields, a.Labels, out)
}

func (v *Service) attachAgentView(ctx context.Context, w platform.Window, region platform.Rect, maxNodes int, includeControls, includeFields bool, labels []string, out *mcp.CallToolResult) (*mcp.CallToolResult, error) {
	viewID, err := opaqueID()
	if err != nil {
		return nil, err
	}
	status := v.Observer.Status()
	view := &agentView{ID: viewID, Window: w, ObservationID: status.ID, FocusVersion: status.FocusVersion, Region: region, Expires: time.Now().Add(time.Minute), Controls: map[string]platform.UIControl{}, MaxNodes: maxNodes, IncludeControls: includeControls}
	meta := out.StructuredContent.(map[string]any)
	if f, ok := meta["frame"].(*observation.Frame); ok && f != nil {
		view.ImageBounds = f.Bounds
		view.ImageWidth = f.Width
		view.ImageHeight = f.Height
	}
	controls := []map[string]any{}
	if !includeControls {
		meta["controls_status"] = map[string]any{"available": false, "reason": "not requested; use include_controls=true to discover controls"}
	} else if _, supported := v.Desktop.(platform.ControlReader); supported {
		raw, e := v.readControls(ctx, w, maxNodes)
		if e != nil {
			meta["controls_status"] = map[string]any{"available": false, "error": e.Error(), "failure": classifyFailure(e)}
		} else {
			meta["controls_status"] = map[string]any{"available": true, "truncated": raw.Truncated, "omitted": max(0, len(raw.Controls)-64)}
			for _, c := range raw.Controls[:min(64, len(raw.Controls))] {
				item := map[string]any{"label": c.Label, "role": c.Role, "enabled": c.Enabled, "bounds": c.Bounds}
				if !raw.Truncated && c.Enabled != nil && *c.Enabled && c.Bounds.Valid() && w.Bounds.Includes(c.Bounds) {
					id, e := opaqueID()
					if e != nil {
						return nil, e
					}
					enabled := *c.Enabled
					c.Enabled = &enabled
					view.Controls[id] = c
					item["target_id"] = id
				}
				controls = append(controls, item)
			}
		}
	} else {
		meta["controls_status"] = map[string]any{"available": false, "reason": "unsupported backend; use visual coordinates"}
	}
	meta["controls"] = controls
	if includeFields {
		if reader, supported := v.Desktop.(platform.UIReader); supported {
			var fields map[string]any
			e := v.withLocalRead(ctx, func() error {
				raw, e := reader.ReadUI(ctx, w, maxNodes)
				if e != nil {
					return e
				}
				after, e := v.attentionWindow(ctx, w.ID)
				if e != nil {
					return e
				}
				if !sameAgentWindow(w, after) {
					return fmt.Errorf("window changed during field snapshot")
				}
				fields, e = v.presentUISnapshot(w, raw, "", maxNodes, true, labels)
				return e
			})
			if e != nil {
				meta["fields_status"] = map[string]any{"available": false, "error": e.Error(), "failure": classifyFailure(e)}
			} else {
				meta["fields"] = fields
			}
		} else {
			meta["fields_status"] = map[string]any{"available": false, "reason": "unsupported backend"}
		}
	}
	after, err := v.agentWindow(ctx, w.ID)
	if err != nil {
		return nil, err
	}
	if !sameAgentWindow(w, after) {
		return nil, fmt.Errorf("window changed during unified observation")
	}
	decorated, err := v.Session.DescribeWindows(ctx, []platform.Window{after})
	if err != nil {
		return nil, err
	}
	meta["window"] = decorated[0]
	meta["view"] = map[string]any{"view_id": view.ID, "expires_at": view.Expires.UTC(), "observation_id": view.ObservationID, "watch_region": view.Region, "capture_bounds": status.CaptureBounds, "image_coordinates_available": view.ImageWidth > 0, "atomic_ui_snapshot": false}
	// Keep the agent-facing result lean. Detailed cumulative counters remain in
	// observation_control status/session_status; don't repeat them each decision.
	if observed, ok := meta["observation"].(observation.Status); ok {
		summary := map[string]any{"id": observed.ID, "state": observed.State, "mode": observed.Mode, "sequence": observed.Sequence}
		if observed.PreviewURL != "" {
			summary["preview_url"] = observed.PreviewURL
		}
		meta["observation"] = summary
	}
	if verified, ok := meta["verification"].(verificationReport); ok && verified.Configured {
		delete(meta, "readiness")
	}
	v.views.mu.Lock()
	v.views.current = view
	v.views.mu.Unlock()
	return replaceMetadata(out, meta), nil
}

func replaceMetadata(out *mcp.CallToolResult, meta map[string]any) *mcp.CallToolResult {
	updated := result(meta)
	if len(out.Content) > 1 {
		updated.Content = append(updated.Content, out.Content[1:]...)
	}
	updated.IsError = out.IsError
	return updated
}

func (v *Service) desktopAct(ctx context.Context, a DesktopActArgs) (*mcp.CallToolResult, error) {
	if len(a.Steps) < 1 || len(a.Steps) > 12 {
		return nil, fmt.Errorf("steps must contain 1..12 actions")
	}
	if a.Coordinates == "" {
		a.Coordinates = "desktop"
	}
	if a.Coordinates != "desktop" && a.Coordinates != "image" {
		return nil, fmt.Errorf("coordinates must be desktop or image")
	}
	v.views.mu.Lock()
	view := v.views.current
	v.views.mu.Unlock()
	if view == nil || view.ID != a.ViewID || time.Now().After(view.Expires) {
		return nil, fmt.Errorf("stale, expired or consumed view; call desktop_observe")
	}
	state := v.Observer.Status()
	if state.State != "running" || state.ID != view.ObservationID || state.FocusVersion != view.FocusVersion {
		return nil, fmt.Errorf("stale view: observation or attention changed")
	}
	region := view.Region
	if a.WatchRegion != nil {
		if !view.Window.Bounds.Includes(*a.WatchRegion) || !state.CaptureBounds.Includes(*a.WatchRegion) {
			return nil, fmt.Errorf("watch_region outside pinned window or capture bounds")
		}
		region = *a.WatchRegion
	}
	active, err := v.agentWindow(ctx, "")
	if err != nil {
		return nil, err
	}
	refocused := false
	if active.ID != view.Window.ID {
		if a.Refocus != nil && !*a.Refocus {
			return nil, fmt.Errorf("expected foreground window %s; no input posted", view.Window.ID)
		}
		// One separately authorized focus before batch input, never an input retry.
		v.views.mu.Lock()
		if view.FocusAttempted {
			v.views.mu.Unlock()
			return nil, fmt.Errorf("foreground focus already attempted for this view; stop and wait for an external window change")
		}
		view.FocusAttempted = true
		v.views.mu.Unlock()
		refocused = true
		if _, err = v.action(ctx, platform.Action{Tool: "window_focus", WindowID: view.Window.ID}); err != nil {
			return nil, err
		}
		active, err = v.agentWindow(ctx, view.Window.ID)
		if err != nil {
			return nil, err
		}
	}
	if !sameAgentWindow(active, view.Window) {
		return nil, fmt.Errorf("stale view window geometry or process; call desktop_observe")
	}
	steps := append([]DesktopStep(nil), a.Steps...)
	refs := map[int]platform.UIControl{}
	var raw platform.ControlSnapshot
	needControls := false
	for _, s := range steps {
		if s.TargetID != "" {
			needControls = true
		}
	}
	if needControls {
		raw, err = v.readControls(ctx, active, view.MaxNodes)
		if err != nil {
			return nil, err
		}
		if raw.Truncated {
			return nil, fmt.Errorf("stale control reference: current tree incomplete")
		}
	}
	for i := range steps {
		s := &steps[i]
		if s.TargetID != "" {
			if (s.Action != "click" && s.Action != "move") || s.X != 0 || s.Y != 0 {
				return nil, fmt.Errorf("target_id requires click/move without x/y")
			}
			target, ok := view.Controls[s.TargetID]
			if !ok {
				return nil, fmt.Errorf("stale or unknown target_id")
			}
			matches := 0
			for _, c := range raw.Controls {
				if c.Role == target.Role && c.Label == target.Label && c.Bounds == target.Bounds && c.Enabled != nil && *c.Enabled {
					matches++
				}
			}
			if matches != 1 {
				return nil, fmt.Errorf("stale or ambiguous target: matched %d current controls", matches)
			}
			refs[i] = target
			s.X = target.Bounds.X + target.Bounds.Width/2
			s.Y = target.Bounds.Y + target.Bounds.Height/2
			s.TargetID = ""
		} else if a.Coordinates == "image" && (s.Action == "click" || s.Action == "move") {
			if view.ImageWidth == 0 || s.X < 0 || s.Y < 0 || s.X >= view.ImageWidth || s.Y >= view.ImageHeight {
				return nil, fmt.Errorf("image coordinates outside the image returned in this view")
			}
			s.X = view.ImageBounds.X + s.X*view.ImageBounds.Width/view.ImageWidth
			s.Y = view.ImageBounds.Y + s.Y*view.ImageBounds.Height/view.ImageHeight
		}
	}
	v.views.mu.Lock()
	if v.views.current != view || time.Now().After(view.Expires) {
		v.views.mu.Unlock()
		return nil, fmt.Errorf("stale or replaced view")
	}
	v.views.current = nil // Consume before any batch event, including partial failures.
	v.views.mu.Unlock()
	out, err := v.runDesktopBatch(ctx, DesktopBatchArgs{ID: view.ObservationID, WindowID: view.Window.ID, WatchRegion: &region, Steps: steps, Expect: a.Expect, ReadyWhen: a.ReadyWhen, WaitFor: a.WaitFor, ResultMode: a.ResultMode, Motion: a.Motion, SettleMS: a.SettleMS, WaitMS: a.WaitMS, ImageOnTimeout: a.ImageOnTimeout}, func(ctx context.Context, index int) error {
		target, ok := refs[index]
		if !ok {
			return nil
		}
		current, e := v.readControls(ctx, view.Window, view.MaxNodes)
		if e != nil {
			return e
		}
		if current.Truncated {
			return fmt.Errorf("stale control: current tree incomplete before step %d", index+1)
		}
		count := 0
		for _, c := range current.Controls {
			if c.Role == target.Role && c.Label == target.Label && c.Bounds == target.Bounds && c.Enabled != nil && *c.Enabled {
				count++
			}
		}
		if count != 1 {
			return fmt.Errorf("stale control before step %d: matched %d", index+1, count)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	meta := out.StructuredContent.(map[string]any)
	meta["view_consumed"] = true
	meta["preflight_refocus_attempted"] = refocused
	out = replaceMetadata(out, meta)
	interaction, _ := meta["interaction"].(observation.ActionWaitResult)
	if !out.IsError && interaction.Posted && !interaction.TimedOut {
		refreshed, e := v.attachAgentView(ctx, view.Window, region, view.MaxNodes, view.IncludeControls, false, nil, out)
		if e == nil {
			return refreshed, nil
		}
		// Input already posted: preserve its report, discard an unconfirmed image,
		// and never turn a result-observation failure into a replayable preflight.
		meta["error"] = e.Error()
		meta["failure"] = classifyFailure(e)
		delete(meta, "frame")
		meta["changed"] = false
		meta["continuation"] = actionContinuation(interaction, false, e)
		failed := result(meta)
		failed.IsError = true
		return failed, nil
	}
	return out, nil
}
