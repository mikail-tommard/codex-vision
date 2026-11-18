package mcpserver

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"example.com/codex-vision/internal/platform"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Discovery data is never read by targeting, readiness or input authorization.
// One bounded entry per service; no screen data or native handles on disk.
type controlCache struct {
	mu    sync.Mutex
	epoch uint64
	entry *controlDiscovery
}

type controlDiscovery struct {
	window    platform.Window
	context   *platform.UIContext
	nodes     int
	compact   bool
	at        time.Time
	revision  string
	digest    [32]byte
	controls  []map[string]any
	truncated bool
	visited   int
}

func (c *controlCache) invalidate() {
	c.mu.Lock()
	c.entry = nil
	c.epoch++
	c.mu.Unlock()
}

func sameDiscoveryWindow(a, b platform.Window) bool {
	return a.ID == b.ID && a.Process == b.Process && a.Bounds == b.Bounds && a.Title == b.Title
}

func (v *Service) discoverControls(ctx context.Context, w platform.Window, a ControlsArgs) (*mcp.CallToolResult, error) {
	c := &v.discovery
	c.mu.Lock()
	previous, epoch := c.entry, c.epoch
	c.mu.Unlock()
	compact := a.Compact == nil || *a.Compact
	compatible := previous != nil && sameDiscoveryWindow(w, previous.window) && previous.nodes == a.MaxNodes && previous.compact == compact
	var currentContext *platform.UIContext
	if reader, ok := v.Desktop.(platform.UIContextReader); ok {
		value, err := reader.ReadUIContext(ctx, w)
		if err != nil {
			c.invalidate()
			return nil, err
		}
		currentContext = &value
	}
	if compatible && !platform.MatchUIContext(previous.context, currentContext) {
		compatible = false
	}
	cached := compatible && a.MaxAgeMS > 0 && time.Since(previous.at) < time.Duration(a.MaxAgeMS)*time.Millisecond
	entry := previous
	if !cached {
		reader, ok := v.Desktop.(platform.ControlReader)
		if !ok {
			c.invalidate()
			return nil, errControlsUnavailable
		}
		started := time.Now()
		raw, err := reader.ReadControls(ctx, w, a.MaxNodes)
		v.performance.record("controls_read", started)
		if err != nil {
			c.invalidate()
			return nil, err
		}
		if len(raw.Controls) > a.MaxNodes {
			c.invalidate()
			return nil, fmt.Errorf("backend exceeded control traversal bound")
		}
		if !platform.MatchUIContext(currentContext, raw.Context) {
			c.invalidate()
			return nil, fmt.Errorf("window context changed during control read")
		}
		encoded, err := json.Marshal(raw)
		if err != nil {
			return nil, err
		}
		entry = &controlDiscovery{window: w, context: raw.Context, nodes: a.MaxNodes, compact: compact, at: started, digest: sha256.Sum256(encoded), truncated: raw.Truncated, visited: raw.Visited, controls: make([]map[string]any, 0, len(raw.Controls))}
		if raw.Context != nil {
			copy := *raw.Context
			entry.context = &copy
		}
		for _, control := range raw.Controls {
			var enabled any
			if control.Enabled != nil {
				enabled = *control.Enabled
			}
			item := map[string]any{"label": control.Label, "role": control.Role, "enabled": enabled}
			if !compact {
				item["bounds"] = control.Bounds
			}
			entry.controls = append(entry.controls, item)
		}
		if compatible && previous.digest == entry.digest {
			entry.revision = previous.revision
		} else {
			entry.revision, err = opaqueID()
			if err != nil {
				return nil, err
			}
		}
	}
	after, err := v.attentionWindow(ctx, w.ID)
	if err != nil {
		c.invalidate()
		return nil, err
	}
	if !sameDiscoveryWindow(w, after) {
		c.invalidate()
		return nil, fmt.Errorf("window changed during control read")
	}
	if err := v.Session.ObservationError(); err != nil {
		c.invalidate()
		return nil, err
	}
	c.mu.Lock()
	if c.epoch != epoch {
		c.mu.Unlock()
		return nil, fmt.Errorf("discovery invalidated during control read; read again")
	}
	c.entry = entry
	c.mu.Unlock()
	if cached {
		v.performance.record("controls_cache_hit", time.Now())
	}
	changed := a.SinceRevision != entry.revision
	meta := map[string]any{"window_id": w.ID, "revision": entry.revision, "changed": changed, "cached": cached, "age_ms": time.Since(entry.at).Milliseconds(), "truncated": entry.truncated, "visited": entry.visited, "content_trust": "untrusted UI metadata; discovery only"}
	if changed {
		meta["controls"] = entry.controls
	}
	return result(meta), nil
}
