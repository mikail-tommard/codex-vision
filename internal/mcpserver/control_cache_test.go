package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"example.com/codex-vision/internal/platform"
)

type discoveryDesktop struct {
	*countedControlsDesktop
	contextVersion atomic.Int64
	denied         atomic.Bool
	duringRead     func()
}

func (d *discoveryDesktop) ReadUIContext(ctx context.Context, _ platform.Window) (platform.UIContext, error) {
	if d.denied.Load() {
		return platform.UIContext{}, fmt.Errorf("accessibility_permission_denied")
	}
	return platform.UIContext{ID: fmt.Sprint(d.contextVersion.Load()), Kind: "test"}, ctx.Err()
}
func (d *discoveryDesktop) ReadControls(ctx context.Context, w platform.Window, n int) (platform.ControlSnapshot, error) {
	if d.duringRead != nil {
		d.duringRead()
	}
	raw, err := d.countedControlsDesktop.ReadControls(ctx, w, n)
	if err != nil {
		return raw, err
	}
	c, e := d.ReadUIContext(ctx, w)
	raw.Context = &c
	for i := 1; i < min(n, 100); i++ {
		control := raw.Controls[0]
		control.Label = fmt.Sprintf("Control %d", i)
		raw.Controls = append(raw.Controls, control)
	}
	return raw, e
}

func discoveryFixture() *discoveryDesktop {
	return &discoveryDesktop{countedControlsDesktop: &countedControlsDesktop{agentDesktop: agentFixture("Saved")}}
}

func TestDiscoveryRevisionAndCachePairedBytes(t *testing.T) {
	sizes := map[string]int{}
	for _, strategy := range []string{"fresh_full", "revision_cache"} {
		d := discoveryFixture()
		cfg := uiConfig()
		c, svc := connectProfileDesktop(t, cfg, d, "compact")
		a := map[string]any{"window_id": "mock-window"}
		var revision string
		for i := 0; i < 5; i++ {
			r := call(t, c, "ui_controls", a, true)
			data := fieldsData(t, r)
			if len(r.Content) != 1 {
				t.Fatal("discovery returned image")
			}
			encoded, _ := json.Marshal(r)
			sizes[strategy] += len(encoded)
			if i == 0 {
				revision = data["revision"].(string)
			}
			if strategy == "revision_cache" {
				if i > 0 && (data["changed"] != false || data["cached"] != true || data["controls"] != nil) {
					t.Fatal(data)
				}
				a["since_revision"], a["max_age_ms"] = revision, 2000
			}
		}
		want := int64(5)
		if strategy == "revision_cache" {
			want = 1
		}
		if d.reads.Load() != want {
			t.Fatal("native read count", d.reads.Load(), want)
		}
		// Readiness/verification reads bypass discovery even while its entry is live.
		w, err := svc.agentWindow(context.Background(), "mock-window")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.readControls(context.Background(), w, 1000); err != nil {
			t.Fatal(err)
		}
		if d.reads.Load() != want+1 {
			t.Fatal("safety read reused cache")
		}
	}
	if sizes["revision_cache"]*2 >= sizes["fresh_full"] {
		t.Fatal("duplicate payload not reduced", sizes)
	}
	t.Logf("DISCOVERY_RESULT before_bytes=%d after_bytes=%d native_reads_before=5 native_reads_after=1 calls_each=5 images_each=0 scope=synthetic_CallToolResult_not_tokens", sizes["fresh_full"], sizes["revision_cache"])
}

func TestDiscoveryFreshnessInvalidationAndPermission(t *testing.T) {
	d := discoveryFixture()
	cfg := uiConfig()
	cfg.Allow = []string{"keyboard_press"}
	c, svc := connectDesktop(t, cfg, d)
	a := map[string]any{"window_id": "mock-window", "max_age_ms": 2000}
	first := fieldsData(t, call(t, c, "ui_controls", a, true))
	a["since_revision"] = first["revision"]
	call(t, c, "ui_controls", a, true)
	if d.reads.Load() != 1 {
		t.Fatal("cache not reused")
	}
	d.contextVersion.Add(1)
	if out := fieldsData(t, call(t, c, "ui_controls", a, true)); out["changed"] != true || out["cached"] != false {
		t.Fatal("context was cached", out)
	}
	call(t, c, "keyboard_press", map[string]any{"window_id": "mock-window", "key": "SPACE"}, true)
	if out := fieldsData(t, call(t, c, "ui_controls", a, true)); out["cached"] != false {
		t.Fatal("input did not invalidate", out)
	}
	svc.discovery.mu.Lock()
	svc.discovery.entry.at = time.Now().Add(-3 * time.Second)
	svc.discovery.mu.Unlock()
	if out := fieldsData(t, call(t, c, "ui_controls", a, true)); out["cached"] != false {
		t.Fatal("expired cache reused", out)
	}
	d.denied.Store(true)
	call(t, c, "ui_controls", a, false)
	d.denied.Store(false)
	if out := fieldsData(t, call(t, c, "ui_controls", a, true)); out["cached"] != false {
		t.Fatal("permission failure retained entry", out)
	}
	call(t, c, "ui_controls", map[string]any{"window_id": "other", "max_age_ms": 2000}, false)
	if out := fieldsData(t, call(t, c, "ui_controls", a, true)); out["cached"] != false {
		t.Fatal("focus failure retained entry", out)
	}
	call(t, c, "ui_controls", map[string]any{"window_id": "mock-window", "max_age_ms": 2001}, false)
	call(t, c, "session_stop", map[string]any{}, true)
	call(t, c, "ui_controls", a, false)
}

func TestDiscoveryRevisionWithoutCacheStillReadsNative(t *testing.T) {
	d := discoveryFixture()
	c, _ := connectDesktop(t, uiConfig(), d)
	a := map[string]any{"window_id": "mock-window"}
	first := fieldsData(t, call(t, c, "ui_controls", a, true))
	a["since_revision"] = first["revision"]
	second := fieldsData(t, call(t, c, "ui_controls", a, true))
	if d.reads.Load() != 2 || second["changed"] != false || second["cached"] != false || second["controls"] != nil {
		t.Fatal(second, d.reads.Load())
	}
}

func TestDiscoveryConcurrentInvalidationCannotRepopulateCache(t *testing.T) {
	d := discoveryFixture()
	c, svc := connectDesktop(t, uiConfig(), d)
	d.duringRead = svc.discovery.invalidate
	call(t, c, "ui_controls", map[string]any{"window_id": "mock-window", "max_age_ms": 2000}, false)
	svc.discovery.mu.Lock()
	entry := svc.discovery.entry
	svc.discovery.mu.Unlock()
	if entry != nil {
		t.Fatal("invalidated observation was republished")
	}
}

func TestViewRevisionCompactBytes(t *testing.T) {
	d := discoveryFixture()
	c, _ := connectProfileDesktop(t, uiConfig(), d, "compact")
	args := map[string]any{"include_controls": true}
	first := call(t, c, "desktop_observe", args, true)
	args["after_sequence"] = agentResult(t, first).Frame.Sequence
	full := call(t, c, "desktop_observe", args, true)
	args["since_controls_revision"] = fieldsData(t, full)["controls_revision"]
	short := call(t, c, "desktop_observe", args, true)
	fullBytes, _ := json.Marshal(full)
	shortBytes, _ := json.Marshal(short)
	if len(short.Content) != 1 || len(full.Content) != 1 || len(shortBytes)*2 >= len(fullBytes) || d.reads.Load() != 3 {
		t.Fatal(len(fullBytes), len(shortBytes), d.reads.Load())
	}
	t.Logf("repeat observe: full=%d bytes, revision=%d bytes", len(fullBytes), len(shortBytes))
	d.contextVersion.Add(1)
	refreshed := fieldsData(t, call(t, c, "desktop_observe", args, true))
	if refreshed["controls_changed"] != true || refreshed["controls_revision"] == args["since_controls_revision"] {
		t.Fatal("context change reused target IDs")
	}
}
