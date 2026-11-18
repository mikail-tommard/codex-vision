package mcpserver

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func reusablePlan() []WorkflowStep {
	return []WorkflowStep{{Step: DesktopStep{Action: "click", Target: &ControlSelector{Label: "Execute", Role: "AXButton"}}, Expect: ExpectedState{TitleContains: "Saved", RequireTransition: ptr(false)}}}
}

func TestVerifiedRecipeReusesPlanWithFreshTargetsAndSmallerRequest(t *testing.T) {
	d := agentFixture("Saved")
	cfg := uiConfig()
	cfg.Workflows = true
	cfg.Allow = []string{"mouse_click"}
	cfg.UIMemoryDirectory, cfg.UIMemoryNamespace, cfg.UIMemoryTTLHours = filepath.Join(t.TempDir(), "private"), "recipes", 1
	c, svc := connectProfileDesktop(t, cfg, d, "compact")
	args := map[string]any{"action": "start", "window_id": "mock-window", "steps": reusablePlan(), "remember": true, "max_actions": 1, "timeout_ms": 5000, "wait_ms": 1000, "detail": "summary"}
	r := decodeWorkflow(t, fieldsData(t, call(t, c, "workflow_control", args, true)))
	if r.State != "success" || r.RecipeID == "" || svc.recipes.count() != 1 {
		t.Fatal(r)
	}
	d.stateMu.Lock()
	d.offset = 30
	d.stateMu.Unlock()
	replay := map[string]any{"action": "start", "window_id": "mock-window", "recipe_id": r.RecipeID, "max_actions": 1, "timeout_ms": 5000, "wait_ms": 1000, "detail": "summary"}
	second := decodeWorkflow(t, fieldsData(t, call(t, c, "workflow_control", replay, true)))
	if second.State != "success" || len(d.actions()) != 2 || d.actions()[1].X != 120 {
		t.Fatal("recipe used stale coordinates", second, d.actions())
	}
	baseline := make(map[string]any, len(args))
	for key, value := range args {
		if key != "remember" {
			baseline[key] = value
		}
	}
	before, _ := json.Marshal(baseline)
	after, _ := json.Marshal(replay)
	if len(after) >= len(before) {
		t.Fatal("recipe did not shorten request", len(before), len(after))
	}
	t.Logf("RECIPE_RESULT before_request_bytes=%d after_request_bytes=%d images_each=0 scope=synthetic_arguments_not_tokens", len(before), len(after))
	call(t, c, "ui_memory_control", map[string]any{"action": "clear"}, true)
	call(t, c, "workflow_control", replay, false)
}

func TestRecipeBoundsExpiryIsolationAndRevocation(t *testing.T) {
	var c recipeCache
	defer c.clear()
	epoch := c.generation()
	first := c.save(reusablePlan(), "app", epoch)
	if first == "" {
		t.Fatal("save failed")
	}
	if _, err := c.load(first, "other"); err == nil {
		t.Fatal("wrong app reused recipe")
	}
	copy, err := c.load(first, "app")
	if err != nil {
		t.Fatal(err)
	}
	copy[0].Step.Target.Label = "mutated"
	again, _ := c.load(first, "app")
	if again[0].Step.Target.Label != "Execute" {
		t.Fatal("caller mutated recipe")
	}
	c.mu.Lock()
	e := c.entries[first]
	e.expires = time.Now().Add(-time.Second)
	c.entries[first] = e
	c.mu.Unlock()
	if _, err := c.load(first, "app"); err == nil {
		t.Fatal("expired recipe reused")
	}
	for i := 0; i < maxRecipes+2; i++ {
		c.save(reusablePlan(), "app", epoch)
	}
	if c.count() != maxRecipes {
		t.Fatal("unbounded recipes")
	}
	c.clear()
	if c.save(reusablePlan(), "app", epoch) != "" || c.count() != 0 {
		t.Fatal("clear did not revoke in-flight learning")
	}
	var other recipeCache
	if _, err := other.load(first, "app"); err == nil {
		t.Fatal("cross-session recipe accepted")
	}
}

func TestRecipeRejectsTypedValuesAndUnverifiedPlans(t *testing.T) {
	for _, action := range []string{"type", "press", "hotkey", "click"} {
		p := reusablePlan()
		p[0].Step.Action = action
		p[0].Step.Text = "secret"
		if _, err := recipePlan(p); err == nil {
			t.Fatal("private text retained", action)
		}
	}
	d := agentFixture("Saved")
	cfg := uiConfig()
	cfg.Workflows = true
	cfg.Allow = []string{"mouse_click"}
	c, svc := connectDesktop(t, cfg, d)
	args := map[string]any{"action": "start", "window_id": "mock-window", "steps": reusablePlan(), "remember": true, "max_actions": 1, "timeout_ms": 1000}
	call(t, c, "workflow_control", args, false)
	if len(d.actions()) != 0 || svc.recipes.count() != 0 {
		t.Fatal("disabled memory learned or posted")
	}
}

func TestWorkflowWaitUsesNoInputAndCanTimeoutSafely(t *testing.T) {
	d := agentFixture("Saved")
	cfg := uiConfig()
	cfg.Workflows = true
	cfg.TimeoutMS = 300
	c, svc := connectDesktop(t, cfg, d)
	plan := []WorkflowStep{{Step: DesktopStep{Action: "wait"}, Expect: ExpectedState{ControlLabel: "Execute", ControlRole: "AXButton", Enabled: ptr(true)}}}
	args := map[string]any{"action": "start", "window_id": "mock-window", "steps": plan, "max_actions": 1, "timeout_ms": 2000, "wait_ms": 1000}
	call(t, c, "workflow_control", args, true)
	r := waitWorkflow(t, svc)
	if r.State != "success" || r.InputMayHavePosted || len(d.actions()) != 0 {
		t.Fatal(r)
	}
	plan[0].Expect.ControlLabel = "missing"
	call(t, c, "workflow_control", args, true)
	r = waitWorkflow(t, svc)
	if r.State != "timeout" || !r.Resumable || r.InputMayHavePosted || len(d.actions()) != 0 {
		t.Fatal(r)
	}
}

func TestRecipeReplayRejectsAmbiguousFreshTargetAndFailedRunDoesNotLearn(t *testing.T) {
	d := &selectorDesktop{agentDesktop: agentFixture("Saved"), mode: "unique"}
	cfg := uiConfig()
	cfg.Workflows = true
	cfg.Allow = []string{"mouse_click"}
	cfg.TimeoutMS = 400
	cfg.UIMemoryDirectory, cfg.UIMemoryNamespace, cfg.UIMemoryTTLHours = filepath.Join(t.TempDir(), "private"), "recipes", 1
	c, svc := connectDesktop(t, cfg, d)
	a := map[string]any{"action": "start", "window_id": "mock-window", "steps": reusablePlan(), "remember": true, "max_actions": 1, "timeout_ms": 3000, "wait_ms": 1000}
	call(t, c, "workflow_control", a, true)
	first := waitWorkflow(t, svc)
	if first.RecipeID == "" {
		t.Fatal(first)
	}
	d.mode = "duplicate"
	call(t, c, "workflow_control", map[string]any{"action": "start", "window_id": "mock-window", "recipe_id": first.RecipeID, "max_actions": 1, "timeout_ms": 3000, "wait_ms": 1000}, true)
	r := waitWorkflow(t, svc)
	if r.State != "ambiguous" || r.InputMayHavePosted || len(d.actions()) != 1 {
		t.Fatal("recipe bypassed uniqueness", r, d.actions())
	}
	call(t, c, "ui_memory_control", map[string]any{"action": "clear"}, true)
	d.mode = "unique"
	plan := reusablePlan()
	plan[0].Expect = ExpectedState{TitleContains: "never appears"}
	a["steps"] = plan
	call(t, c, "workflow_control", a, true)
	r = waitWorkflow(t, svc)
	if r.State != "timeout" || r.RecipeID != "" || svc.recipes.count() != 0 {
		t.Fatal("unverified run learned recipe", r)
	}
}
