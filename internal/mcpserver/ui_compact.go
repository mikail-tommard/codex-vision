package mcpserver

import (
	"example.com/codex-vision/internal/platform"
	"fmt"
)

func validateLabels(labels []string, query string) error {
	if len(labels) > 20 {
		return fmt.Errorf("labels supports at most 20 exact labels")
	}
	if len(labels) > 0 && query != "" {
		return fmt.Errorf("choose labels or query")
	}
	seen := make(map[string]bool)
	for _, label := range labels {
		if label == "" || len(label) > 512 || seen[label] {
			return fmt.Errorf("labels must be unique non-empty strings of at most 512 bytes")
		}
		seen[label] = true
	}
	return nil
}
func (v *Service) presentUISnapshot(w platform.Window, raw platform.UISnapshot, query string, maxNodes int, compact bool, labels []string) (map[string]any, error) {
	if len(raw.Elements) > maxNodes {
		return nil, fmt.Errorf("backend exceeded traversal bound")
	}
	selected := raw
	missing := make([]string, 0)
	if len(labels) > 0 {
		wanted := make(map[string]bool)
		counts := make(map[string]int)
		for _, label := range labels {
			wanted[label] = true
		}
		selected.Elements = make([]platform.UIElement, 0, len(labels))
		for _, element := range raw.Elements {
			if wanted[element.Label] {
				selected.Elements = append(selected.Elements, element)
				counts[element.Label]++
			}
		}
		for _, label := range labels {
			if counts[label] == 0 {
				missing = append(missing, label)
			}
		}
	}
	data, err := v.saveUISnapshot(w, selected, query, maxNodes)
	if err != nil {
		return nil, err
	}
	if len(labels) > 0 {
		data["missing_labels"] = missing
		data["selection_complete"] = !raw.Truncated
	}
	if !compact {
		return data, nil
	}
	elements := data["elements"].([]platform.UIElement)
	counts := make(map[string]int)
	for _, e := range elements {
		counts[e.Label]++
	}
	fields := make([]map[string]any, 0, len(elements))
	for _, e := range elements {
		field := map[string]any{"element_id": e.ID, "label": e.Label, "ready": e.Enabled && e.Editable && !e.Secure}
		if e.Secure {
			field["secure"] = true
		}
		if counts[e.Label] > 1 {
			field["ambiguous"] = true
		}
		if e.Placeholder != "" && e.Placeholder != e.Label {
			field["placeholder"] = e.Placeholder
		}
		fields = append(fields, field)
	}
	data["elements"] = fields
	data["compact"] = true
	return data, nil
}
