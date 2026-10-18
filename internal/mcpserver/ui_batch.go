package mcpserver

import (
	"context"
	"example.com/codex-vision/internal/input"
	"example.com/codex-vision/internal/platform"
	"fmt"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"time"
)

type UIFieldValue struct {
	ElementID string `json:"element_id,omitempty"`
	Label     string `json:"label,omitempty" jsonschema:"Exact unique label; choose element_id or label"`
	Text      string `json:"text"`
}
type UIBatchArgs struct {
	SnapshotID string         `json:"snapshot_id"`
	Fields     []UIFieldValue `json:"fields" jsonschema:"1..20 fields from one ui_snapshot; validated before any write"`
}
type fieldResult struct {
	Index          int    `json:"index"`
	ElementID      string `json:"element_id"`
	Status         string `json:"status"`
	Error          string `json:"error,omitempty"`
	MayHaveChanged bool   `json:"may_have_changed,omitempty"`
}
type batchReport struct {
	OK        bool          `json:"ok"`
	WindowID  string        `json:"window_id"`
	Fields    []fieldResult `json:"fields"`
	Completed int           `json:"completed"`
	Partial   bool          `json:"partial"`
	Submitted bool          `json:"submitted_by_tool"`
	Backend   string        `json:"backend"`
}

func (v *Service) reserveTargets(id string, fields []UIFieldValue) (*uiSnapshot, []platform.UIElement, error) {
	if len(fields) < 1 || len(fields) > 20 {
		return nil, nil, fmt.Errorf("fields must contain 1..20 entries")
	}
	v.uiMu.Lock()
	defer v.uiMu.Unlock()
	snapshot := v.ui
	if snapshot == nil || snapshot.ID != id || time.Now().After(snapshot.Expires) {
		return nil, nil, fmt.Errorf("stale snapshot; call ui_snapshot again")
	}
	targets := make([]platform.UIElement, 0, len(fields))
	seen := make(map[string]bool)
	for i, field := range fields {
		if snapshot.Truncated && field.Label != "" {
			return nil, nil, fmt.Errorf("field %d: label uniqueness unknown in partial snapshot; inspect and use an explicit element_id", i)
		}
		if (field.ElementID == "") == (field.Label == "") || len(field.Label) > 512 {
			return nil, nil, fmt.Errorf("field %d: choose element_id or an exact label (up to 512 bytes)", i)
		}
		if err := input.ValidateText(field.Text); err != nil {
			return nil, nil, fmt.Errorf("field %d: %w", i, err)
		}
		var target platform.UIElement
		matches := 0
		for eid, element := range snapshot.Elements {
			if (field.ElementID != "" && eid == field.ElementID) || (field.Label != "" && element.Label == field.Label) {
				target = element
				matches++
			}
		}
		if matches != 1 {
			return nil, nil, fmt.Errorf("field %d matched %d elements; use element_id from ui_snapshot", i, matches)
		}
		if seen[target.ID] {
			return nil, nil, fmt.Errorf("field %d duplicates an earlier target", i)
		}
		if err := input.Validate(platform.Action{Tool: "ui_fill", WindowID: snapshot.Window.ID, Text: field.Text, UIExpected: &target}, snapshot.Window.Bounds); err != nil {
			return nil, nil, fmt.Errorf("field %d: %w", i, err)
		}
		seen[target.ID] = true
		targets = append(targets, target)
	}
	// Reserve only after complete preflight. Even unattempted targets require a
	// fresh snapshot after partial execution; the same batch cannot be replayed.
	for _, target := range targets {
		delete(snapshot.Elements, target.ID)
	}
	return snapshot, targets, nil
}
func (v *Service) fillBatch(ctx context.Context, id string, fields []UIFieldValue) (*batchReport, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	snapshot, targets, err := v.reserveTargets(id, fields)
	if err != nil {
		return nil, err
	}
	report := &batchReport{OK: true, WindowID: snapshot.Window.ID, Backend: v.Desktop.Name(), Fields: make([]fieldResult, len(targets))}
	for i, target := range targets {
		report.Fields[i] = fieldResult{Index: i, ElementID: target.ID, Status: "not_attempted"}
	}
	for i, target := range targets {
		err = ctx.Err()
		if err == nil {
			err = v.Session.ObservationError()
		}
		if err == nil {
			v.uiMu.Lock()
			valid := v.ui == snapshot && time.Now().Before(snapshot.Expires)
			v.uiMu.Unlock()
			if !valid {
				err = fmt.Errorf("snapshot expired or replaced; inspect completed fields before refreshing")
			}
		}
		if err == nil {
			report.Fields[i].MayHaveChanged = true
			_, err = v.action(ctx, platform.Action{Tool: "ui_fill", WindowID: snapshot.Window.ID, Text: fields[i].Text, UIExpected: &target})
		}
		if err != nil {
			report.OK = false
			report.Partial = report.Completed > 0
			report.Fields[i].Status = "failed"
			report.Fields[i].Error = err.Error()
			break
		}
		report.Fields[i].Status = "filled"
		report.Fields[i].MayHaveChanged = false
		report.Completed++
	}
	return report, nil
}
func (v *Service) registerUIBatch(server *mcp.Server) {
	bind(server, v, "ui_fill_batch", "Fill 1..20 text fields from ONE ui_snapshot in order. Preflight all selectors/values, then reserve targets and execute through ui_fill policy for each field. Stops on first failure; reports filled/failed/not_attempted without echoing text. No rollback, submission, automatic retry or snapshot refresh. After failure inspect completed fields and obtain a fresh snapshot. Each write retains local approval and input budget checks.", false, func(ctx context.Context, a UIBatchArgs) (*mcp.CallToolResult, error) {
		report, err := v.fillBatch(ctx, a.SnapshotID, a.Fields)
		if err != nil {
			return nil, err
		}
		out := result(report)
		out.IsError = !report.OK
		return out, nil
	})
}
