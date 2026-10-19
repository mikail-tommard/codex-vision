package mcpserver

import (
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"example.com/codex-vision/internal/observation"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type usageMetrics struct {
	mu                                           sync.Mutex
	calls, failures, images, bytes, milliseconds int64
	tools                                        map[string]int64
	recent                                       []callDiagnostic
	lastFinished                                 time.Time
}

type callDiagnostic struct {
	Tool           string                          `json:"tool"`
	StartedAt      time.Time                       `json:"started_at"`
	ElapsedMS      int64                           `json:"elapsed_ms"`
	GapMS          *int64                          `json:"gap_since_previous_finish_ms,omitempty"`
	Outcome        string                          `json:"outcome"`
	FailureCode    string                          `json:"failure_code,omitempty"`
	Reason         string                          `json:"reason,omitempty"`
	NativeError    *int                            `json:"native_error,omitempty"`
	Stage          string                          `json:"stage,omitempty"`
	Posted         bool                            `json:"action_posted,omitempty"`
	MayHaveChanged bool                            `json:"may_have_changed,omitempty"`
	TimedOut       bool                            `json:"timed_out,omitempty"`
	ViewConsumed   *bool                           `json:"view_consumed,omitempty"`
	Completed      *int                            `json:"completed_steps,omitempty"`
	Timings        *observation.InteractionTimings `json:"interaction_timings,omitempty"`
	Images         int                             `json:"images"`
}

var nativeErrorRE = regexp.MustCompile(`native error (-?[0-9]{1,9})`)

// Closed categories only: never retain the original error, arguments or UI text.
func diagnosticReason(message string) string {
	s := strings.ToLower(message)
	switch {
	case strings.Contains(s, "cannot read focused") || strings.Contains(s, "no visible foreground"):
		return "active_window_unavailable"
	case strings.Contains(s, "geometry") || strings.Contains(s, "window identity"):
		return "window_identity_changed"
	case strings.Contains(s, "foreground"):
		return "foreground_unconfirmed"
	case strings.Contains(s, "attention") || strings.Contains(s, "observation or"):
		return "observation_changed"
	case strings.Contains(s, "settle_ms") || strings.Contains(s, "wait_ms"):
		return "invalid_wait_arguments"
	case strings.Contains(s, "permission") || strings.Contains(s, "approval") || strings.Contains(s, "allowlist"):
		return "permission_denied"
	case strings.Contains(s, "stale") || strings.Contains(s, "expired") || strings.Contains(s, "consumed"):
		return "stale_reference"
	default:
		return "unclassified"
	}
}

func (m *usageMetrics) record(tool string, out *mcp.CallToolResult, elapsed time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.tools == nil {
		m.tools = map[string]int64{}
	}
	m.calls++
	m.tools[tool]++
	m.milliseconds += elapsed.Milliseconds()
	now := time.Now()
	diagnostic := callDiagnostic{Tool: tool, StartedAt: now.Add(-elapsed).UTC(), ElapsedMS: elapsed.Milliseconds(), Outcome: "ok"}
	if !m.lastFinished.IsZero() {
		gap := diagnostic.StartedAt.Sub(m.lastFinished).Milliseconds()
		diagnostic.GapMS = &gap
	}
	m.lastFinished = now
	defer func() {
		if len(m.recent) == 12 {
			copy(m.recent, m.recent[1:])
			m.recent[11] = diagnostic
		} else {
			m.recent = append(m.recent, diagnostic)
		}
	}()
	if out == nil {
		m.failures++
		diagnostic.Outcome = "error"
		return
	}
	if out.IsError {
		m.failures++
		diagnostic.Outcome = "error"
	}
	if meta, ok := out.StructuredContent.(map[string]any); ok {
		if f, ok := meta["failure"].(failureInfo); ok {
			diagnostic.FailureCode = f.Code
		}
		if code, ok := meta["code"].(string); ok && code == "focus_recovery_required" {
			diagnostic.FailureCode = code
		}
		if message, ok := meta["error"].(string); ok {
			diagnostic.Reason = diagnosticReason(message)
			if matched := nativeErrorRE.FindStringSubmatch(message); len(matched) == 2 {
				n, e := strconv.Atoi(matched[1])
				if e == nil {
					diagnostic.NativeError = &n
				}
			}
		}
		if n, ok := meta["completed_steps"].(int); ok {
			diagnostic.Completed = &n
		}
		if used, ok := meta["view_consumed"].(bool); ok {
			diagnostic.ViewConsumed = &used
		}
		if r, ok := meta["interaction"].(observation.ActionWaitResult); ok {
			diagnostic.Stage = r.Stage
			diagnostic.Posted, diagnostic.MayHaveChanged, diagnostic.TimedOut = r.Posted, r.MayHaveChanged, r.TimedOut
			timings := r.Timings
			diagnostic.Timings = &timings
			if r.TimedOut && diagnostic.Outcome == "ok" {
				diagnostic.Outcome = "timeout"
				diagnostic.Reason = "wait_timeout"
			}
		}
	}
	for _, c := range out.Content {
		switch c := c.(type) {
		case *mcp.ImageContent:
			diagnostic.Images++
			m.images++
			m.bytes += int64(len(c.Data))
		case *mcp.TextContent:
			m.bytes += int64(len(c.Text))
		}
	}
}

func (m *usageMetrics) recentSnapshot() []callDiagnostic {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]callDiagnostic{}, m.recent...)
}

func (m *usageMetrics) snapshot() map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	tools := map[string]int64{}
	for k, v := range m.tools {
		tools[k] = v
	}
	return map[string]any{"tool_calls": m.calls, "tool_errors": m.failures, "images_returned": m.images, "result_content_bytes": m.bytes, "tool_total_ms": m.milliseconds, "calls_by_tool": tools, "scope": "this process; text plus raw image bytes, excludes JSON/base64 overhead; not tokens, model/network time or actual human interventions"}
}
