package mcpserver

import (
	"context"
	"example.com/codex-vision/internal/platform"
	"fmt"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"strings"
	"time"
)

type UIWaitArgs struct {
	Compact        bool   `json:"compact,omitempty" jsonschema:"Compact returned field snapshot; safety metadata remains server-side"`
	Condition      string `json:"condition" jsonschema:"field_ready, field_present, field_absent, window_active, window_title"`
	WindowID       string `json:"window_id"`
	Label          string `json:"label,omitempty" jsonschema:"Exact field label for field conditions; ambiguous or incomplete trees never match"`
	TitleContains  string `json:"title_contains,omitempty" jsonschema:"Non-empty substring for window_title"`
	TimeoutMS      int    `json:"timeout_ms,omitempty" jsonschema:"100..30000 ms; default up to 10000 and bounded by local timeout"`
	PollIntervalMS int    `json:"poll_interval_ms,omitempty" jsonschema:"Local polling interval 50..2000 ms; default 250; no screenshots or model calls"`
	MaxNodes       int    `json:"max_nodes,omitempty" jsonschema:"Field traversal 1..2000; default 1000"`
}

func (v *Service) withLocalRead(ctx context.Context, fn func() error) error {
	select {
	case v.reads <- struct{}{}:
		defer func() { <-v.reads }()
	case <-ctx.Done():
		return ctx.Err()
	}
	return fn()
}
func matchField(condition, label string, raw platform.UISnapshot) bool {
	if raw.Truncated {
		return false
	}
	matches := 0
	var target platform.UIElement
	for _, e := range raw.Elements {
		if e.Label == label {
			matches++
			target = e
		}
	}
	switch condition {
	case "field_absent":
		return matches == 0
	case "field_present":
		return matches == 1
	case "field_ready":
		return matches == 1 && target.Enabled && target.Editable && !target.Secure
	}
	return false
}
func (v *Service) waitUI(ctx context.Context, a UIWaitArgs) (*mcp.CallToolResult, error) {
	fieldCondition := a.Condition == "field_ready" || a.Condition == "field_present" || a.Condition == "field_absent"
	if !fieldCondition && a.Condition != "window_active" && a.Condition != "window_title" {
		return nil, fmt.Errorf("unsupported wait condition")
	}
	if a.WindowID == "" {
		return nil, fmt.Errorf("window_id is required")
	}
	if fieldCondition && (a.Label == "" || len(a.Label) > 512 || a.TitleContains != "") {
		return nil, fmt.Errorf("field wait requires one exact label up to 512 bytes, without title_contains")
	}
	if !fieldCondition && a.Label != "" {
		return nil, fmt.Errorf("label applies only to field waits")
	}
	if a.Condition == "window_title" && (a.TitleContains == "" || len(a.TitleContains) > 512) {
		return nil, fmt.Errorf("window_title requires title_contains up to 512 bytes")
	}
	if a.Condition == "window_active" && a.TitleContains != "" {
		return nil, fmt.Errorf("window_active does not accept title_contains")
	}
	if a.TimeoutMS == 0 {
		a.TimeoutMS = 10000
	}
	if a.TimeoutMS < 100 || a.TimeoutMS > 30000 {
		return nil, fmt.Errorf("timeout_ms must be 100..30000")
	}
	if a.PollIntervalMS == 0 {
		a.PollIntervalMS = 250
	}
	if a.PollIntervalMS < 50 || a.PollIntervalMS > 2000 {
		return nil, fmt.Errorf("poll_interval_ms must be 50..2000")
	}
	if a.MaxNodes == 0 {
		a.MaxNodes = 1000
	}
	if a.MaxNodes < 1 || a.MaxNodes > 2000 {
		return nil, fmt.Errorf("max_nodes must be 1..2000")
	}
	reader, semantic := v.Desktop.(platform.UIReader)
	if fieldCondition && !semantic {
		return nil, fmt.Errorf("semantic UI unsupported by this backend")
	}
	remaining := time.Duration(max(1, v.Config.TimeoutMS-100)) * time.Millisecond
	if deadline, ok := ctx.Deadline(); ok {
		remaining = min(remaining, time.Until(deadline)-50*time.Millisecond)
	}
	if remaining <= 0 {
		return nil, fmt.Errorf("no time remaining after waiting for UI operation lane")
	}
	effective := min(time.Duration(a.TimeoutMS)*time.Millisecond, remaining)
	waiting, cancel := context.WithTimeout(ctx, effective)
	defer cancel()
	started := time.Now()
	checks := 0
	var raw platform.UISnapshot
	lastError := ""
	response := func(matched bool) map[string]any {
		return map[string]any{"matched": matched, "timed_out": !matched, "condition": a.Condition, "window_id": a.WindowID, "checks": checks, "elapsed_ms": time.Since(started).Milliseconds(), "effective_timeout_ms": effective.Milliseconds(), "incomplete_tree": raw.Truncated, "last_error": lastError, "backend": v.Desktop.Name(), "content_trust": "untrusted UI metadata"}
	}
	// UIReader refreshes the native reference map. Never leave an apparently valid
	// previous MCP snapshot behind after a poll, including on timeout or error.
	if fieldCondition {
		v.uiMu.Lock()
		v.ui = nil
		v.uiMu.Unlock()
	}
	for {
		if err := v.Session.ObservationError(); err != nil {
			return nil, err
		}
		if waiting.Err() != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return result(response(false)), nil
		}
		var window platform.Window
		matched := false
		err := v.withLocalRead(waiting, func() error {
			var err error
			window, err = v.Desktop.Active(waiting)
			if err != nil {
				if a.Condition == "window_active" {
					lastError = err.Error()
					return nil
				}
				return err
			}
			lastError = ""
			checks++
			if a.Condition == "window_active" {
				matched = window.ID == a.WindowID
				return nil
			}
			if window.ID != a.WindowID {
				return fmt.Errorf("foreground changed during wait; input was not performed")
			}
			if a.Condition == "window_title" {
				matched = strings.Contains(window.Title, a.TitleContains)
				return nil
			}
			raw, err = reader.ReadUI(waiting, window, a.MaxNodes)
			if err != nil {
				return err
			}
			active, err := v.Desktop.Active(waiting)
			if err != nil {
				return err
			}
			if active.ID != window.ID || active.Process != window.Process || active.Bounds != window.Bounds {
				return fmt.Errorf("foreground changed during field wait")
			}
			matched = matchField(a.Condition, a.Label, raw)
			return nil
		})
		if stopErr := v.Session.ObservationError(); stopErr != nil {
			return nil, stopErr
		}
		if waiting.Err() != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return result(response(false)), nil
		}
		if err != nil {
			return nil, err
		}
		if matched {
			v.recovery.confirm(window.ID)
			data := response(true)
			if fieldCondition && a.Condition != "field_absent" {
				snapshot, err := v.presentUISnapshot(window, raw, "", a.MaxNodes, a.Compact, nil)
				if err != nil {
					return nil, err
				}
				data["snapshot"] = snapshot
			}
			return result(data), nil
		}
		timer := time.NewTimer(time.Duration(a.PollIntervalMS) * time.Millisecond)
		select {
		case <-waiting.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
}
func (v *Service) registerUIWait(server *mcp.Server) {
	bind(server, v, "ui_wait", "Wait locally for a uniquely labelled field to be present/ready/absent, an observed window to become active, or its title to contain text. Returns once, without screenshots or repeated model calls; timeout is a normal matched=false result. Field waits require macOS AX, invalidate previous snapshots, and return a fresh snapshot on a present/ready match. Incomplete or ambiguous trees never match. Field absence means absence from the accessible tree, not proof of navigation or save. No input, automatic retries of writes, or submission. Bounded by local timeout.", true, func(ctx context.Context, a UIWaitArgs) (*mcp.CallToolResult, error) { return v.waitUI(ctx, a) })
}
