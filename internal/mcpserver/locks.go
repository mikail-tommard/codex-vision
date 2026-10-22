package mcpserver

import (
	"context"
	"example.com/codex-vision/internal/platform"
	"strings"
)

// UI snapshots and native reference maps share one lifecycle. A batch also
// reserves the MCP input lane so unrelated client input cannot interleave.
// session_stop never acquires either lane and remains immediately available.
func (s *Service) lockTool(ctx context.Context, name string) (func(), error) {
	var lanes []chan struct{}
	if strings.HasPrefix(name, "ui_") || name == "desktop_observe" || name == "desktop_act" || name == "advisor" {
		lanes = append(lanes, s.uiOps)
	}
	if platform.NeedsApproval(name) || name == "mouse_move" || name == "mouse_scroll" || name == "window_focus" || name == "ui_fill_batch" || name == "mouse_action_wait" || name == "desktop_batch" || name == "desktop_act" || name == "advisor" {
		lanes = append(lanes, s.inputs)
	}
	acquired := 0
	release := func() {
		for i := acquired - 1; i >= 0; i-- {
			<-lanes[i]
		}
		acquired = 0
	}
	for _, lane := range lanes {
		select {
		case lane <- struct{}{}:
			acquired++
		case <-ctx.Done():
			release()
			return nil, ctx.Err()
		}
	}
	return release, nil
}
