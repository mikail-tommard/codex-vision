package advisor

import (
	"encoding/json"
	"net"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"example.com/codex-vision/internal/platform"
)

func fixture(t *testing.T) (*Manager, *Source) {
	t.Helper()
	s := &Source{ObservationID: "obs", Sequence: 1, FocusVersion: 2, State: "running", WindowID: "game", Region: platform.Rect{Width: 300, Height: 200}}
	m := NewWithListener(func() Source { return *s }, func(string, string) (net.Listener, error) { return &memoryListener{closed: make(chan struct{})}, nil })
	if _, err := m.Start("", "Test coach", *s); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	return m, s
}
func suggestion(s Source) Advice {
	return Advice{Recommendation: "Choose attack", Reason: "Observed match-up", Source: s}
}

func TestAdviceFreshnessAndNoResurrection(t *testing.T) {
	m, s := fixture(t)
	if state := m.Snapshot(); state.Status != "waiting" || !state.Stale {
		t.Fatal(state)
	}
	if err := m.Publish(suggestion(*s)); err != nil {
		t.Fatal(err)
	}
	if state := m.Snapshot(); state.Stale || state.Advice.Revision != 1 {
		t.Fatal(state)
	}
	created := m.Snapshot().Advice.CreatedAt
	if err := m.Publish(suggestion(*s)); err != nil {
		t.Fatal(err)
	}
	if state := m.Snapshot(); state.Advice.Revision != 1 || state.Advice.CreatedAt != created {
		t.Fatal("duplicate renewed evidence", state)
	}
	s.Sequence++
	if state := m.Snapshot(); state.Status != "scene_changed" || !state.Stale {
		t.Fatal(state)
	}
	if err := m.Publish(suggestion(Source{ObservationID: s.ObservationID, Sequence: 1})); err == nil {
		t.Fatal("stale source accepted")
	}
	if err := m.Publish(suggestion(*s)); err != nil {
		t.Fatal(err)
	}
	m.MarkVisibility(false)
	m.MarkVisibility(true)
	if state := m.Snapshot(); state.Status != "source_not_visible" || !state.Stale {
		t.Fatal("foreground restoration revived old advice", state)
	}
	if err := m.Publish(suggestion(*s)); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.expires = time.Now().Add(-time.Minute)
	m.mu.Unlock()
	if state := m.Snapshot(); state.Status != "expired" || !state.Stale {
		t.Fatal(state)
	}
	_ = m.Pause(true)
	if err := m.Publish(suggestion(*s)); err == nil {
		t.Fatal("published while paused")
	}
	_ = m.Pause(false)
	if m.Snapshot().Advice != nil {
		t.Fatal("resume revived advice")
	}
	m.Close()
	if state := m.Snapshot(); state.Active || state.Advice != nil || m.URL() != "" {
		t.Fatal(state)
	}
}

func TestAdviceRejectsRetargetingAndInvalidText(t *testing.T) {
	m, s := fixture(t)
	for _, kind := range []string{"observation", "window", "focus", "region", "paused"} {
		original := *s
		switch kind {
		case "observation":
			s.ObservationID = "other"
		case "window":
			s.WindowID = "other"
		case "focus":
			s.FocusVersion++
		case "region":
			s.Region.Width++
		case "paused":
			s.State = "paused"
		}
		if err := m.Publish(suggestion(*s)); err == nil {
			t.Fatal("different source accepted", kind)
		}
		*s = original
	}
	for _, change := range []func(*Advice){func(a *Advice) { a.Recommendation = strings.Repeat("я", 241) }, func(a *Advice) { a.Reason = "" }, func(a *Advice) { a.Alternative = strings.Repeat("x", 301) }, func(a *Advice) { a.TTLSeconds = 121 }, func(a *Advice) { a.Urgency = "unknown" }} {
		a := suggestion(*s)
		change(&a)
		if err := m.Publish(a); err == nil {
			t.Fatal("invalid advice accepted")
		}
	}
	if _, err := m.Start("", "Again", *s); err == nil {
		t.Fatal("replaced active panel")
	}
}

func TestPanelLocalOnlyAndAuthenticated(t *testing.T) {
	for _, address := range []string{"0.0.0.0:0", "[::]:0", "192.168.1.2:8080", "8.8.8.8:1234", "localhost:8080", "127.0.0.1:65536", "127.0.0.1:-1"} {
		if ValidateListen(address) == nil {
			t.Fatal("non-local/invalid bind accepted", address)
		}
	}
	m, s := fixture(t)
	if err := m.Publish(suggestion(*s)); err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(m.URL())
	token := u.Fragment
	u.Fragment = ""
	if len(token) != 64 {
		t.Fatal("weak token")
	}
	unauthorized := httptest.NewRecorder()
	m.handler(u.Host, token).ServeHTTP(unauthorized, httptest.NewRequest("GET", u.String()+"state", nil))
	if unauthorized.Code != 401 || strings.Contains(unauthorized.Body.String(), "Choose attack") {
		t.Fatal(unauthorized.Code, unauthorized.Body.String())
	}
	for _, tc := range []struct {
		method, path, auth, origin, body string
		want                             int
	}{
		{"GET", "/state", "Bearer " + token, "", "", 200},
		{"GET", "/state", token, "", "", 401},
		{"GET", "/state", "Bearer " + token, "https://evil.invalid", "", 403},
		{"POST", "/control", "Bearer " + token, "", `{"action":"pause"}`, 200},
		{"POST", "/control", "Bearer " + token, "", `{"action":"resume"} {}`, 400},
		{"POST", "/control", "Bearer " + token, "", `{"action":"click"}`, 400},
		{"POST", "/state", "Bearer " + token, "", "", 405},
		{"GET", "/screen", "Bearer " + token, "", "", 404},
	} {
		r := httptest.NewRequest(tc.method, "http://"+u.Host+tc.path, strings.NewReader(tc.body))
		r.Header.Set("Authorization", tc.auth)
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		m.handler(u.Host, token).ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatal(tc.path, tc.body, w.Code, w.Body.String())
		}
		if tc.path == "/state" && tc.want == 200 {
			var state State
			if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil || state.Advice == nil {
				t.Fatal(err, state)
			}
		}
	}
	r := httptest.NewRequest("GET", "http://evil.invalid/state", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	m.handler(u.Host, token).ServeHTTP(w, r)
	if w.Code != 404 {
		t.Fatal("host bypass", w.Code)
	}
}

func TestAdvisorUIPreview(t *testing.T) {
	// This opt-in fixture serves only synthetic text for browser verification.
	// Normal test runs never wait or open windows.
	if !previewEnabled() {
		t.Skip("set CODEX_VISION_ADVISOR_PREVIEW=1 for synthetic browser fixture")
	}
	s := &Source{ObservationID: "synthetic", Sequence: 1, FocusVersion: 1, State: "running", WindowID: "test-game", Region: platform.Rect{Width: 300, Height: 200}}
	m := New(func() Source { return *s })
	if _, err := m.Start("", "Синтетический пример", *s); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	a := suggestion(*s)
	a.Recommendation = "Выбери защитный ход"
	a.Reason = "Соперник готовит сильную атаку. Сохрани здоровье и подготовь безопасную смену."
	a.Alternative = "Если ожидаешь смену соперника — сохрани инициативу быстрой атакой."
	a.TTLSeconds = 120
	if err := m.Publish(a); err != nil {
		t.Fatal(err)
	}
	t.Log("SYNTHETIC_ADVISOR_URL=" + m.URL())
	for i := 0; i < 20; i++ {
		time.Sleep(30 * time.Second)
		_ = m.Publish(a)
	}
}

func TestExpiredPanelCannotReadOrPauseNewSession(t *testing.T) {
	m, s := fixture(t)
	u, _ := url.Parse(m.URL())
	oldToken := u.Fragment
	oldHandler := m.handler(u.Host, oldToken)
	m.Close()
	if _, err := m.Start("", "New session", *s); err != nil {
		t.Fatal(err)
	}
	if err := m.Publish(suggestion(*s)); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/state", "/control"} {
		r := httptest.NewRequest("POST", "http://"+u.Host+path, strings.NewReader(`{"action":"pause"}`))
		r.Header.Set("Authorization", "Bearer "+oldToken)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		oldHandler.ServeHTTP(w, r)
		if w.Code != 410 || strings.Contains(w.Body.String(), "Choose attack") || m.Snapshot().Paused {
			t.Fatal("expired panel accessed new session", w.Code, w.Body.String())
		}
	}
	if err := m.pause(true, oldToken); err == nil || m.Snapshot().Paused {
		t.Fatal("stale in-flight control paused new session")
	}
}

type memoryListener struct {
	closed chan struct{}
	once   sync.Once
}

func (l *memoryListener) Accept() (net.Conn, error) { <-l.closed; return nil, net.ErrClosed }
func (l *memoryListener) Close() error              { l.once.Do(func() { close(l.closed) }); return nil }
func (l *memoryListener) Addr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 18147}
}
