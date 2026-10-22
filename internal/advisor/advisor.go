// Package advisor presents bounded, ephemeral advice. It never captures a
// screen, calls a model, injects input, or exposes a remote desktop endpoint.
package advisor

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"example.com/codex-vision/internal/platform"
)

type Source struct {
	ObservationID string        `json:"observation_id"`
	Sequence      uint64        `json:"sequence"`
	FocusVersion  uint64        `json:"focus_version"`
	State         string        `json:"state"`
	WindowID      string        `json:"window_id"`
	Region        platform.Rect `json:"watch_region"`
}

type Advice struct {
	Recommendation string    `json:"recommendation"`
	Reason         string    `json:"reason"`
	Alternative    string    `json:"alternative,omitempty"`
	Urgency        string    `json:"urgency"`
	TTLSeconds     int       `json:"ttl_seconds"`
	Revision       uint64    `json:"revision"`
	CreatedAt      time.Time `json:"created_at"`
	Source         Source    `json:"source"`
}

type State struct {
	Active     bool      `json:"active"`
	Paused     bool      `json:"paused"`
	Title      string    `json:"title"`
	Stale      bool      `json:"stale"`
	Status     string    `json:"status"`
	Advice     *Advice   `json:"advice,omitempty"`
	ServerTime time.Time `json:"server_time"`
}

type Manager struct {
	mu                      sync.Mutex
	server                  *http.Server
	active, paused          bool
	visible, visibilityLost bool
	title, url              string
	key                     string
	source                  Source
	advice                  *Advice
	expires                 time.Time
	revision                uint64
	current                 func() Source
	listen                  func(string, string) (net.Listener, error)
}

func New(current func() Source) *Manager { return NewWithListener(current, net.Listen) }

// NewWithListener allows an embedding host to supply its listener. Address
// validation is unchanged; tests can verify the contract without network I/O.
func NewWithListener(current func() Source, listen func(string, string) (net.Listener, error)) *Manager {
	return &Manager{current: current, listen: listen}
}

// This panel is local-only. No LAN, wildcard or DNS binding.
func ValidateListen(address string) error {
	if address == "" {
		return nil
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("advisor listener requires an IP:port")
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || ip.Zone() != "" || !ip.IsLoopback() || ip.IsUnspecified() {
		return fmt.Errorf("advisor listener must use a concrete loopback IP")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 0 || n > 65535 {
		return fmt.Errorf("invalid advisor port")
	}
	return nil
}

func (m *Manager) Active() bool { m.mu.Lock(); defer m.mu.Unlock(); return m.active }

func (m *Manager) Start(address, title string, source Source) (string, error) {
	if address == "" {
		address = "127.0.0.1:0"
	}
	if err := ValidateListen(address); err != nil {
		return "", err
	}
	if !validText(title, 80) || source.State != "running" || source.ObservationID == "" || source.Sequence == 0 || source.WindowID == "" || !source.Region.Valid() {
		return "", fmt.Errorf("advisor requires a running scoped observation and a title of 1..80 characters")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active {
		return "", fmt.Errorf("advisor already active; use status or stop first")
	}
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return "", err
	}
	listener, err := m.listen("tcp", address)
	if err != nil {
		return "", fmt.Errorf("advisor listener: %w", err)
	}
	host := listener.Addr().String()
	token := hex.EncodeToString(secret[:])
	m.key = token
	m.url = "http://" + host + "/#" + token
	m.active, m.paused, m.title, m.source, m.advice, m.revision = true, false, title, source, nil, 0
	m.visible, m.visibilityLost = true, false
	m.server = &http.Server{Handler: m.handler(host, token), ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
	server := m.server
	go func() { _ = server.Serve(listener) }()
	return m.url, nil
}

func (m *Manager) URL() string { m.mu.Lock(); defer m.mu.Unlock(); return m.url }

func sameSource(a, b Source) bool {
	return a.ObservationID == b.ObservationID && a.FocusVersion == b.FocusVersion && a.WindowID == b.WindowID && a.Region == b.Region
}
func validText(s string, limit int) bool {
	return utf8.ValidString(s) && strings.TrimSpace(s) != "" && utf8.RuneCountInString(s) <= limit && !strings.ContainsRune(s, 0)
}

func (m *Manager) Publish(a Advice) error {
	if !validText(a.Recommendation, 240) || !validText(a.Reason, 600) || (a.Alternative != "" && !validText(a.Alternative, 300)) {
		return fmt.Errorf("advice text missing or exceeds 240/600/300 characters")
	}
	if a.Urgency == "" {
		a.Urgency = "normal"
	}
	if a.Urgency != "normal" && a.Urgency != "important" && a.Urgency != "critical" {
		return fmt.Errorf("urgency must be normal, important or critical")
	}
	if a.TTLSeconds == 0 {
		a.TTLSeconds = 20
	}
	if a.TTLSeconds < 3 || a.TTLSeconds > 120 {
		return fmt.Errorf("ttl_seconds must be 3..120")
	}
	current := m.current()
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.active || m.paused {
		return fmt.Errorf("advisor inactive or paused")
	}
	if current.State != "running" || !sameSource(m.source, a.Source) || !sameSource(a.Source, current) || a.Source.Sequence != current.Sequence {
		return fmt.Errorf("stale advice source; observe the selected region again")
	}
	if previous := m.advice; previous != nil && m.visible && !m.visibilityLost && previous.Source.Sequence == a.Source.Sequence && previous.Recommendation == a.Recommendation && previous.Reason == a.Reason && previous.Alternative == a.Alternative && previous.Urgency == a.Urgency && previous.TTLSeconds == a.TTLSeconds {
		return nil // No duplicate notification and no extension of old evidence.
	}
	m.revision++
	now := time.Now()
	a.Revision, a.CreatedAt = m.revision, now.UTC()
	m.expires = now.Add(time.Duration(a.TTLSeconds) * time.Second)
	m.advice = &a
	m.visible, m.visibilityLost = true, false
	return nil
}

func (m *Manager) Snapshot() State {
	current := m.current()
	m.mu.Lock()
	defer m.mu.Unlock()
	s := State{Active: m.active, Paused: m.paused, Title: m.title, Stale: true, Status: "waiting", ServerTime: time.Now().UTC()}
	if m.advice != nil {
		a := *m.advice
		s.Advice = &a
	}
	switch {
	case !m.active:
		s.Status = "stopped"
	case m.paused:
		s.Status = "paused"
	case current.State != "running":
		s.Status = "observation_unavailable"
	case !sameSource(m.source, current):
		s.Status = "source_changed"
	case !m.visible || m.visibilityLost:
		s.Status = "source_not_visible"
	case m.advice == nil:
		s.Status = "waiting"
	case m.advice.Source.Sequence != current.Sequence:
		s.Status = "scene_changed"
	case !time.Now().Before(m.expires):
		s.Status = "expired"
	default:
		s.Status = "ready"
		s.Stale = false
	}
	return s
}

// Losing the source foreground invalidates advice permanently, even if the
// same pixels return later. Only a new evidence-backed Publish can clear it.
func (m *Manager) MarkVisibility(visible bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.visible = visible
	if !visible {
		m.visibilityLost = true
	}
}

func (m *Manager) Pause(paused bool) error {
	return m.pause(paused, "")
}

func (m *Manager) pause(paused bool, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.active || (key != "" && m.key != key) {
		return fmt.Errorf("advisor not active")
	}
	m.paused = paused
	// Never resurrect a recommendation after a user pauses and resumes.
	if !paused {
		m.advice = nil
	}
	return nil
}

func (m *Manager) Close() {
	m.mu.Lock()
	server := m.server
	m.active, m.paused, m.advice, m.server, m.url, m.key = false, false, nil, nil, "", ""
	m.mu.Unlock()
	if server != nil {
		_ = server.Close()
	}
}

func (m *Manager) handler(host, token string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Robots-Tag", "noindex, nofollow")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
		if r.Host != host {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == "/state" || r.URL.Path == "/control" {
			m.mu.Lock()
			live := m.active && subtle.ConstantTimeCompare([]byte(m.key), []byte(token)) == 1
			m.mu.Unlock()
			if !live {
				http.Error(w, "expired advisor", http.StatusGone)
				return
			}
			header := r.Header.Get("Authorization")
			provided := strings.TrimPrefix(header, "Bearer ")
			if !strings.HasPrefix(header, "Bearer ") || subtle.ConstantTimeCompare([]byte(provided), []byte(token)) != 1 {
				http.Error(w, "pairing required", http.StatusUnauthorized)
				return
			}
			if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+host {
				http.Error(w, "invalid origin", 403)
				return
			}
			if r.URL.Path == "/control" {
				if r.Method != "POST" {
					w.WriteHeader(405)
					return
				}
				if r.Header.Get("Content-Type") != "application/json" {
					w.WriteHeader(415)
					return
				}
				var args struct {
					Action string `json:"action"`
				}
				dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128))
				dec.DisallowUnknownFields()
				if err := dec.Decode(&args); err != nil || (args.Action != "pause" && args.Action != "resume") {
					http.Error(w, "invalid action", 400)
					return
				}
				if err := dec.Decode(new(any)); err != io.EOF {
					http.Error(w, "invalid request", 400)
					return
				}
				if err := m.pause(args.Action == "pause", token); err != nil {
					http.Error(w, "not active", 409)
					return
				}
			} else if r.Method != "GET" {
				w.WriteHeader(405)
				return
			}
			state := m.Snapshot()
			m.mu.Lock()
			live = m.active && m.key == token
			m.mu.Unlock()
			if !live {
				http.Error(w, "expired advisor", http.StatusGone)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(state)
			return
		}
		if r.Method != "GET" {
			w.WriteHeader(405)
			return
		}
		var content, kind string
		switch r.URL.Path {
		case "/":
			content, kind = panelHTML, "text/html; charset=utf-8"
		case "/style.css":
			content, kind = panelCSS, "text/css; charset=utf-8"
		case "/app.js":
			content, kind = panelJS, "text/javascript; charset=utf-8"
		default:
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", kind)
		_, _ = w.Write([]byte(content))
	})
}
