package mcpserver

import (
	"context"
	"fmt"
	"time"

	"example.com/codex-vision/internal/platform"
	"example.com/codex-vision/internal/vision"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func (v *Service) capturePortal(ctx context.Context, d platform.ConsentedScreenshot, a CaptureArgs) (*mcp.CallToolResult, error) {
	if !v.Config.Images {
		return nil, fmt.Errorf("images disabled locally")
	}
	if a.MonitorID != "" {
		return nil, fmt.Errorf("portal screenshot has no monitor_id; select the image in the system dialog")
	}
	if a.Format != "" && a.Format != "png" && a.Format != "jpeg" || a.Quality < 0 || a.Quality > 100 || a.MaxWidth < 0 || a.MaxWidth > 16384 {
		return nil, fmt.Errorf("invalid image encoding parameters")
	}
	if err := v.Session.ObservationError(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// Stop-file cancellation must also close a pending consent interaction.
	go func() {
		ticker := time.NewTicker(25 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if v.Session.ObservationError() != nil {
					cancel()
					return
				}
			}
		}
	}()
	started := time.Now()
	im, err := d.Screenshot(ctx)
	v.performance.record("capture", started)
	if err != nil {
		return nil, err
	}
	if err := v.Session.ObservationError(); err != nil {
		return nil, err
	}
	if a.Format == "" {
		a.Format = "jpeg"
	}
	if a.Quality == 0 {
		a.Quality = 75
	}
	if a.MaxWidth == 0 {
		a.MaxWidth = 768
	}
	data, mime, w, h, err := vision.Encode(ctx, im, a.Format, a.Quality, a.MaxWidth)
	if err != nil {
		return nil, err
	}
	out := result(map[string]any{"backend": v.Desktop.Name(), "width": w, "height": h, "source_width": im.Bounds().Dx(), "source_height": im.Bounds().Dy(), "coordinate_mapping_available": false, "coordinate_space": "portal image pixels only; no desktop or input mapping", "capture_mode": "consented_portal_single_shot", "client_display_verified": false, "content_trust": "untrusted screen data"})
	out.Content = append(out.Content, &mcp.ImageContent{Data: data, MIMEType: mime})
	return out, nil
}
