package mcpserver

import (
	"strings"

	"example.com/codex-vision/internal/observation"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ActionOutcome struct {
	Status             string `json:"status"`
	Verified           bool   `json:"verified"`
	InputMayHavePosted bool   `json:"input_may_have_posted"`
	RetryAction        bool   `json:"retry_action"`
	Evidence           string `json:"evidence"`
}

// Additive metadata: legacy IsError/interaction fields keep their meanings.
// A successful native event post alone never proves the application outcome.
func withActionOutcome(tool string, out *mcp.CallToolResult) *mcp.CallToolResult {
	if out == nil || !inputTool(tool) {
		return out
	}
	r := ActionOutcome{Status: "ambiguous", Evidence: "event_delivery_only; inspect application state"}
	meta, mapped := out.StructuredContent.(map[string]any)
	if batch, ok := out.StructuredContent.(*batchReport); ok {
		meta = map[string]any{"ok": batch.OK, "window_id": batch.WindowID, "fields": batch.Fields, "completed": batch.Completed, "partial": batch.Partial, "submitted_by_tool": batch.Submitted, "backend": batch.Backend}
		r.InputMayHavePosted = batch.Completed > 0
		for _, field := range batch.Fields {
			r.InputMayHavePosted = r.InputMayHavePosted || field.MayHaveChanged
		}
		if batch.OK {
			r.Status, r.Verified, r.Evidence = "success", true, "native_field_value_readback; not form submission"
		}
		mapped = true
	}
	if !mapped {
		return out
	}
	if interaction, ok := meta["interaction"].(observation.ActionWaitResult); ok {
		r.InputMayHavePosted = interaction.Posted || interaction.MayHaveChanged
		if interaction.TimedOut {
			r.Status, r.Evidence = "timeout", "postcondition not observed before deadline; do not replay input"
		} else if verified, ok := meta["verification"].(verificationReport); ok && verified.Configured && verified.Matched {
			r.Status, r.Verified, r.Evidence = "success", true, verified.Scope
		} else if readiness, ok := meta["readiness"].(readinessReport); ok && readiness.Mode == "ax_ready" && readiness.Matched {
			r.Status, r.Verified, r.Evidence = "success", true, "native readiness predicate only; not application business state"
		}
	} else if ok, _ := meta["ok"].(bool); ok {
		r.InputMayHavePosted = true
		if tool == "ui_fill" || tool == "window_focus" || tool == "app_launch" {
			r.Status, r.Verified, r.Evidence = "success", true, "native field readback or foreground confirmation only"
		}
	}
	if out.IsError {
		r.Status, r.Verified, r.Evidence = "failed", false, "operation failed; inspect failure and completed steps"
		if f, ok := meta["failure"].(failureInfo); ok {
			switch f.Code {
			case "ambiguous_target":
				r.Status = "ambiguous"
			case "timeout":
				r.Status = "timeout"
			}
			if _, hasInteraction := meta["interaction"]; !hasInteraction && (f.Code == "operation_failed" || f.Code == "timeout" || f.Code == "cancelled") {
				// Legacy primitive errors cannot prove that no event was posted.
				r.InputMayHavePosted = true
			}
		}
		if message, _ := meta["error"].(string); strings.Contains(message, "approval_required") {
			r.Status = "needs_confirmation"
		}
	}
	meta["outcome"] = r
	return replaceMetadata(out, meta)
}
