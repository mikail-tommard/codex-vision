package mcpserver

import (
	"example.com/codex-vision/internal/advisor"
	"net"
	"sync"
	"testing"
	"time"
)

func TestAdvisorEvidenceAndObservationOnlyLifecycle(t *testing.T) {
	d := agentFixture("Saved")
	c, svc := agentClient(t, d)
	svc.Advisor = advisor.NewWithListener(svc.advisorSource, func(string, string) (net.Listener, error) {
		return &advisorTestListener{closed: make(chan struct{})}, nil
	})
	view := agentResult(t, call(t, c, "desktop_observe", map[string]any{}, true))
	call(t, c, "advisor", map[string]any{"action": "start", "view_id": view.View.ID}, true)
	if !svc.Advisor.Active() {
		t.Fatal("advisor not running")
	}
	call(t, c, "advisor", map[string]any{"action": "publish", "view_id": view.View.ID, "recommendation": "Choose a move", "reason": "Visible game state"}, true)
	for _, name := range []string{"desktop_act", "mouse_click", "window_focus"} {
		args := map[string]any{"window_id": "mock-window", "x": 50, "y": 50}
		if name == "desktop_act" {
			args = agentClick(view.View.ID, view.Controls[0].ID, "Saved")
		}
		if name == "window_focus" {
			args = map[string]any{"window_id": "mock-window"}
		}
		call(t, c, name, args, false)
	}
	if len(d.actions()) != 0 || !svc.Advisor.Active() {
		t.Fatal("advisor injected input", d.actions())
	}
	call(t, c, "advisor", map[string]any{"action": "pause"}, true)
	call(t, c, "advisor", map[string]any{"action": "publish", "view_id": view.View.ID, "recommendation": "Move", "reason": "Visible"}, false)
	call(t, c, "advisor", map[string]any{"action": "resume"}, true)
	if svc.Advisor.Snapshot().Advice != nil {
		t.Fatal("old advice revived")
	}
	svc.views.mu.Lock()
	svc.views.current.Expires = time.Now().Add(-time.Second)
	svc.views.mu.Unlock()
	call(t, c, "advisor", map[string]any{"action": "publish", "view_id": view.View.ID, "recommendation": "Move", "reason": "Visible"}, false)
	call(t, c, "advisor", map[string]any{"action": "stop"}, true)
	if svc.Advisor.Active() || svc.Observer.Status().State != "running" || svc.Session.Status()["stopped"] == true {
		t.Fatal("advisor stop killed session/capture")
	}
	call(t, c, "mouse_click", map[string]any{"window_id": "mock-window", "x": 50, "y": 50}, true)
	if len(d.actions()) != 1 {
		t.Fatal("input unusable after explicit stop")
	}
}

type advisorTestListener struct {
	closed chan struct{}
	once   sync.Once
}

func (l *advisorTestListener) Accept() (net.Conn, error) { <-l.closed; return nil, net.ErrClosed }
func (l *advisorTestListener) Close() error              { l.once.Do(func() { close(l.closed) }); return nil }
func (l *advisorTestListener) Addr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 18147}
}
