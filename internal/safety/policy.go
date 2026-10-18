// Package safety is the sole gate between MCP and OS input. Client text cannot grant permission.
package safety

import (
	"context"
	"example.com/codex-vision/internal/config"
	"example.com/codex-vision/internal/platform"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"sync"
	"time"
)

type Session struct {
	cfg     config.Config
	desk    platform.Desktop
	log     *slog.Logger
	gate    chan struct{}
	mu      sync.Mutex
	stopped bool
	cancel  context.CancelFunc
	total   int
	recent  []time.Time
}

func New(c config.Config, d platform.Desktop, l *slog.Logger) *Session {
	c.Allow = slices.Clone(c.Allow)
	c.AllowedProcesses = slices.Clone(c.AllowedProcesses)
	return &Session{cfg: c, desk: d, log: l, gate: make(chan struct{}, 1)}
}
func (s *Session) Stop() {
	s.mu.Lock()
	s.stopped = true
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()
	s.log.Info("audit", "tool", "session_stop", "decision", "stop", "code", "ok")
}
func (s *Session) checkStopLocked() error {
	if s.stopped {
		return fmt.Errorf("session stopped; restart locally to resume")
	}
	_, e := os.Stat(s.cfg.StopFile)
	if e == nil || !os.IsNotExist(e) {
		s.stopped = true
		if s.cancel != nil {
			s.cancel()
		}
		return fmt.Errorf("local stop file present or inaccessible; input disabled")
	}
	return nil
}
func (s *Session) Status() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.checkStopLocked()
	permitted := []string{}
	if !s.stopped && s.cfg.Mode == "assist" {
		for _, tool := range s.cfg.Allow {
			if platform.Supports(s.desk, tool) && s.approvalAvailable(tool) {
				permitted = append(permitted, tool)
			}
		}
	}
	return map[string]any{"mode": s.cfg.Mode, "cursor_mode": s.cfg.CursorMode, "backend": s.desk.Name(), "stopped": s.stopped, "permitted_input": permitted, "actions_used": s.total, "max_actions_per_task": s.cfg.MaxPerTask, "max_actions_per_minute": s.cfg.MaxPerMinute, "task_budget_unlimited": s.cfg.MaxPerTask == 0, "rate_limit_unlimited": s.cfg.MaxPerMinute == 0, "local_approval": s.cfg.LocalApproval, "approval_ui_available": s.hasApprover(), "confirm_each_action": s.cfg.ConfirmEachAction, "stop_file": s.cfg.StopFile, "screen_text": "untrusted data"}
}
func (s *Session) Watch(ctx context.Context) {
	t := time.NewTicker(25 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			s.Stop()
			return
		case <-t.C:
			s.mu.Lock()
			_ = s.checkStopLocked()
			s.mu.Unlock()
		}
	}
}
func (s *Session) limitsLocked(now time.Time) error {
	cut := now.Add(-time.Minute)
	i := 0
	for i < len(s.recent) && !s.recent[i].After(cut) {
		i++
	}
	s.recent = s.recent[i:]
	if s.cfg.MaxPerTask > 0 && s.total >= s.cfg.MaxPerTask {
		return fmt.Errorf("task action budget exhausted; local restart required")
	}
	if s.cfg.MaxPerMinute > 0 && len(s.recent) >= s.cfg.MaxPerMinute {
		return fmt.Errorf("action rate exceeded; retry after one minute")
	}
	if s.cfg.MaxPerMinute > 0 {
		s.recent = append(s.recent, now)
	}
	s.total++
	return nil
}
func (s *Session) Execute(ctx context.Context, a platform.Action) (err error) {
	defer func() {
		code := "ok"
		if err != nil {
			code = "denied_or_failed"
		}
		s.log.Info("audit", "tool", a.Tool, "decision", code, "code", code)
	}()
	ctx, cancel := context.WithTimeout(ctx, time.Duration(s.cfg.TimeoutMS)*time.Millisecond)
	defer cancel()
	select {
	case s.gate <- struct{}{}:
		defer func() { <-s.gate }()
	case <-ctx.Done():
		return ctx.Err()
	}
	s.mu.Lock()
	if err = s.checkStopLocked(); err == nil && s.cfg.Mode != "assist" {
		err = fmt.Errorf("input denied in observe mode")
	}
	if err == nil {
		switch a.Tool {
		case "mouse_move", "mouse_scroll", "window_focus":
		case "mouse_click", "mouse_drag", "keyboard_type", "keyboard_press", "keyboard_hotkey", "app_launch", "ui_fill":
			if !s.approvalAvailable(a.Tool) {
				err = fmt.Errorf("approval_required: input needs local_approval; per-action mode additionally needs a supported local review UI")
			}
		default:
			err = fmt.Errorf("unsupported input action")
		}
	}
	if err == nil && !platform.Supports(s.desk, a.Tool) {
		err = fmt.Errorf("capability unsupported by active backend")
	}
	if err == nil && !slices.Contains(s.cfg.Allow, a.Tool) {
		err = fmt.Errorf("capability is not locally allowlisted")
	}
	if err == nil {
		err = s.limitsLocked(time.Now())
	}
	if err == nil {
		s.cancel = cancel
	}
	s.mu.Unlock()
	if err != nil {
		return err
	}
	defer func() {
		s.mu.Lock()
		s.cancel = nil
		s.mu.Unlock()
		if e := s.desk.Release(); err == nil {
			err = e
		}
	}()
	var w platform.Window
	var e error
	if a.Tool == "app_launch" {
		w = platform.Window{Process: a.Process, Title: "Launch application"}
	} else if a.Tool == "window_focus" {
		// Focus authorizes the destination, even if the current application has
		// no visible window or is outside the input allowlist.
		ws, err := s.desk.Windows(ctx)
		if err != nil {
			return err
		}
		for _, target := range ws {
			if target.ID == a.WindowID {
				w = target
				break
			}
		}
		if w.ID == "" {
			return fmt.Errorf("target window unavailable; refresh window_list")
		}
	} else {
		w, e = s.desk.Active(ctx)
		if e != nil {
			return e
		}
		if a.WindowID != "" && a.WindowID != w.ID {
			return fmt.Errorf("target window is not foreground; call window_focus and verify before input")
		}
	}
	if _, e := s.allowedProcess(ctx, w); e != nil {
		return e
	}
	if (a.Tool == "mouse_move" || a.Tool == "mouse_click" || a.Tool == "mouse_drag") && !w.Bounds.Contains(a.X, a.Y) {
		return fmt.Errorf("cursor target outside allowed foreground window")
	}
	if a.Tool == "mouse_drag" && !w.Bounds.Contains(a.EndX, a.EndY) {
		return fmt.Errorf("drag endpoint outside allowed foreground window")
	}
	if platform.NeedsApproval(a.Tool) {
		if s.cfg.ConfirmEachAction {
			if e = s.desk.(platform.LocalApprover).Approve(ctx, a, w); e != nil {
				return e
			}
		}
		// Launch does not inject into the foreground; its destination may not
		// exist yet. All keyboard/mouse actions retain the foreground check.
		if a.Tool != "app_launch" {
			// The review dialog may change focus. Never inject into a different window.
			current, e := s.desk.Active(ctx)
			if e != nil {
				return e
			}
			if current.ID != w.ID || !processEqual(current.Process, w.Process) || current.Bounds != w.Bounds {
				return fmt.Errorf("foreground window changed before input; retry after restoring target")
			}
		}
	}
	s.mu.Lock()
	err = s.checkStopLocked()
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	// Preserve the policy-authorized window throughout smooth approach and
	// drag, even when the client omitted the optional expected window ID.
	if a.Tool == "mouse_move" || a.Tool == "mouse_click" || a.Tool == "mouse_drag" {
		a.WindowID = w.ID
	}
	return s.desk.Execute(ctx, a)
}

func processEqual(a, b string) bool { return platform.ProcessEqual(a, b) }

// Raw paths remain visible. Only a native verifier may recognize a signed
// OS-managed clone as one of the locally configured executables.
func (s *Session) allowedProcess(ctx context.Context, w platform.Window) (string, error) {
	for _, expected := range s.cfg.AllowedProcesses {
		if processEqual(expected, w.Process) {
			return expected, nil
		}
	}
	var failure error
	for _, expected := range s.cfg.AllowedProcesses {
		ok, err := platform.ProcessMatches(ctx, s.desk, w, expected)
		if err != nil {
			failure = err
			continue
		}
		if ok {
			return expected, nil
		}
	}
	if failure != nil {
		return "", fmt.Errorf("target process %q is not locally allowlisted: %w", w.Process, failure)
	}
	return "", fmt.Errorf("target process %q is not locally allowlisted", w.Process)
}

// DescribeWindows reports process authorization separately from visibility.
// It does not grant input, skip OS permission checks, or hide denied windows.
func (s *Session) DescribeWindows(ctx context.Context, ws []platform.Window) ([]platform.Window, error) {
	out := slices.Clone(ws)
	for i := range out {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		path, err := s.allowedProcess(ctx, out[i])
		allowed := err == nil
		out[i].ProcessAllowed = &allowed
		out[i].AllowedProcess = path
		out[i].IdentityError = ""
		if err != nil {
			out[i].IdentityError = err.Error()
		}
	}
	return out, ctx.Err()
}

func (s *Session) hasApprover() bool { _, ok := s.desk.(platform.LocalApprover); return ok }
func (s *Session) approvalAvailable(tool string) bool {
	return !platform.NeedsApproval(tool) || (s.cfg.LocalApproval && (!s.cfg.ConfirmEachAction || s.hasApprover()))
}

// ObservationError checks the local emergency stop without authorizing input.
func (s *Session) ObservationError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.checkStopLocked()
}
