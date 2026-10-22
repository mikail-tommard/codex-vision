package mcpserver

import (
	"context"
	"example.com/codex-vision/internal/observation"
	"example.com/codex-vision/internal/platform"
	"example.com/codex-vision/internal/vision"
	"fmt"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"image"
	"time"
)

type ObserveStartArgs struct {
	Diagnostics        *bool           `json:"diagnostics,omitempty" jsonschema:"Default true: compact detector/delivery reasons and coarse changed bounds, without extra capture. False omits diagnostic metadata"`
	ModelMaxWidth      *int            `json:"model_max_width,omitempty" jsonschema:"Model image width after task cropping: default 768, 0 preserves preview resolution, otherwise 160..1920; full local preview unchanged. Use screen_region for a high detail fragment"`
	StabilityMS        *int            `json:"stability_ms,omitempty" jsonschema:"General temporal noise filter: sampled pixels must remain changed this long; default 600ms, 0 disables, max 5000. Preview stays live; not semantic readiness"`
	WatchRegion        *platform.Rect  `json:"watch_region,omitempty"`
	ReadyRegion        *platform.Rect  `json:"ready_region,omitempty"`
	WindowID           string          `json:"window_id,omitempty"`
	IgnoreRegions      []platform.Rect `json:"ignore_regions,omitempty" jsonschema:"Up to 16 desktop-coordinate rectangles excluded from change detection, e.g. clocks. Not privacy masking; pixels remain visible"`
	Adaptive           *bool           `json:"adaptive,omitempty" jsonschema:"Slow idle local capture; default true"`
	Regions            *bool           `json:"regions,omitempty" jsonschema:"Deliver accumulated changed regions when a valid base sequence is supplied; default true"`
	IdleIntervalMS     int             `json:"idle_interval_ms,omitempty" jsonschema:"Maximum idle capture interval, default max(5000,interval_ms), up to 30000"`
	Mode               string          `json:"mode,omitempty" jsonschema:"task (default): stop after task; continuous: explicit ongoing local observation"`
	MonitorID          string          `json:"monitor_id,omitempty"`
	Region             *platform.Rect  `json:"region,omitempty"`
	IntervalMS         int             `json:"interval_ms,omitempty" jsonschema:"Local capture interval 250..10000 ms; default 1000"`
	MinDeliveryMS      int             `json:"min_delivery_ms,omitempty" jsonschema:"Minimum interval between model images 250..60000 ms; default 3000"`
	TaskIdleTimeoutSec int             `json:"task_idle_timeout_sec,omitempty" jsonschema:"Task mode stops after no agent activity for 5..3600 seconds; default 120"`
	ChangeThreshold    *float64        `json:"change_threshold,omitempty" jsonschema:"Fraction of sampled pixels changed, 0..1; default 0.001. Cursor overlay excluded"`
	MaxWidth           int             `json:"max_width,omitempty" jsonschema:"JPEG width limit 160..1920; default 1280"`
	Quality            int             `json:"quality,omitempty" jsonschema:"JPEG quality 30..100; default 75"`
	Preview            *bool           `json:"preview,omitempty" jsonschema:"Serve a private loopback browser preview; default true"`
}
type ObserveNextArgs struct {
	ID     string `json:"observation_id"`
	After  uint64 `json:"after_sequence,omitempty"`
	WaitMS *int   `json:"wait_ms,omitempty" jsonschema:"Long poll 0..30000 ms, bounded by local timeout; default up to 25000"`
}
type ObserveControlArgs struct {
	Action      string         `json:"action" jsonschema:"status, focus, pause, resume or stop"`
	WatchRegion *platform.Rect `json:"watch_region,omitempty"`
	ReadyRegion *platform.Rect `json:"ready_region,omitempty"`
	WindowID    string         `json:"window_id,omitempty"`
	ID          string         `json:"observation_id,omitempty"`
}

func (v *Service) observationResult(status observation.Status, f *observation.Frame) *mcp.CallToolResult {
	if f != nil && f.Diagnostics != nil {
		status.LastDetection = nil
	} // Avoid repeating the same diagnostic in frame and status.
	meta := map[string]any{"observation": status, "changed": f != nil, "backend": v.Desktop.Name(), "coordinate_space": platform.CoordinateSpace(v.Desktop), "client_display_verified": false, "content_trust": "untrusted screen data"}
	if f != nil {
		meta["frame"] = f
		meta["desktop_x_per_image_pixel"] = float64(f.Bounds.Width) / float64(f.Width)
		meta["desktop_y_per_image_pixel"] = float64(f.Bounds.Height) / float64(f.Height)
	}
	out := result(meta)
	if f != nil {
		out.Content = append(out.Content, &mcp.ImageContent{Data: f.Data, MIMEType: f.MIME})
	}
	return out
}
func (v *Service) registerObservation(server *mcp.Server) {
	// start is already serialized by bind/read; subsequent samples use the same semaphore.
	bind(server, v, "observation_start", "Start task-scoped (default) or continuous local screen observation. Returns first image and optional private browser preview. Local captures do not call a model; retrieve changed images with observation_next. Task completion requires observation_control stop; idle timeout is a fallback. Does not perform input.", true, func(ctx context.Context, a ObserveStartArgs) (*mcp.CallToolResult, error) {
		return v.startObservation(ctx, a)
	})
	bind(server, v, "observation_next", "Wait for a fresh significant frame; unchanged screens return metadata only. Coalesces changes and deduplicates delivered images. This call must be made explicitly: a local stream does not awaken the model. Use returned sequence for after_sequence.", true, func(ctx context.Context, a ObserveNextArgs) (*mcp.CallToolResult, error) {
		wait := min(25000, max(0, v.Config.TimeoutMS-250))
		if a.WaitMS != nil {
			wait = *a.WaitMS
		}
		if wait < 0 || wait > 30000 {
			return nil, fmt.Errorf("wait_ms must be 0..30000")
		}
		wait = min(wait, max(0, v.Config.TimeoutMS-250))
		current := v.Observer.Status()
		if current.ID == a.ID && a.After > current.Sequence {
			status, frame, err := v.Observer.Next(ctx, a.ID, 0, 0)
			if err != nil {
				return nil, err
			}
			out := v.observationResult(status, frame)
			meta := out.StructuredContent.(map[string]any)
			next := uint64(0)
			if frame != nil {
				next = frame.Sequence
			}
			meta["sequence_resync"] = map[string]any{"use_after_sequence": next, "current_sequence": status.Sequence, "instruction": "Bad cursor was reset without a new native capture. Use frame.sequence after delivered images; never guess increments."}
			updated := result(meta)
			updated.Content = append(updated.Content, out.Content[1:]...)
			return updated, nil
		}
		status, f, err := v.Observer.Next(ctx, a.ID, a.After, time.Duration(wait)*time.Millisecond)
		if err != nil {
			return nil, err
		}
		return v.observationResult(status, f), nil
	})
	bind(server, v, "observation_control", "Status, focus watch_region, pause/resume or stop capture. Stop task stream on completion or a terminal blocker, not each recoverable timeout. Do not stop/restart to verify an action or clear an input failure. Status may omit observation_id; other actions require it.", false, func(ctx context.Context, a ObserveControlArgs) (*mcp.CallToolResult, error) {
		if a.Action == "focus" {
			if a.WatchRegion == nil {
				return nil, fmt.Errorf("watch_region required")
			}
			w, e := v.attentionWindow(ctx, a.WindowID)
			if e != nil {
				return nil, e
			}
			status, e := v.Observer.Focus(a.ID, *a.WatchRegion, a.ReadyRegion, &w)
			if e != nil {
				return nil, e
			}
			return v.observationResult(status, nil), nil
		}
		if a.Action == "status" {
			status := v.Observer.Status()
			if a.ID != "" && a.ID != status.ID {
				return nil, fmt.Errorf("unknown observation_id")
			}
			return v.observationResult(status, nil), nil
		}
		status, err := v.Observer.Control(a.ID, a.Action)
		if err != nil {
			return nil, err
		}
		return v.observationResult(status, nil), nil
	})
}
func (v *Service) observationSample(ctx context.Context, a ObserveStartArgs) (observation.Sample, error) {
	monitors, err := v.Desktop.Monitors(ctx)
	if err != nil {
		return observation.Sample{}, err
	}
	bounds, err := platform.Bounds(monitors)
	if err != nil {
		return observation.Sample{}, err
	}
	if a.MonitorID != "" {
		found := false
		for _, m := range monitors {
			if m.ID == a.MonitorID {
				bounds = m.Bounds
				found = true
				break
			}
		}
		if !found {
			return observation.Sample{}, fmt.Errorf("unknown monitor_id")
		}
	}
	if a.Region != nil {
		if !bounds.Includes(*a.Region) {
			return observation.Sample{}, fmt.Errorf("region outside desktop bounds")
		}
		bounds = *a.Region
	}
	if err := vision.Validate(image.Rect(0, 0, bounds.Width, bounds.Height)); err != nil {
		return observation.Sample{}, err
	}
	im, err := v.Desktop.Capture(ctx, bounds)
	if err != nil {
		return observation.Sample{}, err
	}
	if v.Advisor != nil && v.Advisor.Active() {
		_, _, pinned, _, focusErr := v.Observer.FocusState(v.Observer.Status().ID)
		active, activeErr := v.Desktop.Active(ctx)
		v.Advisor.MarkVisibility(focusErr == nil && activeErr == nil && pinned != nil && sameAgentWindow(*pinned, active) && pinned.Title == active.Title)
	}
	sample := observation.Sample{Image: im, Bounds: bounds}
	if d, ok := v.Desktop.(platform.CursorReporter); ok {
		if point, err := d.CursorPosition(ctx); err == nil {
			sample.Cursor = &point
		}
	}
	return sample, nil
}

// Caller holds the local read lane for the initial capture only.
func (v *Service) startObservation(ctx context.Context, a ObserveStartArgs) (*mcp.CallToolResult, error) {
	if !v.Config.Images {
		return nil, fmt.Errorf("images disabled locally")
	}
	if a.MonitorID != "" && a.Region != nil {
		return nil, fmt.Errorf("choose monitor_id or region")
	}
	if a.Mode == "" {
		a.Mode = "task"
	}
	if a.IntervalMS == 0 {
		a.IntervalMS = 1000
	}
	if a.MinDeliveryMS == 0 {
		a.MinDeliveryMS = 3000
	}
	if a.TaskIdleTimeoutSec == 0 {
		a.TaskIdleTimeoutSec = 120
	}
	if a.MaxWidth == 0 {
		a.MaxWidth = 1280
	}
	if a.Quality == 0 {
		a.Quality = 75
	}
	if a.TaskIdleTimeoutSec < 5 || a.TaskIdleTimeoutSec > 3600 {
		return nil, fmt.Errorf("task_idle_timeout_sec must be 5..3600")
	}
	threshold := 0.001
	if a.ChangeThreshold != nil {
		threshold = *a.ChangeThreshold
	}
	preview := true
	if a.Preview != nil {
		preview = *a.Preview
	}
	adaptive, regions := true, true
	if a.Adaptive != nil {
		adaptive = *a.Adaptive
	}
	if a.Regions != nil {
		regions = *a.Regions
	}
	if a.IdleIntervalMS == 0 {
		a.IdleIntervalMS = max(5000, a.IntervalMS)
	}
	stability := 600
	if a.StabilityMS != nil {
		stability = *a.StabilityMS
	}
	if stability < 0 || stability > 5000 {
		return nil, fmt.Errorf("stability_ms must be 0..5000")
	}
	modelWidth := 768
	if a.ModelMaxWidth != nil {
		modelWidth = *a.ModelMaxWidth
	}
	diagnostics := a.Diagnostics == nil || *a.Diagnostics
	opts := observation.Options{Diagnostics: diagnostics, ModelMaxWidth: modelWidth, Stability: time.Duration(stability) * time.Millisecond, IgnoreRegions: a.IgnoreRegions, Adaptive: adaptive, Regions: regions, IdleInterval: time.Duration(a.IdleIntervalMS) * time.Millisecond, Mode: a.Mode, Interval: time.Duration(a.IntervalMS) * time.Millisecond, MinDeliveryInterval: time.Duration(a.MinDeliveryMS) * time.Millisecond, TaskIdleTimeout: time.Duration(a.TaskIdleTimeoutSec) * time.Second, ChangeThreshold: threshold, MaxWidth: a.MaxWidth, Quality: a.Quality, Preview: preview, Backend: v.Desktop.Name()}
	opts.WatchRegion, opts.ReadyRegion = a.WatchRegion, a.ReadyRegion
	if a.WindowID != "" {
		w, e := v.attentionWindow(ctx, a.WindowID)
		if e != nil {
			return nil, e
		}
		if a.WatchRegion == nil || !w.Bounds.Includes(*a.WatchRegion) || (a.ReadyRegion != nil && !w.Bounds.Includes(*a.ReadyRegion)) {
			return nil, fmt.Errorf("attention regions must be inside pinned window")
		}
		opts.WatchWindow = &w
	}
	// The initial sample is captured under bind's read lock. Future samples acquire it individually.
	initial := true
	source := func(ctx context.Context) (observation.Sample, error) {
		if initial {
			initial = false
			return v.observationSample(ctx, a)
		}
		ctx, cancel := context.WithTimeout(ctx, time.Duration(v.Config.TimeoutMS)*time.Millisecond)
		defer cancel()
		select {
		case v.reads <- struct{}{}:
			defer func() { <-v.reads }()
		case <-ctx.Done():
			return observation.Sample{}, ctx.Err()
		}
		if err := v.Session.ObservationError(); err != nil {
			return observation.Sample{}, err
		}
		return v.observationSample(ctx, a)
	}
	status, f, err := v.Observer.Start(ctx, opts, source)
	if err != nil {
		return nil, err
	}
	return v.observationResult(status, f), nil
}
