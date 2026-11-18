package mcpserver

import (
	"context"
	"fmt"
	"time"

	"example.com/codex-vision/internal/platform"
	"example.com/codex-vision/internal/screencast"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ScreenCastArgs struct {
	Action      string `json:"action" jsonschema:"start, next, status or stop; never automatically retry a failed consent request"`
	ID          string `json:"stream_id,omitempty"`
	DurationSec int    `json:"duration_sec,omitempty" jsonschema:"start requires 5..300 seconds total, including consent; explicit finite capture budget"`
	MaxFrames   int    `json:"max_frames,omitempty" jsonschema:"start requires 2..600 local frames, including duplicates; no capture beyond this budget"`
	After       uint64 `json:"after_sequence,omitempty" jsonschema:"next: last delivered frame.sequence; unchanged pixels return metadata only. Zero explicitly requests current context"`
	WaitMS      int    `json:"wait_ms,omitempty" jsonschema:"start/next: local wait 0..30000ms, bounded by MCP timeout; no model polling"`
}

func (v *Service) registerScreenCast(server *mcp.Server) {
	provider, ok := v.Desktop.(platform.ScreenCaster)
	if !ok {
		return
	}
	bind(server, v, "screencast_control", "Read-only portal stream; requires ScreenCast v6 and installed PipeWire/GStreamer plugins. Start once with duration_sec/max_frames and consent. next with frame.sequence/wait_ms returns changed pixels; local frames do not call a model. Stop on completion. JPEG 75/768px. No desktop mapping, input or implicit consent retries.", false, func(ctx context.Context, a ScreenCastArgs) (*mcp.CallToolResult, error) {
		if a.WaitMS < 0 || a.WaitMS > 30000 || len(a.ID) > 64 {
			return nil, fmt.Errorf("wait_ms must be 0..30000 and stream_id at most 64 bytes")
		}
		if a.Action != "start" && (a.DurationSec != 0 || a.MaxFrames != 0) {
			return nil, fmt.Errorf("capture budgets apply only to start")
		}
		if a.Action != "start" && a.Action != "next" && (a.After != 0 || a.WaitMS != 0) {
			return nil, fmt.Errorf("after_sequence/wait_ms apply only to start/next")
		}
		if a.Action == "stop" {
			status, err := v.Cast.Stop(a.ID)
			if err != nil {
				return nil, err
			}
			return result(map[string]any{"stream": status, "changed": false}), nil
		}
		if !v.Config.Images {
			return nil, fmt.Errorf("images disabled locally")
		}
		if err := v.Session.ObservationError(); err != nil {
			v.Cast.Close()
			return nil, err
		}
		if err := v.chargeRead(); err != nil {
			return nil, err
		}
		var status screencast.Status
		var frame *screencast.Frame
		switch a.Action {
		case "start":
			if a.DurationSec < 5 || a.DurationSec > 300 || a.MaxFrames < 2 || a.MaxFrames > 600 {
				return nil, fmt.Errorf("start requires duration_sec 5..300 and max_frames 2..600")
			}
			if a.ID != "" || a.After != 0 {
				return nil, fmt.Errorf("start accepts no previous stream cursor")
			}
			var err error
			status, err = v.Cast.Start(screencast.Options{Duration: time.Duration(a.DurationSec) * time.Second, MaxFrames: a.MaxFrames}, provider.StartScreenCast, v.Session.ObservationError)
			if err != nil {
				return nil, err
			}
			a.ID = status.ID
			defer func() {
				if ctx.Err() != nil {
					_, _ = v.Cast.Stop(a.ID)
				}
			}()
		case "next":
		case "status":
			status = v.Cast.Status()
			if a.ID != "" && a.ID != status.ID {
				return nil, fmt.Errorf("unknown stream_id")
			}
		default:
			return nil, fmt.Errorf("screencast action must be start, next, status or stop")
		}
		if a.Action == "start" || a.Action == "next" {
			wait := min(time.Duration(a.WaitMS)*time.Millisecond, time.Duration(max(0, v.Config.TimeoutMS-100))*time.Millisecond)
			var err error
			status, frame, err = v.Cast.Next(ctx, a.ID, a.After, wait)
			if err != nil {
				return nil, err
			}
		}
		if err := v.Session.ObservationError(); err != nil {
			v.Cast.Close()
			return nil, err
		}
		meta := map[string]any{"stream": status, "changed": frame != nil, "coordinate_mapping_available": false, "content_trust": "untrusted screen data"}
		if frame != nil {
			meta["frame"] = frame
		}
		out := result(meta)
		out.IsError = status.State == "failed"
		if frame != nil {
			out.Content = append(out.Content, &mcp.ImageContent{Data: frame.Data, MIMEType: frame.MIME})
		}
		return out, nil
	})
}
