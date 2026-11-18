// Package screencast manages bounded, explicitly pulled portal image streams.
// Images have no desktop coordinate mapping and cannot authorize input.
package screencast

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"sync"
	"time"

	"example.com/codex-vision/internal/platform"
	"example.com/codex-vision/internal/vision"
)

type Options struct {
	Duration  time.Duration
	MaxFrames int
}

type Status struct {
	ID         string    `json:"stream_id"`
	State      string    `json:"state"`
	Code       string    `json:"code,omitempty"`
	Deadline   time.Time `json:"deadline"`
	Sequence   uint64    `json:"sequence"`
	Sampled    int       `json:"sampled_frames"`
	Duplicates int       `json:"duplicate_frames"`
	Delivered  int       `json:"images_returned"`
	MaxFrames  int       `json:"max_frames"`
}

type Frame struct {
	Sequence   uint64    `json:"sequence"`
	CapturedAt time.Time `json:"captured_at"`
	Width      int       `json:"width"`
	Height     int       `json:"height"`
	MIME       string    `json:"mime"`
	Data       []byte    `json:"-"`
}

type Manager struct {
	mu            sync.Mutex
	status        Status
	image         image.Image
	captured      time.Time
	digest        [32]byte
	cancel        context.CancelFunc
	changed       chan struct{}
	done          chan struct{}
	stopped       bool
	life          context.Context
	sourceDone    <-chan struct{}
	pulls         chan struct{}
	cleanupFailed bool
}

func (m *Manager) signal() {
	if m.changed != nil {
		close(m.changed)
	}
	m.changed = make(chan struct{})
}

// Start reserves the one session before requesting consent asynchronously.
// Cancellation and status remain available while the system dialog is open.
func (m *Manager) Start(options Options, open func(context.Context) (platform.ScreenCastSource, error), check func() error) (Status, error) {
	if open == nil || check == nil {
		return Status{}, fmt.Errorf("stream provider and stop guard required")
	}
	if options.Duration < 5*time.Second || options.Duration > 300*time.Second || options.MaxFrames < 2 || options.MaxFrames > 600 {
		return Status{}, fmt.Errorf("stream requires duration_sec 5..300 and max_frames 2..600")
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return Status{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopped {
		return m.status, fmt.Errorf("streaming permanently stopped; restart locally")
	}
	if m.cleanupFailed {
		return m.status, fmt.Errorf("native capture cleanup failed; inspect locally before restarting")
	}
	if m.status.State == "starting" || m.status.State == "running" {
		return m.status, fmt.Errorf("stream already active; stop before starting another consent session")
	}
	if m.done != nil {
		select {
		case <-m.done:
		default:
			return m.status, fmt.Errorf("previous stream cleanup still pending")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), options.Duration)
	m.cancel = cancel
	m.life, m.sourceDone = ctx, nil
	m.status = Status{ID: hex.EncodeToString(random[:]), State: "starting", Deadline: time.Now().Add(options.Duration), MaxFrames: options.MaxFrames}
	m.image = nil
	m.digest = [32]byte{}
	m.done = make(chan struct{})
	m.signal()
	go m.run(ctx, cancel, m.done, open, check)
	return m.status, nil
}

func (m *Manager) finish(state, code string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.status.State == "starting" || m.status.State == "running" {
		m.status.State, m.status.Code = state, code
	}
	m.image = nil
	m.signal()
}

func (m *Manager) run(ctx context.Context, cancel context.CancelFunc, done chan struct{}, open func(context.Context) (platform.ScreenCastSource, error), check func() error) {
	defer close(done)
	defer cancel()
	defer func() {
		if recover() != nil {
			m.finish("failed", "internal_failure")
		}
	}()
	go func() {
		ticker := time.NewTicker(25 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if check() != nil {
					cancel()
					return
				}
			}
		}
	}()
	if check() != nil {
		m.finish("stopped", "session_stopped")
		return
	}
	source, err := open(ctx)
	if err != nil || source == nil {
		if source != nil {
			m.closeSource(source)
		}
		state, code := "failed", "source_unavailable"
		if errors.Is(err, platform.ErrCaptureDenied) {
			state, code = "cancelled", "consent_denied"
		}
		if errors.Is(err, platform.ErrCaptureUnsupported) {
			code = "unsupported_portal"
		}
		if errors.Is(err, context.DeadlineExceeded) {
			state, code = "timeout", "setup_timeout"
		}
		if ctx.Err() == context.DeadlineExceeded {
			state, code = "timeout", "deadline"
		} else if ctx.Err() != nil {
			state, code = "stopped", "cancelled"
		}
		m.finish(state, code)
		return
	}
	// A cancellation must close a blocked native reader, not wait for a new frame.
	m.mu.Lock()
	m.sourceDone = source.Done()
	m.mu.Unlock()
	stop := context.AfterFunc(ctx, func() { _ = source.Close() })
	defer stop()
	defer m.closeSource(source)
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-source.Done():
			m.mu.Lock()
			m.activeLocked()
			m.mu.Unlock()
		}
	}()
	for {
		im, err := source.Next(ctx)
		if ctx.Err() != nil {
			if ctx.Err() == context.DeadlineExceeded {
				m.finish("timeout", "deadline")
			} else {
				m.finish("stopped", "cancelled")
			}
			return
		}
		if err != nil {
			m.finish("failed", "source_closed")
			return
		}
		if check() != nil {
			cancel()
			m.finish("stopped", "session_stopped")
			return
		}
		if im == nil || vision.Validate(im.Bounds()) != nil {
			m.finish("failed", "invalid_frame")
			return
		}
		// Copy provider-owned buffers before the next native read can reuse them.
		copy := image.NewNRGBA(image.Rect(0, 0, im.Bounds().Dx(), im.Bounds().Dy()))
		draw.Draw(copy, copy.Bounds(), im, im.Bounds().Min, draw.Src)
		digest := sha256.Sum256(copy.Pix)
		m.mu.Lock()
		if !m.activeLocked() {
			m.mu.Unlock()
			return
		}
		m.status.State = "running"
		m.status.Sampled++
		if m.image != nil && m.digest == digest && m.image.Bounds() == copy.Bounds() {
			m.status.Duplicates++
		} else {
			m.image, m.digest, m.captured = copy, digest, time.Now().UTC()
			m.status.Sequence++
			m.signal()
		}
		limit := m.status.Sampled >= m.status.MaxFrames
		m.mu.Unlock()
		if limit {
			m.finish("limit_reached", "frame_budget")
			return
		}
	}
}

func (m *Manager) closeSource(source platform.ScreenCastSource) {
	if err := source.Close(); err != nil {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.cleanupFailed = true
		m.status.State, m.status.Code = "failed", "cleanup_failed"
		m.image = nil
		m.signal()
	}
}

func (m *Manager) activeLocked() bool {
	active := m.status.State == "starting" || m.status.State == "running"
	if !active {
		return false
	}
	state, code := "", ""
	if m.life != nil && m.life.Err() != nil {
		state, code = "stopped", "cancelled"
		if m.life.Err() == context.DeadlineExceeded {
			state, code = "timeout", "deadline"
		}
	} else if m.sourceDone != nil {
		select {
		case <-m.sourceDone:
			state, code = "failed", "source_closed"
		default:
		}
	}
	if state != "" {
		m.status.State, m.status.Code = state, code
		m.image = nil
		m.cancel()
		m.signal()
		return false
	}
	return true
}

func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.activeLocked()
	return m.status
}

func (m *Manager) Stop(id string) (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id == "" || id != m.status.ID {
		return m.status, fmt.Errorf("unknown stream_id")
	}
	if m.cancel != nil {
		m.cancel()
	}
	if m.status.State == "starting" || m.status.State == "running" {
		m.status.State, m.status.Code = "stopped", "requested"
	}
	m.image = nil
	m.signal()
	return m.status, nil
}

func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopped = true
	if m.cancel != nil {
		m.cancel()
	}
	if m.status.State == "starting" || m.status.State == "running" {
		m.status.State, m.status.Code = "stopped", "session_stopped"
	}
	m.image = nil
	m.signal()
}

// Next sends pixels only for a sequence the caller has not acknowledged. A
// caller can explicitly use zero to resynchronise; no implicit cursor guessing.
func (m *Manager) Next(ctx context.Context, id string, after uint64, wait time.Duration) (Status, *Frame, error) {
	if err := ctx.Err(); err != nil {
		return m.Status(), nil, err
	}
	if wait < 0 || wait > 30*time.Second {
		return Status{}, nil, fmt.Errorf("wait_ms must be 0..30000")
	}
	m.mu.Lock()
	if m.pulls == nil {
		m.pulls = make(chan struct{}, 1)
	}
	pulls := m.pulls
	m.mu.Unlock()
	select {
	case pulls <- struct{}{}:
		defer func() { <-pulls }()
	case <-ctx.Done():
		return m.Status(), nil, ctx.Err()
	}
	poll, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	for {
		m.mu.Lock()
		active := m.activeLocked()
		status, changed := m.status, m.changed
		if id == "" || id != status.ID || after > status.Sequence {
			m.mu.Unlock()
			return status, nil, fmt.Errorf("unknown stream_id or future after_sequence")
		}
		if active && m.image != nil && status.Sequence > after {
			im, at, sequence := m.image, m.captured, status.Sequence
			m.mu.Unlock()
			// The poll timeout does not truncate encoding an already available frame.
			data, mime, w, h, err := vision.Encode(ctx, im, "jpeg", 75, 768)
			if err == nil {
				err = ctx.Err()
			}
			if err != nil {
				return status, nil, err
			}
			m.mu.Lock()
			if m.status.ID != id || !m.activeLocked() || m.status.State != "running" {
				status = m.status
				m.mu.Unlock()
				return status, nil, nil
			}
			m.status.Delivered++
			status = m.status
			m.mu.Unlock()
			return status, &Frame{Sequence: sequence, CapturedAt: at, Width: w, Height: h, MIME: mime, Data: data}, nil
		}
		m.mu.Unlock()
		if !active || wait == 0 {
			return status, nil, nil
		}
		select {
		case <-poll.Done():
			if ctx.Err() != nil {
				return m.Status(), nil, ctx.Err()
			}
			return m.Status(), nil, nil
		case <-changed:
		}
	}
}
