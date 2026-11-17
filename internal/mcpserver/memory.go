package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"

	"example.com/codex-vision/internal/platform"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func (v *Service) memoryStatus() map[string]any {
	v.memoryMu.Lock()
	fault := v.memoryFault
	v.memoryMu.Unlock()
	if v.memory == nil {
		return map[string]any{"enabled": false, "error": fault}
	}
	status := v.memory.Stats()
	status["enabled"] = true
	if fault != "" {
		status["error"] = fault
	}
	return status
}

func (v *Service) memoryFailure(err error) {
	if err != nil {
		v.memoryMu.Lock()
		v.memoryFault = "storage_failed; locator safety and native verification remain required"
		v.memoryMu.Unlock()
	}
}

func (v *Service) locatorKey(w platform.Window, raw platform.ControlSnapshot, selector ControlSelector) string {
	// Hash the complete observed structure. A moved/changed control tree never
	// reuses a receipt from the old layout. No raw component reaches storage.
	controls := append([]platform.UIControl(nil), raw.Controls...)
	for i := range controls {
		controls[i].Identity = "" // Process-local AX handles are never reusable locator hints.
	}
	shape, _ := json.Marshal(controls)
	return v.memory.Key(w.Process, selector.Label, selector.Role, string(shape))
}

func (v *Service) recordLocator(w platform.Window, raw platform.ControlSnapshot, selector ControlSelector) {
	if v.memory == nil {
		return
	}
	if _, _, err := selectControl(raw, selector, w); err != nil {
		return
	}
	v.memoryFailure(v.memory.Record(v.locatorKey(w, raw, selector)))
}

func (v *Service) registerMemory(server *mcp.Server) {
	type args struct {
		Action string `json:"action" jsonschema:"status or clear; clear erases only the current configured memory cache"`
	}
	bind(server, v, "ui_memory_control", "Opt-in local hashed verified-locator receipts. No labels, values, coordinates, screenshots or workflow plans on disk. A memory hit never authorizes input or bypasses a fresh native control read. status reports counts; clear removes stored receipts for the configured cache.", false, func(_ context.Context, a args) (*mcp.CallToolResult, error) {
		if a.Action != "status" && a.Action != "clear" {
			return nil, fmt.Errorf("memory action must be status or clear")
		}
		if v.memory == nil {
			return nil, fmt.Errorf("UI memory unavailable; check local session_status diagnostics")
		}
		if a.Action == "clear" {
			if err := v.memory.Clear(); err != nil {
				v.memoryFailure(err)
				return nil, fmt.Errorf("UI memory clear failed")
			}
		}
		return result(v.memoryStatus()), nil
	})
}
