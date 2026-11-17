package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"example.com/codex-vision/internal/platform"
)

type identityDesktop struct {
	*agentDesktop
	identityMu sync.Mutex
	identity   string
	document   string
}

func (d *identityDesktop) ReadUIContext(ctx context.Context, _ platform.Window) (platform.UIContext, error) {
	d.identityMu.Lock()
	defer d.identityMu.Unlock()
	return platform.UIContext{ID: d.document, Kind: "native_window_document"}, ctx.Err()
}

func (d *identityDesktop) ReadControls(ctx context.Context, w platform.Window, n int) (platform.ControlSnapshot, error) {
	raw, err := d.agentDesktop.ReadControls(ctx, w, n)
	d.identityMu.Lock()
	defer d.identityMu.Unlock()
	raw.Context = &platform.UIContext{ID: d.document, Kind: "native_window_document"}
	raw.Controls[0].Identity = d.identity
	return raw, err
}

func (d *identityDesktop) ReadUI(ctx context.Context, w platform.Window, n int) (platform.UISnapshot, error) {
	raw, err := d.agentDesktop.ReadUI(ctx, w, n)
	d.identityMu.Lock()
	defer d.identityMu.Unlock()
	raw.Context = &platform.UIContext{ID: d.document, Kind: "native_window_document"}
	return raw, err
}

func TestNativeTargetRejectsSameLookingReplacementAndDocument(t *testing.T) {
	for _, mode := range []string{"replacement", "identity_lost", "document", "keyboard_document"} {
		t.Run(mode, func(t *testing.T) {
			d := &identityDesktop{agentDesktop: agentFixture("Saved"), identity: "object-A", document: "document-A"}
			cfg := uiConfig()
			cfg.Allow = []string{"mouse_click", "keyboard_press"}
			c, _ := connectDesktop(t, cfg, d)
			observed := call(t, c, "desktop_observe", map[string]any{"include_controls": true, "interval_ms": 250}, true)
			metadata, err := json.Marshal(observed)
			if err != nil || strings.Contains(string(metadata), "object-A") || strings.Contains(string(metadata), "document-A") {
				t.Fatal("private native proof leaked to MCP", err)
			}
			view := agentResult(t, observed)
			d.identityMu.Lock()
			switch mode {
			case "replacement":
				d.identity = "object-B"
			case "identity_lost":
				d.identity = ""
			default:
				d.document = "document-B"
			}
			d.identityMu.Unlock()
			args := agentClick(view.View.ID, view.Controls[0].ID, "Saved")
			if mode == "keyboard_document" {
				args["steps"] = []map[string]any{{"action": "press", "key": "SPACE"}}
			}
			out := agentResult(t, call(t, c, "desktop_act", args, false))
			if len(d.actions()) != 0 || out.Failure.Code != "stale_reference" {
				t.Fatal("same-looking target received input", out, d.actions())
			}
		})
	}
}

func TestFieldSnapshotCannotWriteIntoSameTitleDifferentDocument(t *testing.T) {
	d := &identityDesktop{agentDesktop: agentFixture("Saved"), identity: "object-A", document: "document-A"}
	c, _ := connectDesktop(t, uiConfig(), d)
	id, _ := snapshotData(t, call(t, c, "ui_snapshot", map[string]any{"window_id": "mock-window"}, true))
	d.identityMu.Lock()
	d.document = "document-B"
	d.identityMu.Unlock()
	call(t, c, "ui_fill_batch", map[string]any{"snapshot_id": id, "fields": []map[string]any{{"label": "Name", "text": "Synthetic value"}}}, false)
	if len(d.actions()) != 0 {
		t.Fatal("field snapshot crossed document context", d.actions())
	}
}
