package mcpserver

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"example.com/codex-vision/internal/config"
	"example.com/codex-vision/internal/observation"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestRecentDiagnosticsAreBoundedAndContentFree(t *testing.T) {
	var metrics usageMetrics
	out := fail(fmt.Errorf("cannot read focused application (native error -25204); secret-page-text"))
	out.Content = append(out.Content, &mcp.ImageContent{Data: []byte("secret-image-data"), MIMEType: "image/png"})
	metrics.record("desktop_act", out, 12*time.Millisecond)
	recent := metrics.recentSnapshot()
	if len(recent) != 1 || recent[0].Reason != "active_window_unavailable" || recent[0].NativeError == nil || *recent[0].NativeError != -25204 || recent[0].Outcome != "error" || recent[0].Images != 1 {
		t.Fatal(recent)
	}
	encoded, _ := json.Marshal(recent)
	if strings.Contains(string(encoded), "secret-") {
		t.Fatal("UI/error/image content retained", string(encoded))
	}
	timed := observation.ActionWaitResult{Posted: true, TimedOut: true, Stage: "waiting_own_change", Timings: observation.InteractionTimings{WaitMS: 2500}}
	metrics.record("desktop_act", result(map[string]any{"interaction": timed, "view_consumed": true, "completed_steps": 1}), 3*time.Second)
	recent = metrics.recentSnapshot()
	last := recent[1]
	if last.Outcome != "timeout" || !last.Posted || last.ViewConsumed == nil || !*last.ViewConsumed || last.Timings == nil || last.Timings.WaitMS != 2500 || last.GapMS == nil {
		t.Fatal(last)
	}
	for i := 0; i < 20; i++ {
		metrics.record("window_list", result(map[string]any{"title": "secret-title"}), time.Millisecond)
	}
	recent = metrics.recentSnapshot()
	if len(recent) != 12 || recent[0].Tool != "window_list" {
		t.Fatal("history not bounded", recent)
	}
	encoded, _ = json.Marshal(recent)
	if strings.Contains(string(encoded), "secret-") {
		t.Fatal("title retained", string(encoded))
	}
}

func TestStatusDiagnosticsRequireExplicitRequest(t *testing.T) {
	c, _ := connect(t, config.Default())
	plain := call(t, c, "session_status", map[string]any{}, true)
	var meta map[string]json.RawMessage
	if err := json.Unmarshal([]byte(plain.Content[0].(*mcp.TextContent).Text), &meta); err != nil {
		t.Fatal(err)
	}
	if _, ok := meta["recent_calls"]; ok {
		t.Fatal("default status includes unnecessary history")
	}
	call(t, c, "desktop_act", map[string]any{"view_id": "unknown", "steps": []map[string]any{{"action": "press", "key": "SPACE"}}}, false)
	detail := call(t, c, "session_status", map[string]any{"include_recent": true}, true)
	if err := json.Unmarshal([]byte(detail.Content[0].(*mcp.TextContent).Text), &meta); err != nil {
		t.Fatal(err)
	}
	var recent []callDiagnostic
	if err := json.Unmarshal(meta["recent_calls"], &recent); err != nil {
		t.Fatal(err)
	}
	if len(recent) != 2 || recent[1].Tool != "desktop_act" || recent[1].FailureCode != "stale_reference" || recent[1].Reason != "stale_reference" {
		t.Fatal(recent)
	}
}

func TestFocusAmbiguityDiagnosticsContainOnlyCounts(t *testing.T) {
	var metrics usageMetrics
	message := "cannot uniquely match window to Accessibility element; refusing focus (AX windows=3, bounds matches=2, title matches=0, unreadable bounds=1, AX error=0, activation attempted=yes); secret-window-title"
	failure := fail(fmt.Errorf("%s", message))
	metrics.record("window_focus", failure, time.Millisecond)
	diagnostic := metrics.recentSnapshot()[0]
	if diagnostic.FailureCode != "ambiguous_target" || diagnostic.Reason != "window_match_ambiguous" || diagnostic.FocusMatch == nil || diagnostic.FocusMatch.Windows != 3 || diagnostic.FocusMatch.BoundsMatches != 2 || diagnostic.FocusMatch.TitleMatches != 0 || diagnostic.NativeError == nil || *diagnostic.NativeError != 0 {
		t.Fatal(diagnostic)
	}
	encoded, _ := json.Marshal(diagnostic)
	if strings.Contains(string(encoded), "secret-window-title") {
		t.Fatal("title leaked", string(encoded))
	}
	if !focusFailure("focused Accessibility window matches 2 Quartz windows; refusing ambiguous foreground") {
		t.Fatal("ambiguous foreground is not guarded")
	}
}
