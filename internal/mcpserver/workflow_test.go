package mcpserver

import (
	"context"
	"encoding/json"
	"example.com/codex-vision/internal/platform"
	"example.com/codex-vision/internal/platform/mock"
	"fmt"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type workflowDesktop struct {
	mock.Desktop
	stateMu      sync.Mutex
	fields       []platform.UIElement
	window       platform.Window
	recorded     []platform.Action
	failAt       int
	firstEntered chan struct{}
	releaseFirst chan struct{}
	fillCalls    atomic.Int32
	reads        atomic.Int32
	truncated    bool
}

func workflow() *workflowDesktop {
	fields := make([]platform.UIElement, 3)
	for i, label := range []string{"Name", "Email", "City"} {
		fields[i] = platform.UIElement{Label: label, Role: "AXTextField", Enabled: true, Editable: true, Identity: fmt.Sprint(i), Path: []int{i}, Bounds: platform.Rect{X: 20, Y: 20 + i*40, Width: 200, Height: 30}}
	}
	return &workflowDesktop{fields: fields, window: platform.Window{ID: "mock-window", Process: "mock.exe", Title: "Loading", Bounds: platform.Rect{Width: 640, Height: 480}}}
}
func (d *workflowDesktop) Active(ctx context.Context) (platform.Window, error) {
	d.stateMu.Lock()
	defer d.stateMu.Unlock()
	return d.window, ctx.Err()
}
func (d *workflowDesktop) ReadUI(ctx context.Context, w platform.Window, n int) (platform.UISnapshot, error) {
	d.reads.Add(1)
	d.stateMu.Lock()
	defer d.stateMu.Unlock()
	return platform.UISnapshot{Elements: append([]platform.UIElement(nil), d.fields...), Visited: len(d.fields), Truncated: d.truncated}, ctx.Err()
}
func (d *workflowDesktop) Execute(ctx context.Context, a platform.Action) error {
	if a.Tool == "ui_fill" {
		n := int(d.fillCalls.Add(1))
		if n == 1 && d.firstEntered != nil {
			close(d.firstEntered)
			if d.releaseFirst != nil {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-d.releaseFirst:
				}
			}
		}
		if d.failAt == n {
			return fmt.Errorf("native field changed")
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	d.stateMu.Lock()
	d.recorded = append(d.recorded, a)
	d.stateMu.Unlock()
	return nil
}
func (d *workflowDesktop) actions() []platform.Action {
	d.stateMu.Lock()
	defer d.stateMu.Unlock()
	return append([]platform.Action(nil), d.recorded...)
}
func batchFields() []map[string]any {
	return []map[string]any{{"label": "Name", "text": "Иван"}, {"label": "Email", "text": "user@example.test"}, {"label": "City", "text": "Москва"}}
}
func reportData(t *testing.T, r *mcp.CallToolResult) batchReport {
	t.Helper()
	var report batchReport
	if err := json.Unmarshal([]byte(r.Content[0].(*mcp.TextContent).Text), &report); err != nil {
		t.Fatal(err)
	}
	return report
}
func TestBatchFillAndPartialFailure(t *testing.T) {
	for _, fail := range []int{0, 2} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			d := workflow()
			d.failAt = fail
			c, _ := connectDesktop(t, uiConfig(), d)
			id, _ := snapshotData(t, call(t, c, "ui_snapshot", map[string]any{"window_id": "mock-window"}, true))
			r := call(t, c, "ui_fill_batch", map[string]any{"snapshot_id": id, "fields": batchFields()}, fail == 0)
			report := reportData(t, r)
			expected := 3
			if fail > 0 {
				expected = 1
			}
			if report.Completed != expected || len(d.actions()) != expected || report.Submitted || len(report.Fields) != 3 {
				t.Fatal(report)
			}
			if fail > 0 && (!report.Partial || report.Fields[1].Status != "failed" || !report.Fields[1].MayHaveChanged || report.Fields[2].Status != "not_attempted") {
				t.Fatal(report)
			}
			for i, a := range d.actions() {
				if a.Tool != "ui_fill" || a.WindowID != "mock-window" || a.UIExpected.Label != []string{"Name", "Email", "City"}[i] {
					t.Fatal(a)
				}
			}
			if strings.Contains(r.Content[0].(*mcp.TextContent).Text, "user@example.test") {
				t.Fatal("value echoed")
			}
			call(t, c, "ui_fill_batch", map[string]any{"snapshot_id": id, "fields": batchFields()}, false)
			if len(d.actions()) != expected {
				t.Fatal("batch replayed")
			}
		})
	}
}
func TestBatchPreflightWritesNothing(t *testing.T) {
	for _, kind := range []string{"missing", "duplicate", "empty_text", "secure", "unknown_property"} {
		t.Run(kind, func(t *testing.T) {
			d := workflow()
			if kind == "secure" {
				d.fields[2].Secure = true
			}
			c, _ := connectDesktop(t, uiConfig(), d)
			id, _ := snapshotData(t, call(t, c, "ui_snapshot", map[string]any{"window_id": "mock-window"}, true))
			fields := batchFields()
			switch kind {
			case "missing":
				fields[2]["label"] = "Missing"
			case "duplicate":
				fields[2]["label"] = "Name"
			case "empty_text":
				fields[2]["text"] = ""
			case "unknown_property":
				fields[2]["submit"] = true
			}
			call(t, c, "ui_fill_batch", map[string]any{"snapshot_id": id, "fields": fields}, false)
			if len(d.actions()) != 0 {
				t.Fatal("preflight performed input")
			}
		})
	}
}

type toolReply struct {
	r   *mcp.CallToolResult
	err error
}

func asyncCall(c *mcp.ClientSession, name string, args any) <-chan toolReply {
	out := make(chan toolReply, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		r, err := c.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		out <- toolReply{r, err}
	}()
	return out
}
func awaitReply(t *testing.T, c <-chan toolReply) toolReply {
	t.Helper()
	select {
	case r := <-c:
		if r.err != nil {
			t.Fatal(r.err)
		}
		return r
	case <-time.After(4 * time.Second):
		t.Fatal("tool did not return")
		return toolReply{}
	}
}
func TestBatchSerializesOtherInputAndAllowsStop(t *testing.T) {
	for _, stop := range []bool{false, true} {
		t.Run(fmt.Sprint(stop), func(t *testing.T) {
			d := workflow()
			d.firstEntered = make(chan struct{})
			d.releaseFirst = make(chan struct{})
			cfg := uiConfig()
			cfg.Allow = append(cfg.Allow, "mouse_move")
			c, _ := connectDesktop(t, cfg, d)
			id, _ := snapshotData(t, call(t, c, "ui_snapshot", map[string]any{"window_id": "mock-window"}, true))
			pending := asyncCall(c, "ui_fill_batch", map[string]any{"snapshot_id": id, "fields": batchFields()})
			select {
			case <-d.firstEntered:
			case <-time.After(time.Second):
				t.Fatal("first write not started")
			}
			if stop {
				call(t, c, "session_stop", map[string]any{}, true)
				reply := awaitReply(t, pending)
				if !reply.r.IsError || len(d.actions()) != 0 {
					t.Fatal("stop did not cancel batch")
				}
			} else {
				move := asyncCall(c, "mouse_move", map[string]any{"window_id": "mock-window", "x": 2, "y": 2})
				time.Sleep(30 * time.Millisecond)
				close(d.releaseFirst)
				if awaitReply(t, pending).r.IsError || awaitReply(t, move).r.IsError {
					t.Fatal("input failed")
				}
				actions := d.actions()
				if len(actions) != 4 || actions[3].Tool != "mouse_move" {
					t.Fatal("input interleaved with batch", actions)
				}
			}
		})
	}
}
func TestLocalWaitReturnsUsableSnapshotWithoutBlockingCapture(t *testing.T) {
	d := workflow()
	d.fields[0].Editable = false
	cfg := uiConfig()
	cfg.MaxPerMinute = 100
	c, svc := connectDesktop(t, cfg, d)
	old, _ := snapshotData(t, call(t, c, "ui_snapshot", map[string]any{"window_id": "mock-window"}, true))
	call(t, c, "observation_start", map[string]any{"preview": false, "interval_ms": 250}, true)
	svc.mu.Lock()
	before := svc.total
	svc.mu.Unlock()
	go func() {
		time.Sleep(450 * time.Millisecond)
		d.stateMu.Lock()
		d.fields[0].Editable = true
		d.stateMu.Unlock()
	}()
	r := call(t, c, "ui_wait", map[string]any{"condition": "field_ready", "window_id": "mock-window", "label": "Name", "timeout_ms": 1500, "poll_interval_ms": 50}, true)
	var out struct {
		Matched  bool            `json:"matched"`
		Snapshot json.RawMessage `json:"snapshot"`
	}
	if err := json.Unmarshal([]byte(r.Content[0].(*mcp.TextContent).Text), &out); err != nil {
		t.Fatal(err)
	}
	if !out.Matched || d.reads.Load() < 3 || svc.Observer.Status().LocalFrames < 2 {
		t.Fatal("wait did not poll or blocked capture")
	}
	svc.mu.Lock()
	total := svc.total
	svc.mu.Unlock()
	if total != before+1 {
		t.Fatal("polls consumed MCP read budget", total, before)
	}
	var snapshot struct {
		ID string `json:"snapshot_id"`
	}
	if err := json.Unmarshal(out.Snapshot, &snapshot); err != nil || snapshot.ID == "" {
		t.Fatal("no snapshot", err)
	}
	call(t, c, "ui_fill", map[string]any{"snapshot_id": old, "label": "Name", "text": "stale"}, false)
	call(t, c, "ui_fill_batch", map[string]any{"snapshot_id": snapshot.ID, "fields": batchFields()}, true)
}
func TestWaitTimeoutAmbiguityTitleAndStop(t *testing.T) {
	d := workflow()
	d.fields[0].Editable = false
	c, _ := connectDesktop(t, uiConfig(), d)
	r := call(t, c, "ui_wait", map[string]any{"condition": "field_ready", "window_id": "mock-window", "label": "Name", "timeout_ms": 150, "poll_interval_ms": 50}, true)
	var timeout struct {
		Matched  bool `json:"matched"`
		TimedOut bool `json:"timed_out"`
	}
	if err := json.Unmarshal([]byte(r.Content[0].(*mcp.TextContent).Text), &timeout); err != nil || timeout.Matched || !timeout.TimedOut {
		t.Fatal(timeout, err)
	}
	d.stateMu.Lock()
	d.window.Title = "Saved"
	d.stateMu.Unlock()
	call(t, c, "ui_wait", map[string]any{"condition": "window_title", "window_id": "mock-window", "title_contains": "Saved", "timeout_ms": 100}, true)
	pending := asyncCall(c, "ui_wait", map[string]any{"condition": "field_ready", "window_id": "mock-window", "label": "Name", "timeout_ms": 1500, "poll_interval_ms": 50})
	time.Sleep(70 * time.Millisecond)
	call(t, c, "session_stop", map[string]any{}, true)
	if !awaitReply(t, pending).r.IsError {
		t.Fatal("wait ignored stop")
	}
	raw := platform.UISnapshot{Truncated: true}
	if matchField("field_absent", "Name", raw) {
		t.Fatal("partial tree falsely matched absence")
	}
	raw = platform.UISnapshot{Elements: []platform.UIElement{{Label: "Name"}, {Label: "Name"}}}
	if matchField("field_present", "Name", raw) {
		t.Fatal("ambiguous field matched")
	}
}

func TestCursorModeFlowsToActionsWithoutChangingDragDuration(t *testing.T) {
	d := workflow()
	cfg := uiConfig()
	cfg.CursorMode = "fast"
	cfg.Allow = []string{"mouse_move", "mouse_click", "mouse_drag"}
	c, _ := connectDesktop(t, cfg, d)
	call(t, c, "mouse_move", map[string]any{"window_id": "mock-window", "x": 30, "y": 30}, true)
	call(t, c, "mouse_move", map[string]any{"window_id": "mock-window", "x": 31, "y": 31, "motion": "smooth"}, true)
	r := call(t, c, "mouse_move", map[string]any{"window_id": "mock-window", "x": 32, "y": 32, "duration_ms": 25}, true)
	call(t, c, "mouse_click", map[string]any{"window_id": "mock-window", "x": 33, "y": 33}, true)
	call(t, c, "mouse_drag", map[string]any{"window_id": "mock-window", "x": 34, "y": 34, "end_x": 40, "end_y": 40, "duration_ms": 400}, true)
	a := d.actions()
	if len(a) != 5 || a[0].ApproachDuration != 70*time.Millisecond || a[1].ApproachDuration != 300*time.Millisecond || a[2].Duration != 25*time.Millisecond || a[3].ApproachDuration != 70*time.Millisecond || a[4].ApproachDuration != 70*time.Millisecond || a[4].Duration != 400*time.Millisecond {
		t.Fatal(a)
	}
	var meta struct {
		Duration int `json:"approach_duration_ms"`
	}
	if err := json.Unmarshal([]byte(r.Content[0].(*mcp.TextContent).Text), &meta); err != nil || meta.Duration != 25 {
		t.Fatal(meta, err)
	}
	call(t, c, "mouse_move", map[string]any{"x": 30, "y": 30, "motion": "instant"}, false)
}
func TestWaitWindowActiveAndFieldAbsence(t *testing.T) {
	d := workflow()
	d.window.ID = "other-window"
	c, _ := connectDesktop(t, uiConfig(), d)
	pending := asyncCall(c, "ui_wait", map[string]any{"condition": "window_active", "window_id": "mock-window", "timeout_ms": 1000, "poll_interval_ms": 50})
	time.Sleep(100 * time.Millisecond)
	d.stateMu.Lock()
	d.window.ID = "mock-window"
	d.stateMu.Unlock()
	r := awaitReply(t, pending)
	if r.r.IsError || !strings.Contains(r.r.Content[0].(*mcp.TextContent).Text, `"matched":true`) {
		t.Fatal(r.r)
	}
	call(t, c, "ui_wait", map[string]any{"condition": "field_absent", "window_id": "mock-window", "label": "Missing", "timeout_ms": 100}, true)
	call(t, c, "ui_wait", map[string]any{"condition": "window_title", "window_id": "wrong", "title_contains": "Loading", "timeout_ms": 100}, false)
	for _, a := range []map[string]any{{"condition": "unknown"}, {"condition": "field_ready", "label": ""}, {"condition": "field_ready", "label": "Name", "poll_interval_ms": 1}, {"condition": "field_ready", "label": "Name", "timeout_ms": 99}} {
		a["window_id"] = "mock-window"
		call(t, c, "ui_wait", a, false)
	}
}

func TestCompactSelectionPreservesSafetyAndReducesPayload(t *testing.T) {
	d := workflow()
	c, _ := connectDesktop(t, uiConfig(), d)
	full := call(t, c, "ui_snapshot", map[string]any{"window_id": "mock-window"}, true)
	compact := call(t, c, "ui_snapshot", map[string]any{"window_id": "mock-window", "compact": true, "labels": []string{"Email", "Missing"}}, true)
	fullText := full.Content[0].(*mcp.TextContent).Text
	short := compact.Content[0].(*mcp.TextContent).Text
	var out struct {
		ID       string           `json:"snapshot_id"`
		Elements []map[string]any `json:"elements"`
		Missing  []string         `json:"missing_labels"`
	}
	if err := json.Unmarshal([]byte(short), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Elements) != 1 || out.Elements[0]["bounds"] != nil || out.Elements[0]["identity"] != nil || out.Elements[0]["ready"] != true || len(out.Missing) != 1 || out.Missing[0] != "Missing" || len(short) >= len(fullText) {
		t.Fatal("compact response not filtered or compact", short)
	}
	call(t, c, "ui_fill", map[string]any{"snapshot_id": out.ID, "label": "Email", "text": "user@example.test"}, true)
	if len(d.actions()) != 1 || d.actions()[0].UIExpected.Identity == "" || d.actions()[0].UIExpected.Bounds.Width == 0 {
		t.Fatal("safety metadata lost")
	}
	t.Logf("fixture JSON bytes: full=%d compact+selection=%d", len(fullText), len(short))
	call(t, c, "ui_snapshot", map[string]any{"window_id": "mock-window", "query": "Email", "labels": []string{"Email"}}, false)
}
func TestCompactWaitReturnsFillableSnapshot(t *testing.T) {
	d := workflow()
	c, _ := connectDesktop(t, uiConfig(), d)
	r := call(t, c, "ui_wait", map[string]any{"condition": "field_ready", "window_id": "mock-window", "label": "Name", "timeout_ms": 100, "compact": true}, true)
	var out struct {
		Snapshot struct {
			ID      string `json:"snapshot_id"`
			Compact bool   `json:"compact"`
		} `json:"snapshot"`
	}
	if err := json.Unmarshal([]byte(r.Content[0].(*mcp.TextContent).Text), &out); err != nil || out.Snapshot.ID == "" || !out.Snapshot.Compact {
		t.Fatal(out, err)
	}
	call(t, c, "ui_fill_batch", map[string]any{"snapshot_id": out.Snapshot.ID, "fields": batchFields()}, true)
}

func TestPartialCompactSnapshotCannotClaimUniqueLabel(t *testing.T) {
	d := workflow()
	d.truncated = true
	c, _ := connectDesktop(t, uiConfig(), d)
	id, _ := snapshotData(t, call(t, c, "ui_snapshot", map[string]any{"window_id": "mock-window", "compact": true}, true))
	call(t, c, "ui_fill", map[string]any{"snapshot_id": id, "label": "Email", "text": "user@example.test"}, false)
	if len(d.actions()) != 0 {
		t.Fatal("partial snapshot guessed label uniqueness")
	}
}
