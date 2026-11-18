package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"example.com/codex-vision/internal/input"
	"example.com/codex-vision/internal/platform"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type WorkflowStep struct {
	Step   DesktopStep   `json:"step"`
	Expect ExpectedState `json:"expect" jsonschema:"Required observable postcondition for this step"`
}

type WorkflowArgs struct {
	RecipeID   string         `json:"recipe_id,omitempty" jsonschema:"start only: reuse an unexpired verified session recipe instead of steps; explicit window and budgets remain required"`
	Remember   bool           `json:"remember,omitempty" jsonschema:"start only, opt-in UI memory required: retain a successful click/move/wait plan in RAM for 15 minutes; no typed text or keyboard actions"`
	Detail     string         `json:"detail,omitempty" jsonschema:"full (default) or summary: omit successful journal rows, preserve current state/checkpoint and last failure. Cannot combine summary with after_event"`
	WaitMS     int            `json:"wait_ms,omitempty" jsonschema:"0..30000: wait locally for this run to finish; start/status/resume only, bounded by remaining MCP timeout. No model polling"`
	AfterEvent *int           `json:"after_event,omitempty" jsonschema:"Return journal entries after event_cursor from the previous report; start permits only 0. Omit for the legacy full journal"`
	Action     string         `json:"action" jsonschema:"start, status, cancel or resume; resumes only a failure proven to precede input"`
	ID         string         `json:"workflow_id,omitempty"`
	WindowID   string         `json:"window_id,omitempty"`
	Steps      []WorkflowStep `json:"steps,omitempty"`
	TimeoutMS  int            `json:"timeout_ms,omitempty" jsonschema:"start requires 1000..300000 total wall-clock budget, preserved across resume"`
	MaxActions int            `json:"max_actions,omitempty" jsonschema:"start requires an explicit action budget, 1..50"`
}

type WorkflowEvent struct {
	Step  int       `json:"step"`
	State string    `json:"state"`
	Code  string    `json:"code,omitempty"`
	At    time.Time `json:"at"`
}

type WorkflowReport struct {
	RecipeID           string          `json:"recipe_id,omitempty"`
	JournalMode        string          `json:"journal_mode,omitempty"`
	EventCursor        int             `json:"event_cursor"`
	ID                 string          `json:"workflow_id"`
	State              string          `json:"state"`
	Checkpoint         int             `json:"checkpoint"`
	Total              int             `json:"total_steps"`
	Attempts           int             `json:"attempts"`
	Deadline           time.Time       `json:"deadline"`
	Resumable          bool            `json:"resumable"`
	InputMayHavePosted bool            `json:"input_may_have_posted"`
	Journal            []WorkflowEvent `json:"journal"`
}

type workflowState struct {
	mu          sync.Mutex
	report      WorkflowReport
	plan        []WorkflowStep // Memory only; never returned, persisted or logged.
	window      platform.Window
	cancel      context.CancelFunc
	done        chan struct{}
	remember    bool
	memoryEpoch uint64
}

func (w *workflowState) snapshot() WorkflowReport {
	w.mu.Lock()
	defer w.mu.Unlock()
	r := w.report
	r.Journal = append([]WorkflowEvent{}, r.Journal...)
	r.EventCursor = len(r.Journal)
	return r
}

func workflowAction(step DesktopStep, windowID string) (platform.Action, error) {
	a := platform.Action{WindowID: windowID}
	if step.TargetID != "" || step.X != 0 || step.Y != 0 || step.DelayBeforeMS != 0 {
		return a, fmt.Errorf("workflow uses fresh semantic targets; coordinates, target_id and fixed delays are unsupported")
	}
	if err := validateStepTargets([]DesktopStep{step}); err != nil {
		return a, err
	}
	switch step.Action {
	case "wait":
		if step.Target != nil || step.Key != "" || len(step.Keys) > 0 || step.Text != "" || step.Delta != 0 || step.Horizontal {
			return a, fmt.Errorf("workflow wait accepts only an observable expectation")
		}
		return a, nil
	case "click", "move":
		if step.Target == nil {
			return a, fmt.Errorf("workflow click/move requires target label and optional role")
		}
		a.Tool = "mouse_" + step.Action
		a.Button, a.Count = "left", 1
	case "press":
		a.Tool, a.Keys = "keyboard_press", []string{step.Key}
	case "hotkey":
		a.Tool, a.Keys = "keyboard_hotkey", step.Keys
	case "type":
		a.Tool, a.Text = "keyboard_type", step.Text
	default:
		return a, fmt.Errorf("unsupported workflow action")
	}
	if a.Tool == "keyboard_type" {
		return a, input.ValidateText(a.Text)
	}
	if a.Keys != nil {
		return a, input.ValidateKeys(a.Keys)
	}
	return a, nil
}

func (v *Service) workflowControl(ctx context.Context, a WorkflowArgs) (*mcp.CallToolResult, error) {
	if len(a.RecipeID) > 64 {
		return nil, fmt.Errorf("recipe_id exceeds 64 bytes")
	}
	if a.Detail != "" && a.Detail != "full" && a.Detail != "summary" {
		return nil, fmt.Errorf("workflow detail must be full or summary")
	}
	if a.Detail == "summary" && a.AfterEvent != nil {
		return nil, fmt.Errorf("choose summary or after_event journal delivery")
	}
	if a.WaitMS < 0 || a.WaitMS > 30000 || (a.Action == "cancel" && a.WaitMS != 0) || (a.AfterEvent != nil && *a.AfterEvent < 0) {
		return nil, fmt.Errorf("workflow wait_ms must be 0..30000 for start/status/resume, after_event must be nonnegative")
	}
	if a.Action == "start" && a.AfterEvent != nil && *a.AfterEvent != 0 {
		return nil, fmt.Errorf("new workflow after_event must be 0")
	}
	if a.Action != "start" && (a.WindowID != "" || len(a.Steps) > 0 || a.TimeoutMS != 0 || a.MaxActions != 0 || a.RecipeID != "" || a.Remember) {
		return nil, fmt.Errorf("workflow plan and budgets apply only to start")
	}
	if a.Action == "start" {
		if a.RecipeID != "" && (len(a.Steps) > 0 || a.Remember) {
			return nil, fmt.Errorf("choose recipe_id or steps/remember")
		}
		if a.RecipeID != "" {
			if v.memory == nil {
				return nil, fmt.Errorf("workflow recipe memory unavailable")
			}
			w, err := v.agentWindow(ctx, a.WindowID)
			if err != nil {
				return nil, err
			}
			a.Steps, err = v.recipes.load(a.RecipeID, w.Process)
			if err != nil {
				return nil, err
			}
		}
		if a.ID != "" || a.WindowID == "" || a.MaxActions < 1 || a.MaxActions > 50 || len(a.Steps) < 1 || len(a.Steps) > a.MaxActions || a.TimeoutMS < 1000 || a.TimeoutMS > 300000 {
			return nil, fmt.Errorf("start requires window_id, 1..50 bounded steps, explicit max_actions and timeout_ms 1000..300000")
		}
		if v.Config.Mode != "assist" {
			return nil, fmt.Errorf("workflow input denied in observe mode")
		}
		textBytes := 0
		for _, step := range a.Steps {
			if _, err := workflowAction(step.Step, a.WindowID); err != nil {
				return nil, err
			}
			if err := validateExpectation(&step.Expect); err != nil {
				return nil, err
			}
			textBytes += len(step.Step.Text)
		}
		if textBytes > 16384 {
			return nil, fmt.Errorf("workflow text exceeds 16384 bytes")
		}
		if a.Remember {
			if v.memory == nil {
				return nil, fmt.Errorf("remember requires enabled UI memory")
			}
			if _, err := recipePlan(a.Steps); err != nil {
				return nil, err
			}
		}
		if err := v.Session.ObservationError(); err != nil {
			return nil, err
		}
		window, err := v.agentWindow(ctx, a.WindowID)
		if err != nil {
			return nil, err
		}
		id, err := opaqueID()
		if err != nil {
			return nil, err
		}
		// Own a deep copy independent of the caller's argument memory.
		encoded, err := json.Marshal(a.Steps)
		if err != nil {
			return nil, err
		}
		var plan []WorkflowStep
		if err := json.Unmarshal(encoded, &plan); err != nil {
			return nil, err
		}
		v.workflows.mu.Lock()
		if v.workflows.report.State == "running" {
			v.workflows.mu.Unlock()
			return nil, fmt.Errorf("workflow already running")
		}
		v.workflows.report = WorkflowReport{ID: id, State: "running", Total: len(plan), Deadline: time.Now().Add(time.Duration(a.TimeoutMS) * time.Millisecond)}
		v.workflows.plan, v.workflows.window = plan, window
		v.workflows.remember, v.workflows.memoryEpoch = a.Remember, v.recipes.generation()
		v.launchWorkflowLocked()
		v.workflows.mu.Unlock()
		a.ID = id
	} else {
		v.workflows.mu.Lock()
		if a.Action == "status" && a.ID == "" {
			a.ID = v.workflows.report.ID
		}
		if a.ID == "" || a.ID != v.workflows.report.ID {
			v.workflows.mu.Unlock()
			return nil, fmt.Errorf("unknown workflow_id")
		}
		if a.AfterEvent != nil && *a.AfterEvent > len(v.workflows.report.Journal) {
			v.workflows.mu.Unlock()
			return nil, fmt.Errorf("after_event exceeds current event_cursor")
		}
		switch a.Action {
		case "status":
		case "cancel":
			if v.workflows.cancel != nil {
				v.workflows.cancel()
			}
			v.workflows.report.Resumable = false
			if v.workflows.report.State != "running" {
				v.workflows.report.State = "cancelled"
				v.workflows.plan = nil
			}
		case "resume":
			if !v.workflows.report.Resumable || v.workflows.report.State == "running" || !time.Now().Before(v.workflows.report.Deadline) || v.workflows.report.Attempts >= v.workflows.report.Total+3 {
				v.workflows.mu.Unlock()
				return nil, fmt.Errorf("workflow cannot resume; inspect checkpoint without replaying uncertain input")
			}
			v.workflows.report.State, v.workflows.report.Resumable = "running", false
			v.launchWorkflowLocked()
		default:
			v.workflows.mu.Unlock()
			return nil, fmt.Errorf("workflow action must be start, status, cancel or resume")
		}
		v.workflows.mu.Unlock()
	}
	return v.workflowResult(ctx, a)
}

func (v *Service) workflowResult(ctx context.Context, a WorkflowArgs) (*mcp.CallToolResult, error) {
	v.workflows.mu.Lock()
	if v.workflows.report.ID != a.ID {
		v.workflows.mu.Unlock()
		return nil, fmt.Errorf("workflow replaced before result; inspect current status")
	}
	done, running := v.workflows.done, v.workflows.report.State == "running"
	v.workflows.mu.Unlock()
	if a.WaitMS > 0 && running && done != nil {
		budget := min(time.Duration(a.WaitMS)*time.Millisecond, time.Duration(max(0, v.Config.TimeoutMS-100))*time.Millisecond)
		if deadline, ok := ctx.Deadline(); ok {
			budget = min(budget, max(time.Duration(0), time.Until(deadline)-50*time.Millisecond))
		}
		waiting, cancel := context.WithTimeout(ctx, budget)
		select {
		case <-done:
		case <-waiting.Done():
		}
		cancel()
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	report := v.workflows.snapshot()
	if report.ID != a.ID {
		return nil, fmt.Errorf("workflow replaced while waiting; inspect current status")
	}
	if a.AfterEvent != nil {
		if *a.AfterEvent > len(report.Journal) {
			return nil, fmt.Errorf("after_event exceeds current event_cursor")
		}
		report.Journal = report.Journal[*a.AfterEvent:]
	}
	if a.Detail == "summary" {
		report.JournalMode = "summary"
		if report.State == "running" || report.State == "success" || len(report.Journal) == 0 {
			report.Journal = []WorkflowEvent{}
		} else {
			report.Journal = report.Journal[len(report.Journal)-1:]
		}
	}
	return result(report), nil
}

func (v *Service) launchWorkflowLocked() {
	ctx, cancel := context.WithDeadline(context.Background(), v.workflows.report.Deadline)
	v.workflows.cancel, v.workflows.done = cancel, make(chan struct{})
	go v.runWorkflow(ctx, cancel, v.workflows.done)
}

func (v *Service) cancelWorkflow() {
	v.workflows.mu.Lock()
	defer v.workflows.mu.Unlock()
	if v.workflows.cancel != nil {
		v.workflows.cancel()
	}
	if v.workflows.report.Resumable {
		v.workflows.report.Resumable = false
		if v.workflows.report.State != "running" {
			v.workflows.report.State = "cancelled"
			v.workflows.plan = nil
		}
	}
}

func (v *Service) runWorkflow(ctx context.Context, cancel context.CancelFunc, done chan struct{}) {
	defer cancel()
	defer close(done)
	defer v.performance.record("workflow", time.Now())
	defer func() {
		if recover() != nil {
			v.Session.Stop()
			v.workflows.mu.Lock()
			v.workflows.report.State = "failed"
			v.workflows.report.Resumable = false
			v.workflows.report.InputMayHavePosted = true
			v.workflows.plan = nil
			v.workflows.report.Journal = append(v.workflows.report.Journal, WorkflowEvent{v.workflows.report.Checkpoint, "failed", "internal_failure", time.Now().UTC()})
			v.workflows.mu.Unlock()
		}
	}()
	for {
		v.workflows.mu.Lock()
		i := v.workflows.report.Checkpoint
		if i == len(v.workflows.plan) {
			v.workflows.report.State, v.workflows.report.Resumable = "success", false
			if v.workflows.remember {
				v.workflows.report.RecipeID = v.recipes.save(v.workflows.plan, v.workflows.window.Process, v.workflows.memoryEpoch)
			}
			v.workflows.plan = nil
			v.workflows.mu.Unlock()
			return
		}
		step, window := v.workflows.plan[i], v.workflows.window
		v.workflows.report.Attempts++
		v.workflows.mu.Unlock()
		attempt, stop := context.WithTimeout(ctx, time.Duration(v.Config.TimeoutMS)*time.Millisecond)
		posted, err := v.runWorkflowStep(attempt, window, step)
		stop()
		v.workflows.mu.Lock()
		if err != nil {
			v.recovery.failure(window.ID, err.Error())
			f := classifyFailure(err)
			state := "failed"
			if f.Code == "timeout" {
				state = "timeout"
			}
			if f.Code == "ambiguous_target" {
				state = "ambiguous"
			}
			if strings.Contains(err.Error(), "approval_required") {
				state = "needs_confirmation"
			}
			if ctx.Err() == context.Canceled {
				state = "cancelled"
			}
			v.workflows.report.State = state
			v.workflows.report.InputMayHavePosted = posted
			v.workflows.report.Resumable = !posted && state != "cancelled" && time.Now().Before(v.workflows.report.Deadline) && v.workflows.report.Attempts < v.workflows.report.Total+3
			v.workflows.report.Journal = append(v.workflows.report.Journal, WorkflowEvent{i, state, f.Code, time.Now().UTC()})
			if !v.workflows.report.Resumable {
				v.workflows.plan = nil
			}
			v.workflows.mu.Unlock()
			return
		}
		v.workflows.report.Checkpoint++
		v.workflows.report.InputMayHavePosted = false
		v.workflows.report.Journal = append(v.workflows.report.Journal, WorkflowEvent{i, "success", "", time.Now().UTC()})
		v.workflows.mu.Unlock()
	}
}

func (v *Service) runWorkflowStep(ctx context.Context, pinned platform.Window, step WorkflowStep) (bool, error) {
	release, err := v.lockTool(ctx, "desktop_act")
	if err != nil {
		return false, err
	}
	defer release()
	if err := v.Session.ObservationError(); err != nil {
		return false, err
	}
	if v.recovery.status().Required {
		return false, fmt.Errorf("focus_recovery_required")
	}
	if err := v.chargeRead(); err != nil {
		return false, err
	}
	w, err := v.agentWindow(ctx, pinned.ID)
	if err != nil {
		return false, err
	}
	if w.Process != pinned.Process {
		return false, fmt.Errorf("stale workflow window process")
	}
	a, err := workflowAction(step.Step, w.ID)
	if err != nil {
		return false, err
	}
	if reader, ok := v.Desktop.(platform.UIContextReader); ok {
		var context platform.UIContext
		err := v.withLocalRead(ctx, func() error {
			var e error
			context, e = reader.ReadUIContext(ctx, w)
			return e
		})
		if err != nil {
			return false, err
		}
		a.ContextExpected = &context
	}
	spec := step.Expect
	if step.Step.Action == "wait" {
		spec.RequireTransition = ptr(false)
	}
	expected, err := v.prepareExpectation(ctx, w, &spec, 100*time.Millisecond)
	if err != nil {
		return false, err
	}
	if step.Step.Target != nil {
		raw, err := v.readControls(ctx, w, 1000)
		if err != nil {
			return false, err
		}
		if !platform.MatchUIContext(a.ContextExpected, raw.Context) {
			return false, fmt.Errorf("stale native UI context during workflow preflight")
		}
		target, _, err := v.selectControl(raw, *step.Step.Target, w)
		if err != nil {
			return false, err
		}
		a.X, a.Y = target.Bounds.X+target.Bounds.Width/2, target.Bounds.Y+target.Bounds.Height/2
		a.ControlExpected, a.ControlUniqueSelector, a.ControlMaxNodes = &target, true, 1000
		a.ControlLabelOnly = step.Step.Target.Role == ""
	}
	posted := step.Step.Action != "wait"
	if posted {
		v.views.mu.Lock()
		v.views.current = nil
		v.views.mu.Unlock()
		// A running report must not claim "no uncertain input" while native
		// execution or postcondition verification is pending. Clear only after the
		// checkpoint is verified, or replace with the proven pre-input error state.
		v.workflows.mu.Lock()
		v.workflows.report.InputMayHavePosted = true
		v.workflows.mu.Unlock()
		if _, err = v.action(ctx, a); err != nil {
			return true, err // Even native failure may mean partial delivery; never retry.
		}
	}
	for {
		if err := v.Session.ObservationError(); err != nil {
			return posted, err
		}
		if err := ctx.Err(); err != nil {
			return posted, err
		}
		matched, err := expected.observe(ctx)
		if err != nil {
			return posted, err
		}
		if matched {
			return posted, nil
		}
		if err := waitBatchDelay(ctx, 50*time.Millisecond); err != nil {
			return posted, err
		}
	}
}

func (v *Service) registerWorkflow(server *mcp.Server) {
	bind(server, v, "workflow_control", "Bounded native-UI workflow: fresh semantic targets and per-step postconditions; wait steps post no input. Prefer wait_ms/detail=summary. remember stores verified click/move/wait plans in opt-in session RAM; recipe_id reuses them with explicit window/budgets and all safety checks. No screenshots, automatic refocus or uncertain replay. Resume only pre-input failure within original deadline. Reports omit UI text and plan.", false, v.workflowControl)
}
