package mcpserver

import (
	"context"
	"encoding/json"
	"example.com/codex-vision/internal/input"
	"example.com/codex-vision/internal/platform"
	"example.com/codex-vision/internal/vision"
	"fmt"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"image"
	"time"
)

func fail(err error) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}
}
func result(v any) *mcp.CallToolResult {
	b, _ := json.Marshal(v)
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}, StructuredContent: v}
}

// Reads are serialized and rate bounded too: a client cannot queue unlimited GDI captures.
func (s *Service) read(ctx context.Context, fn func(context.Context) (*mcp.CallToolResult, error)) (*mcp.CallToolResult, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(s.Config.TimeoutMS)*time.Millisecond)
	defer cancel()
	select {
	case s.reads <- struct{}{}:
		defer func() { <-s.reads }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if err := s.chargeRead(); err != nil {
		return nil, err
	}
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	r, e := fn(ctx)
	if e == nil {
		e = ctx.Err()
	}
	return r, e
}
func (s *Service) capture(ctx context.Context, r platform.Rect, monitor, format string, quality, maxWidth int) (*mcp.CallToolResult, error) {
	if !s.Config.Images {
		return nil, fmt.Errorf("images disabled locally: enable images only with a client capable of consuming MCP image content; display cannot be detected by this server")
	}
	if format != "" && format != "png" && format != "jpeg" {
		return nil, fmt.Errorf("format must be png or jpeg")
	}
	if quality < 0 || quality > 100 || maxWidth < 0 || maxWidth > 16384 {
		return nil, fmt.Errorf("invalid image quality or max_width")
	}
	if !r.Valid() {
		return nil, fmt.Errorf("invalid capture bounds")
	}
	if e := vision.Validate(image.Rect(0, 0, r.Width, r.Height)); e != nil {
		return nil, e
	}
	im, e := s.Desktop.Capture(ctx, r)
	if e != nil {
		return nil, e
	}
	cursor := map[string]any{"available": false, "included": false}
	if provider, ok := s.Desktop.(platform.CursorReporter); ok {
		if point, err := provider.CursorPosition(ctx); err == nil {
			included := r.Contains(point.X, point.Y)
			cursor = map[string]any{"available": true, "included": included, "desktop_x": point.X, "desktop_y": point.Y, "rendering": "position marker; shape is not the system cursor", "sampled_at": time.Now().UTC().Format(time.RFC3339Nano)}
			if included {
				im = vision.WithCursorMarker(im, point.X-r.X, point.Y-r.Y)
			}
		}
	}
	data, mime, w, h, e := vision.Encode(ctx, im, format, quality, maxWidth)
	if e != nil {
		return nil, e
	}
	meta := map[string]any{"source_bounds": r, "monitor": monitor, "width": w, "height": h, "coordinate_space": platform.CoordinateSpace(s.Desktop), "desktop_x_per_image_pixel": float64(r.Width) / float64(w), "desktop_y_per_image_pixel": float64(r.Height) / float64(h), "backend": s.Desktop.Name(), "client_display_verified": false, "content_trust": "untrusted screen data"}
	meta["cursor"] = cursor
	if cursor["available"] == true {
		cursor["image_x"] = float64(cursor["desktop_x"].(int)-r.X) * float64(w) / float64(r.Width)
		cursor["image_y"] = float64(cursor["desktop_y"].(int)-r.Y) * float64(h) / float64(r.Height)
	}
	out := result(meta)
	out.Content = append(out.Content, &mcp.ImageContent{Data: data, MIMEType: mime})
	return out, nil
}
func (s *Service) action(ctx context.Context, a platform.Action) (*mcp.CallToolResult, error) {
	started := time.Now()
	if s.Desktop.Name() == "macos-quartz" {
		a.Metrics = &platform.InputMetrics{}
	}
	if a.Tool == "mouse_move" || a.Tool == "mouse_click" || a.Tool == "mouse_drag" {
		duration, err := input.ApproachDuration(a.Motion, s.Config.CursorMode)
		if err != nil {
			return nil, err
		}
		a.ApproachDuration = duration
	}
	ms, e := s.Desktop.Monitors(ctx)
	if e != nil {
		return nil, e
	}
	b, e := platform.Bounds(ms)
	if e != nil {
		return nil, e
	}
	if e = input.Validate(a, b); e != nil {
		return nil, e
	}
	if a.Tool == "mouse_move" || a.Tool == "mouse_click" || a.Tool == "mouse_drag" {
		onMonitor := func(x, y int) bool {
			for _, m := range ms {
				if m.Bounds.Contains(x, y) {
					return true
				}
			}
			return false
		}
		if !onMonitor(a.X, a.Y) || (a.Tool == "mouse_drag" && !onMonitor(a.EndX, a.EndY)) {
			return nil, fmt.Errorf("point falls in a gap between monitors")
		}
	}
	if e = s.Session.Execute(ctx, a); e != nil {
		return nil, e
	}
	response := map[string]any{"ok": true, "backend": s.Desktop.Name(), "local_execution_ms": time.Since(started).Milliseconds()}
	if a.Metrics != nil && a.Metrics.PointerMeasured {
		response["cursor_path_ms"] = a.Metrics.PointerTime.Milliseconds()
	}
	if a.ApproachDuration > 0 {
		duration := a.ApproachDuration
		if a.Tool == "mouse_move" && a.Duration > 0 {
			duration = a.Duration
		}
		response["approach_duration_ms"] = duration.Milliseconds()
		response["approach_applied_by_backend"] = s.Desktop.Name() == "macos-quartz"
	}
	if provider, ok := s.Desktop.(platform.CursorReporter); ok && (a.Tool == "mouse_move" || a.Tool == "mouse_click" || a.Tool == "mouse_drag") {
		if point, err := provider.CursorPosition(ctx); err == nil {
			response["cursor_position"] = point
		}
	}
	return result(response), nil
}

// chargeRead accounts for MCP pulls without holding the capture semaphore.
func (s *Service) chargeRead() error {
	s.mu.Lock()
	now := time.Now()
	i := 0
	for i < len(s.recent) && !s.recent[i].After(now.Add(-time.Minute)) {
		i++
	}
	s.recent = s.recent[i:]
	if s.Config.MaxPerTask > 0 && s.total >= s.Config.MaxPerTask {
		s.mu.Unlock()
		return fmt.Errorf("read task budget exhausted; local restart required")
	}
	if s.Config.MaxPerMinute > 0 && len(s.recent) >= s.Config.MaxPerMinute {
		s.mu.Unlock()
		return fmt.Errorf("read rate exceeded; retry after one minute")
	}
	if s.Config.MaxPerMinute > 0 {
		s.recent = append(s.recent, now)
	}
	s.total++
	s.mu.Unlock()
	return nil
}
