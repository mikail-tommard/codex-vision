package observation

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

func (m *Manager) startPreview(id string) (*http.Server, string, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, "", fmt.Errorf("local preview: %w", err)
	}
	host := listener.Addr().String()
	server := &http.Server{Handler: m.previewHandler(id, host), ReadHeaderTimeout: 3 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
	go func() { _ = server.Serve(listener) }()
	return server, "http://" + host + "/" + id + "/", nil
}
func (m *Manager) previewHandler(id, host string) http.Handler {
	prefix := "/" + id + "/"
	var clients atomic.Int32
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' blob:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
		if r.Host != host || !strings.HasPrefix(r.URL.Path, prefix) {
			http.NotFound(w, r)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, prefix)
		if path == "control" {
			if r.Method != "POST" {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+host {
				http.Error(w, "invalid origin", 403)
				return
			}
			if r.Header.Get("Content-Type") != "application/json" {
				http.Error(w, "expected application/json", 415)
				return
			}
			var args struct {
				Action string `json:"action"`
			}
			decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&args); err != nil {
				http.Error(w, "invalid request", 400)
				return
			}
			if err := decoder.Decode(new(any)); err != io.EOF {
				http.Error(w, "invalid request", 400)
				return
			}
			if args.Action != "pause" && args.Action != "resume" && args.Action != "stop" {
				http.Error(w, "invalid action", 400)
				return
			}
			if args.Action == "stop" {
				if m.Status().ID != id {
					http.Error(w, "expired observation", 409)
					return
				}
				body := []byte(`{"state":"stopped"}`)
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Content-Length", fmt.Sprint(len(body)))
				_, _ = w.Write(body)
				_ = http.NewResponseController(w).Flush()
				_, _ = m.Control(id, "stop")
				return
			}
			status, err := m.Control(id, args.Action)
			if err != nil {
				http.Error(w, err.Error(), 409)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(status)
			return
		}
		if r.Method != "GET" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		switch path {
		case "":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, previewHTML)
		case "style.css":
			w.Header().Set("Content-Type", "text/css; charset=utf-8")
			_, _ = io.WriteString(w, previewCSS)
		case "app.js":
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = io.WriteString(w, previewJS)
		case "status":
			status := m.Status()
			if status.ID != id {
				http.Error(w, "expired observation", 410)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(status)
		case "stream":
			if clients.Add(1) > 4 {
				clients.Add(-1)
				http.Error(w, "too many viewers", 429)
				return
			}
			defer clients.Add(-1)
			w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary=frame")
			var after uint64
			for {
				frame, seq, ok := m.previewNext(r.Context(), id, after)
				if !ok {
					return
				}
				after = seq
				controller := http.NewResponseController(w)
				_ = controller.SetWriteDeadline(time.Now().Add(5 * time.Second))
				if _, err := fmt.Fprintf(w, "--frame\r\nContent-Type: image/jpeg\r\nContent-Length: %d\r\n\r\n", len(frame.Data)); err != nil {
					return
				}
				if _, err := w.Write(frame.Data); err != nil {
					return
				}
				if _, err := io.WriteString(w, "\r\n"); err != nil {
					return
				}
				if err := controller.Flush(); err != nil {
					return
				}
			}
		default:
			http.NotFound(w, r)
		}
	})
}

func (m *Manager) previewNext(ctx context.Context, id string, after uint64) (*Frame, uint64, bool) {
	for {
		m.mu.Lock()
		state := m.status.State
		if m.status.ID != id || (state != "running" && state != "paused") {
			m.mu.Unlock()
			return nil, 0, false
		}
		if m.preview != nil && m.previewSequence > after {
			f, seq := m.preview, m.previewSequence
			m.mu.Unlock()
			return f, seq, true
		}
		changed := m.changed
		m.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, 0, false
		case <-changed:
		}
	}
}

//go:embed web/index.html
var previewHTML string

//go:embed web/style.css
var previewCSS string

//go:embed web/app.js
var previewJS string
