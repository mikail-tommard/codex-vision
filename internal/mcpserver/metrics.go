package mcpserver

import (
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type usageMetrics struct {
	mu                                           sync.Mutex
	calls, failures, images, bytes, milliseconds int64
	tools                                        map[string]int64
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
	if out == nil {
		m.failures++
		return
	}
	if out.IsError {
		m.failures++
	}
	for _, c := range out.Content {
		switch c := c.(type) {
		case *mcp.ImageContent:
			m.images++
			m.bytes += int64(len(c.Data))
		case *mcp.TextContent:
			m.bytes += int64(len(c.Text))
		}
	}
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
