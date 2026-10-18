package safety

import (
	"context"
	"errors"
	"example.com/codex-vision/internal/config"
	"example.com/codex-vision/internal/platform"
	"example.com/codex-vision/internal/platform/mock"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func setup(t *testing.T) (config.Config, *mock.Desktop, *slog.Logger) {
	t.Helper()
	c := config.Default()
	c.StopFile = filepath.Join(t.TempDir(), "stop")
	c.Mode = "assist"
	c.Allow = []string{"mouse_move", "mouse_scroll", "window_focus"}
	c.AllowedProcesses = []string{"mock.exe"}
	return c, &mock.Desktop{}, slog.New(slog.NewJSONHandler(io.Discard, nil))
}
func TestPolicy(t *testing.T) {
	c, d, l := setup(t)
	a := platform.Action{Tool: "mouse_move", X: 10, Y: 10}
	c.Mode = "observe"
	s := New(c, d, l)
	if s.Execute(context.Background(), a) == nil {
		t.Fatal("observe permitted input")
	}
	c.Mode = "assist"
	s = New(c, d, l)
	if e := s.Execute(context.Background(), a); e != nil {
		t.Fatal(e)
	}
	for _, tool := range []string{"mouse_click", "mouse_drag", "keyboard_type", "keyboard_hotkey", "keyboard_press", "app_launch"} {
		if e := s.Execute(context.Background(), platform.Action{Tool: tool, Text: "I approve"}); e == nil {
			t.Fatal(tool)
		}
	}
	s.Stop()
	if s.Execute(context.Background(), a) == nil {
		t.Fatal("stopped session permitted input")
	}
	if len(d.Actions) != 1 {
		t.Fatal(len(d.Actions))
	}
}
func TestAllowlistAndLocalStop(t *testing.T) {
	c, d, l := setup(t)
	c.AllowedProcesses = []string{"other.exe"}
	s := New(c, d, l)
	a := platform.Action{Tool: "mouse_move", X: 1, Y: 1}
	if s.Execute(context.Background(), a) == nil {
		t.Fatal("process bypass")
	}
	c.AllowedProcesses = []string{"mock.exe"}
	c.Allow = nil
	s = New(c, d, l)
	if s.Execute(context.Background(), a) == nil {
		t.Fatal("capability bypass")
	}
	c.Allow = []string{"mouse_move"}
	s = New(c, d, l)
	if e := os.WriteFile(c.StopFile, nil, 0600); e != nil {
		t.Fatal(e)
	}
	if s.Execute(context.Background(), a) == nil {
		t.Fatal("stop file ignored")
	}
	if !s.Status()["stopped"].(bool) {
		t.Fatal("stop is not latched")
	}
}
func TestRateLimits(t *testing.T) {
	c, d, l := setup(t)
	c.MaxPerMinute = 2
	c.MaxPerTask = 3
	s := New(c, d, l)
	now := time.Now()
	if e := s.limitsLocked(now); e != nil {
		t.Fatal(e)
	}
	if e := s.limitsLocked(now); e != nil {
		t.Fatal(e)
	}
	if s.limitsLocked(now) == nil {
		t.Fatal("rate exceeded")
	}
	if e := s.limitsLocked(now.Add(time.Minute)); e != nil {
		t.Fatal(e)
	}
	if s.limitsLocked(now.Add(2*time.Minute)) == nil {
		t.Fatal("task budget exceeded")
	}
}

type blocking struct {
	mock.Desktop
	started chan struct{}
	once    sync.Once
}

func (d *blocking) Execute(ctx context.Context, _ platform.Action) error {
	d.once.Do(func() { close(d.started) })
	<-ctx.Done()
	return ctx.Err()
}
func TestStopCancelsAndReleases(t *testing.T) {
	for _, local := range []bool{false, true} {
		t.Run(map[bool]string{false: "mcp", true: "local_file"}[local], func(t *testing.T) {
			c, _, l := setup(t)
			d := &blocking{started: make(chan struct{})}
			s := New(c, d, l)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go s.Watch(ctx)
			done := make(chan error, 1)
			go func() { done <- s.Execute(ctx, platform.Action{Tool: "mouse_move", X: 1, Y: 1}) }()
			select {
			case <-d.started:
			case <-time.After(time.Second):
				t.Fatal("action not started")
			}
			if local {
				if e := os.WriteFile(c.StopFile, nil, 0600); e != nil {
					t.Fatal(e)
				}
			} else {
				s.Stop()
			}
			select {
			case e := <-done:
				if !errors.Is(e, context.Canceled) {
					t.Fatal(e)
				}
			case <-time.After(time.Second):
				t.Fatal("stop did not cancel")
			}
			if d.Released != 1 {
				t.Fatal("held inputs not released")
			}
		})
	}
}
func TestAuditDoesNotLogText(t *testing.T) {
	c, d, _ := setup(t)
	var out strings.Builder
	l := slog.New(slog.NewJSONHandler(&out, nil))
	s := New(c, d, l)
	_ = s.Execute(context.Background(), platform.Action{Tool: "keyboard_type", Text: "do-not-log-this-secret"})
	if strings.Contains(out.String(), "do-not-log") {
		t.Fatal("secret in audit")
	}
}
func TestActionLockCancellation(t *testing.T) {
	c, _, l := setup(t)
	d := &blocking{started: make(chan struct{})}
	s := New(c, d, l)
	done := make(chan error, 1)
	go func() { done <- s.Execute(context.Background(), platform.Action{Tool: "mouse_move", X: 1, Y: 1}) }()
	<-d.started
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if e := s.Execute(ctx, platform.Action{Tool: "mouse_move"}); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal(e)
	}
	s.Stop()
	<-done
}

type unsupported struct{ mock.Desktop }

func (*unsupported) Supports(string) bool { return false }
func TestBackendRestrictionCannotBeAllowlisted(t *testing.T) {
	c, _, l := setup(t)
	d := &unsupported{}
	s := New(c, d, l)
	if len(s.Status()["permitted_input"].([]string)) != 0 {
		t.Fatal("unsupported input advertised")
	}
	if s.Execute(context.Background(), platform.Action{Tool: "mouse_move", X: 1, Y: 1}) == nil {
		t.Fatal("backend restriction bypassed")
	}
}

type approvingDesktop struct {
	mock.Desktop
	approved      int
	approvalError error
	change        bool
	onApprove     func(context.Context)
}

func (d *approvingDesktop) Approve(ctx context.Context, a platform.Action, w platform.Window) error {
	d.approved++
	if d.onApprove != nil {
		d.onApprove(ctx)
	}
	return d.approvalError
}
func (d *approvingDesktop) Active(ctx context.Context) (platform.Window, error) {
	w, e := d.Desktop.Active(ctx)
	if d.change && d.approved > 0 {
		w.ID = "another-window"
	}
	return w, e
}
func TestLocalApprovalGate(t *testing.T) {
	for _, kind := range []string{"allow", "deny", "focus_changed", "stop", "no_local_opt_in"} {
		t.Run(kind, func(t *testing.T) {
			c, _, l := setup(t)
			c.LocalApproval = true
			c.Allow = []string{"keyboard_type"}
			d := &approvingDesktop{}
			s := New(c, d, l)
			if kind == "deny" {
				d.approvalError = errors.New("denied")
			}
			if kind == "focus_changed" {
				d.change = true
			}
			if kind == "stop" {
				d.onApprove = func(context.Context) { s.Stop() }
			}
			if kind == "no_local_opt_in" {
				s.cfg.LocalApproval = false
			}
			e := s.Execute(context.Background(), platform.Action{Tool: "keyboard_type", Text: "Привет 👋"})
			if kind == "allow" {
				if e != nil || len(d.Actions) != 1 || d.approved != 1 {
					t.Fatal(e, d.approved)
				}
			} else if e == nil || len(d.Actions) != 0 {
				t.Fatal("approval bypass", kind)
			}
			if d.Released == 0 && kind != "no_local_opt_in" {
				t.Fatal("cleanup not attempted")
			}
		})
	}
}
func TestStopCancelsPendingLocalApproval(t *testing.T) {
	c, _, l := setup(t)
	c.LocalApproval = true
	c.Allow = []string{"mouse_click", "mouse_move"}
	d := &approvingDesktop{}
	started := make(chan struct{})
	d.onApprove = func(ctx context.Context) { close(started); <-ctx.Done() }
	s := New(c, d, l)
	done := make(chan error, 1)
	go func() { done <- s.Execute(context.Background(), platform.Action{Tool: "mouse_click", X: 1, Y: 1}) }()
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if e := s.Execute(ctx, platform.Action{Tool: "mouse_move", X: 1, Y: 1}); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal("input bypassed review lock", e)
	}
	s.Stop()
	select {
	case e := <-done:
		if e == nil {
			t.Fatal("stop ignored")
		}
	case <-time.After(time.Second):
		t.Fatal("approval not cancelled")
	}
	if len(d.Actions) != 0 {
		t.Fatal("input ran while approval pending")
	}
}

func TestConfiguredInputWithoutPerActionDialog(t *testing.T) {
	c, _, l := setup(t)
	c.LocalApproval = true
	c.ConfirmEachAction = false
	c.Allow = []string{"mouse_click", "keyboard_type", "keyboard_hotkey"}
	d := &approvingDesktop{approvalError: errors.New("review must not run")}
	s := New(c, d, l)
	for _, a := range []platform.Action{{Tool: "mouse_click", X: 1, Y: 1}, {Tool: "keyboard_type", Text: "Привет"}, {Tool: "keyboard_hotkey", Keys: []string{"CMD", "C"}}} {
		if e := s.Execute(context.Background(), a); e != nil {
			t.Fatal(e)
		}
	}
	if d.approved != 0 || len(d.Actions) != 3 {
		t.Fatal("unexpected review or missing input", d.approved, len(d.Actions))
	}
	if s.Status()["confirm_each_action"] != false {
		t.Fatal("wrong status")
	}
	s.Stop()
	if s.Execute(context.Background(), platform.Action{Tool: "keyboard_type", Text: "must not execute"}) == nil {
		t.Fatal("no-confirmation bypassed stop")
	}
}
func TestNoDialogStillRequiresProcessAllowlist(t *testing.T) {
	c, d, l := setup(t)
	c.LocalApproval = true
	c.ConfirmEachAction = false
	c.Allow = []string{"keyboard_type"}
	c.AllowedProcesses = []string{"other.exe"}
	s := New(c, d, l)
	if s.Execute(context.Background(), platform.Action{Tool: "keyboard_type", Text: "must not execute"}) == nil || len(d.Actions) != 0 {
		t.Fatal("process allowlist bypass")
	}
}

type focusDesktop struct {
	mock.Desktop
	windows     []platform.Window
	active      platform.Window
	activeError error
}

func (d *focusDesktop) Windows(ctx context.Context) ([]platform.Window, error) {
	return d.windows, ctx.Err()
}
func (d *focusDesktop) Active(ctx context.Context) (platform.Window, error) {
	return d.active, d.activeError
}

func TestFocusAuthorizesDestinationProcess(t *testing.T) {
	for _, kind := range []string{"different_process", "no_foreground", "offscreen_window", "denied_destination", "stale_id"} {
		t.Run(kind, func(t *testing.T) {
			c, _, l := setup(t)
			d := &focusDesktop{
				active:  platform.Window{ID: "terminal", Process: "terminal.exe"},
				windows: []platform.Window{{ID: "browser", Process: "mock.exe"}},
			}
			id := "browser"
			if kind == "no_foreground" {
				d.activeError = errors.New("no visible foreground window")
			}
			if kind == "denied_destination" {
				d.windows[0].Process = "denied.exe"
			}
			if kind == "offscreen_window" {
				onScreen := false
				d.windows[0].OnScreen = &onScreen
				d.windows[0].Bounds = platform.Rect{X: 2000, Width: 900, Height: 700}
			}
			if kind == "stale_id" {
				id = "stale"
			}
			err := New(c, d, l).Execute(context.Background(), platform.Action{Tool: "window_focus", WindowID: id})
			wantOK := kind == "different_process" || kind == "no_foreground" || kind == "offscreen_window"
			if (err == nil) != wantOK || (len(d.Actions) == 1) != wantOK {
				t.Fatalf("focus result: err=%v, actions=%v", err, d.Actions)
			}
		})
	}
}

func TestKeyboardExpectedWindow(t *testing.T) {
	for _, tool := range []string{"keyboard_type", "keyboard_press", "keyboard_hotkey"} {
		for _, id := range []string{"mock-window", "browser"} {
			t.Run(tool+"/"+id, func(t *testing.T) {
				c, d, l := setup(t)
				c.LocalApproval = true
				c.ConfirmEachAction = false
				c.Allow = []string{tool}
				err := New(c, d, l).Execute(context.Background(), platform.Action{Tool: tool, WindowID: id})
				wantOK := id == "mock-window"
				if (err == nil) != wantOK || (len(d.Actions) == 1) != wantOK {
					t.Fatalf("keyboard result: err=%v, actions=%v", err, d.Actions)
				}
			})
		}
	}
}

func TestLaunchWithNoForegroundStillRequiresLocalAuthorization(t *testing.T) {
	for _, kind := range []string{"allow", "observe", "not_allowlisted", "no_local_approval", "denied_process", "review_denied", "stopped"} {
		t.Run(kind, func(t *testing.T) {
			c, _, l := setup(t)
			c.Allow = []string{"app_launch"}
			c.LocalApproval = true
			c.ConfirmEachAction = false
			d := &focusDesktop{activeError: errors.New("Spotlight has no visible foreground window")}
			if kind == "observe" {
				c.Mode = "observe"
			}
			if kind == "not_allowlisted" {
				c.Allow = nil
			}
			if kind == "no_local_approval" {
				c.LocalApproval = false
			}
			if kind == "denied_process" {
				c.AllowedProcesses = []string{"other.exe"}
			}
			if kind == "review_denied" {
				c.ConfirmEachAction = true
			}
			s := New(c, d, l)
			if kind == "stopped" {
				s.Stop()
			}
			err := s.Execute(context.Background(), platform.Action{Tool: "app_launch", Process: "mock.exe"})
			wantOK := kind == "allow"
			if (err == nil) != wantOK || (len(d.Actions) == 1) != wantOK {
				t.Fatalf("err=%v actions=%v", err, d.Actions)
			}
		})
	}
}

func TestLaunchPerActionReviewAndStop(t *testing.T) {
	for _, kind := range []string{"allow", "deny", "stop"} {
		t.Run(kind, func(t *testing.T) {
			c, _, l := setup(t)
			c.Allow = []string{"app_launch"}
			c.LocalApproval = true
			c.ConfirmEachAction = true
			d := &approvingDesktop{}
			s := New(c, d, l)
			if kind == "deny" {
				d.approvalError = errors.New("declined")
			}
			if kind == "stop" {
				d.onApprove = func(context.Context) { s.Stop() }
			}
			err := s.Execute(context.Background(), platform.Action{Tool: "app_launch", Process: "mock.exe"})
			wantOK := kind == "allow"
			if d.approved != 1 || (err == nil) != wantOK || (len(d.Actions) == 1) != wantOK {
				t.Fatalf("err=%v actions=%v approvals=%d", err, d.Actions, d.approved)
			}
		})
	}
}

func TestMouseActionBindsPolicyAuthorizedWindow(t *testing.T) {
	c, d, l := setup(t)
	s := New(c, d, l)
	if err := s.Execute(context.Background(), platform.Action{Tool: "mouse_move", X: 10, Y: 20}); err != nil {
		t.Fatal(err)
	}
	if len(d.Actions) != 1 || d.Actions[0].WindowID != "mock-window" {
		t.Fatal("backend did not receive authorized window ID", d.Actions)
	}
}

func TestKeyboardAndScrollBindPolicyAuthorizedWindow(t *testing.T) {
	for _, a := range []platform.Action{{Tool: "keyboard_type", Text: "test"}, {Tool: "keyboard_press", Keys: []string{"ENTER"}}, {Tool: "keyboard_hotkey", Keys: []string{"CTRL", "L"}}, {Tool: "mouse_scroll", Delta: 120}} {
		t.Run(a.Tool, func(t *testing.T) {
			c, d, l := setup(t)
			c.LocalApproval = true
			c.ConfirmEachAction = false
			c.Allow = append(c.Allow, a.Tool)
			if err := New(c, d, l).Execute(context.Background(), a); err != nil {
				t.Fatal(err)
			}
			if len(d.Actions) != 1 || d.Actions[0].WindowID != "mock-window" {
				t.Fatal("lost authorized window", d.Actions)
			}
		})
	}
}

type signedCloneDesktop struct {
	mock.Desktop
	verified bool
	checks   int
}

const chromeInstalled = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
const chromeClone = "/private/var/folders/test/X/com.google.Chrome.code_sign_clone/session/Google Chrome.app.bundle/Contents/MacOS/Google Chrome"

func (d *signedCloneDesktop) Windows(ctx context.Context) ([]platform.Window, error) {
	onScreen := false
	return []platform.Window{{ID: "chrome", Process: chromeClone, Bounds: platform.Rect{Width: 640, Height: 480}, OnScreen: &onScreen}}, ctx.Err()
}
func (d *signedCloneDesktop) Active(ctx context.Context) (platform.Window, error) {
	ws, err := d.Windows(ctx)
	return ws[0], err
}
func (d *signedCloneDesktop) MatchesWindowProcess(ctx context.Context, w platform.Window, expected string) (bool, error) {
	d.checks++
	return d.verified && w.ID == "chrome" && w.Process == chromeClone && expected == chromeInstalled, ctx.Err()
}
func TestSignedCloneRequiresNativeIdentityAndConfiguredApplication(t *testing.T) {
	for _, kind := range []string{"verified", "bad_signature", "not_configured", "observe", "stopped"} {
		for _, tool := range []string{"window_focus", "keyboard_type"} {
			t.Run(kind+"/"+tool, func(t *testing.T) {
				c, _, l := setup(t)
				c.Allow = []string{tool}
				c.AllowedProcesses = []string{chromeInstalled}
				c.LocalApproval = true
				c.ConfirmEachAction = false
				d := &signedCloneDesktop{verified: kind != "bad_signature"}
				if kind == "not_configured" {
					c.AllowedProcesses = []string{"/Applications/Denied.app/Contents/MacOS/Denied"}
				}
				if kind == "observe" {
					c.Mode = "observe"
				}
				s := New(c, d, l)
				if kind == "stopped" {
					s.Stop()
				}
				err := s.Execute(context.Background(), platform.Action{Tool: tool, WindowID: "chrome", Text: "test"})
				if (err == nil) != (kind == "verified") || (len(d.Actions) == 1) != (kind == "verified") {
					t.Fatal(kind, tool, err, d.Actions)
				}
			})
		}
	}
}
func TestWindowDescriptionKeepsDeniedAndOffscreenWindows(t *testing.T) {
	c, _, l := setup(t)
	c.AllowedProcesses = []string{chromeInstalled}
	d := &signedCloneDesktop{verified: true}
	s := New(c, d, l)
	ws, _ := d.Windows(context.Background())
	description, err := s.DescribeWindows(context.Background(), ws)
	if err != nil || len(description) != 1 {
		t.Fatal(description, err)
	}
	w := description[0]
	if w.Process != chromeClone || w.AllowedProcess != chromeInstalled || w.ProcessAllowed == nil || !*w.ProcessAllowed || w.OnScreen == nil || *w.OnScreen {
		t.Fatal("incorrect identity/visibility", w)
	}
	if ws[0].ProcessAllowed != nil {
		t.Fatal("description modified backend windows")
	}
	d.verified = false
	description, err = s.DescribeWindows(context.Background(), ws)
	if err != nil || len(description) != 1 || description[0].ProcessAllowed == nil || *description[0].ProcessAllowed || description[0].IdentityError == "" || description[0].AllowedProcess != "" {
		t.Fatal("denied window omitted or authorized", description, err)
	}
}

func TestUnlimitedBudgetsRemainBoundedAndStoppable(t *testing.T) {
	c, d, l := setup(t)
	c.MaxPerMinute = 0
	c.MaxPerTask = 0
	s := New(c, d, l)
	for i := 0; i < 1000; i++ {
		if err := s.limitsLocked(time.Now()); err != nil {
			t.Fatal(i, err)
		}
	}
	if s.total != 1000 || len(s.recent) != 0 {
		t.Fatal("unlimited mode must count without retaining request timestamps")
	}
	if s.Status()["task_budget_unlimited"] != true || s.Status()["rate_limit_unlimited"] != true {
		t.Fatal(s.Status())
	}
	s.Stop()
	if s.Execute(context.Background(), platform.Action{Tool: "mouse_move", X: 1, Y: 1}) == nil {
		t.Fatal("unlimited budget bypassed stop")
	}
}
func TestIndependentlyDisabledBudgets(t *testing.T) {
	c, d, l := setup(t)
	c.MaxPerTask = 0
	c.MaxPerMinute = 1
	s := New(c, d, l)
	now := time.Now()
	if s.limitsLocked(now) != nil || s.limitsLocked(now) == nil || s.limitsLocked(now.Add(time.Minute)) != nil {
		t.Fatal("minute limit changed when task budget disabled")
	}
	c.MaxPerMinute = 0
	c.MaxPerTask = 1
	s = New(c, d, l)
	if s.limitsLocked(now) != nil || s.limitsLocked(now.Add(time.Minute)) == nil {
		t.Fatal("task limit changed when minute limit disabled")
	}
}
