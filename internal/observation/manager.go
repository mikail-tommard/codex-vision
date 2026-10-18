// Package observation keeps a bounded, local screen feed. It never injects input
// or calls a model. MCP clients explicitly retrieve significant frames.
package observation

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"image"
	"math"
	"net/http"
	"sync"
	"time"

	"example.com/codex-vision/internal/platform"
	"example.com/codex-vision/internal/vision"
)

type Options struct {
	Diagnostics         bool
	ModelMaxWidth       int
	Stability           time.Duration
	WatchRegion         *platform.Rect
	ReadyRegion         *platform.Rect
	WatchWindow         *platform.Window
	IgnoreRegions       []platform.Rect
	Mode                string
	Interval            time.Duration
	MinDeliveryInterval time.Duration
	TaskIdleTimeout     time.Duration
	ChangeThreshold     float64
	MaxWidth, Quality   int
	Preview             bool
	Backend             string
	Adaptive            bool
	Regions             bool
	IdleInterval        time.Duration
}

func (o Options) validate() error {
	if o.ModelMaxWidth != 0 && (o.ModelMaxWidth < 160 || o.ModelMaxWidth > 1920) {
		return fmt.Errorf("model max width must be 0 or 160..1920")
	}
	if o.Stability < 0 || o.Stability > 5*time.Second {
		return fmt.Errorf("stability must be 0..5000 ms")
	}
	for _, r := range []*platform.Rect{o.WatchRegion, o.ReadyRegion} {
		if r != nil && !r.Valid() {
			return fmt.Errorf("invalid attention region")
		}
	}
	if len(o.IgnoreRegions) > 16 {
		return fmt.Errorf("ignore_regions supports at most 16 rectangles")
	}
	for _, r := range o.IgnoreRegions {
		if !r.Valid() {
			return fmt.Errorf("invalid ignored region")
		}
	}
	if o.Mode != "task" && o.Mode != "continuous" {
		return fmt.Errorf("mode must be task or continuous")
	}
	if o.Interval < 250*time.Millisecond || o.Interval > 10*time.Second {
		return fmt.Errorf("interval must be 250..10000 ms")
	}
	if o.MinDeliveryInterval < 250*time.Millisecond || o.MinDeliveryInterval > time.Minute {
		return fmt.Errorf("minimum delivery interval must be 250..60000 ms")
	}
	if o.TaskIdleTimeout < 50*time.Millisecond || o.TaskIdleTimeout > time.Hour {
		return fmt.Errorf("task idle timeout is out of range")
	}
	if math.IsNaN(o.ChangeThreshold) || math.IsInf(o.ChangeThreshold, 0) || o.ChangeThreshold < 0 || o.ChangeThreshold > 1 {
		return fmt.Errorf("change threshold must be 0..1")
	}
	if o.MaxWidth < 160 || o.MaxWidth > 1920 || o.Quality < 30 || o.Quality > 100 {
		return fmt.Errorf("max_width must be 160..1920 and quality 30..100")
	}
	if (o.Adaptive || o.IdleInterval != 0) && (o.IdleInterval < o.Interval || o.IdleInterval > 30*time.Second) {
		return fmt.Errorf("idle interval must be between capture interval and 30000 ms")
	}
	return nil
}

type Sample struct {
	Image  image.Image
	Bounds platform.Rect
	Cursor *platform.Point
}
type Frame struct {
	Diagnostics    *FrameDiagnostic `json:"diagnostics,omitempty"`
	Kind           string           `json:"kind"`
	BaseSequence   uint64           `json:"base_sequence,omitempty"`
	FullBounds     platform.Rect    `json:"full_bounds"`
	Sequence       uint64           `json:"sequence"`
	CapturedAt     time.Time        `json:"captured_at"`
	Bounds         platform.Rect    `json:"source_bounds"`
	Width          int              `json:"width"`
	Height         int              `json:"height"`
	MIME           string           `json:"mime_type"`
	ChangeFraction float64          `json:"change_fraction"`
	Cursor         *platform.Point  `json:"cursor_position,omitempty"`
	Data           []byte           `json:"-"`
}
type Status struct {
	LastDetection     *ChangeDiagnostic `json:"last_detection,omitempty"`
	ModelMaxWidth     int               `json:"model_max_width"`
	StabilityMS       int64             `json:"stability_ms"`
	FilteredFrames    uint64            `json:"filtered_frames"`
	CaptureBounds     platform.Rect     `json:"capture_bounds"`
	WatchRegion       *platform.Rect    `json:"watch_region,omitempty"`
	ReadyRegion       *platform.Rect    `json:"ready_region,omitempty"`
	WatchWindowID     string            `json:"watch_window_id,omitempty"`
	FocusVersion      uint64            `json:"focus_version"`
	IgnoredRegions    int               `json:"ignored_regions"`
	Adaptive          bool              `json:"adaptive"`
	Regions           bool              `json:"regions"`
	CurrentIntervalMS int64             `json:"current_interval_ms"`
	IdleIntervalMS    int64             `json:"idle_interval_ms"`
	PreviewSequence   uint64            `json:"preview_sequence"`
	ID                string            `json:"observation_id"`
	Mode              string            `json:"mode"`
	State             string            `json:"state"`
	Reason            string            `json:"reason,omitempty"`
	Error             string            `json:"error,omitempty"`
	Backend           string            `json:"backend"`
	PreviewURL        string            `json:"preview_url,omitempty"`
	Sequence          uint64            `json:"sequence"`
	LocalFrames       uint64            `json:"local_frames"`
	SignificantFrames uint64            `json:"significant_frames"`
	ImagesReturned    uint64            `json:"images_returned"`
	UnchangedFrames   uint64            `json:"unchanged_frames"`
	IntervalMS        int64             `json:"interval_ms"`
	MinDeliveryMS     int64             `json:"min_delivery_ms"`
	TaskIdleTimeoutMS int64             `json:"task_idle_timeout_ms"`
	StartedAt         time.Time         `json:"started_at,omitempty"`
	LastCaptureAt     time.Time         `json:"last_capture_at,omitempty"`
	Width             int               `json:"width"`
	Height            int               `json:"height"`
}
type Manager struct {
	op                         sync.Mutex
	mu                         sync.Mutex
	capture                    func(context.Context) (Sample, error)
	check                      func() error
	opts                       Options
	status                     Status
	changed                    chan struct{}
	cancel                     context.CancelFunc
	captureCancel              context.CancelFunc
	generation                 uint64
	closed                     bool
	persistent                 persistence
	baseline                   fingerprint
	baselineBounds             platform.Rect
	latest, preview            *Frame
	previewSequence            uint64
	deliveredSequence          uint64
	lastDelivery, lastActivity time.Time
	server                     *http.Server
	wake                       chan struct{}
	dirty                      image.Rectangle
	pendingFull                bool
	lastFull                   time.Time
	focusEpoch                 uint64
	sampleSig                  fingerprint
	markerSig                  fingerprint
	waiters                    int
}

func New(capture func(context.Context) (Sample, error), check func() error) *Manager {
	if check == nil {
		check = func() error { return nil }
	}
	return &Manager{capture: capture, check: check, changed: make(chan struct{}), wake: make(chan struct{}, 1), status: Status{State: "idle"}}
}
func (m *Manager) signalLocked()  { close(m.changed); m.changed = make(chan struct{}) }
func (m *Manager) Status() Status { m.mu.Lock(); defer m.mu.Unlock(); return m.status }
func (m *Manager) Touch() {
	m.mu.Lock()
	m.lastActivity = time.Now()
	m.mu.Unlock()
	select {
	case m.wake <- struct{}{}:
	default:
	}
}
func (m *Manager) prepare(ctx context.Context, s Sample, o Options) (*Frame, fingerprint, error) {
	if s.Image == nil || !s.Bounds.Valid() {
		return nil, fingerprint{}, fmt.Errorf("invalid observation sample")
	}
	if s.Image.Bounds().Dx() != s.Bounds.Width || s.Image.Bounds().Dy() != s.Bounds.Height {
		return nil, fingerprint{}, fmt.Errorf("sample and desktop coordinate bounds differ")
	}
	if err := vision.Validate(s.Image.Bounds()); err != nil {
		return nil, fingerprint{}, err
	}
	if err := ctx.Err(); err != nil {
		return nil, fingerprint{}, err
	}
	masks := make([]image.Rectangle, 0, len(o.IgnoreRegions))
	for _, r := range o.IgnoreRegions {
		if !s.Bounds.Includes(r) {
			return nil, fingerprint{}, fmt.Errorf("ignore region outside capture bounds")
		}
		masks = append(masks, image.Rect(s.Image.Bounds().Min.X+r.X-s.Bounds.X, s.Image.Bounds().Min.Y+r.Y-s.Bounds.Y, s.Image.Bounds().Min.X+r.X-s.Bounds.X+r.Width, s.Image.Bounds().Min.Y+r.Y-s.Bounds.Y+r.Height))
	}
	if o.WatchRegion != nil {
		w := *o.WatchRegion
		if !s.Bounds.Includes(w) {
			return nil, fingerprint{}, fmt.Errorf("watch_region outside capture bounds")
		}
		b := s.Image.Bounds()
		x := b.Min.X + w.X - s.Bounds.X
		y := b.Min.Y + w.Y - s.Bounds.Y
		masks = append(masks, image.Rect(b.Min.X, b.Min.Y, b.Max.X, y), image.Rect(b.Min.X, y+w.Height, b.Max.X, b.Max.Y), image.Rect(b.Min.X, y, x, y+w.Height), image.Rect(x+w.Width, y, b.Max.X, y+w.Height))
	}
	if o.ReadyRegion != nil && !s.Bounds.Includes(*o.ReadyRegion) {
		return nil, fingerprint{}, fmt.Errorf("ready_region outside capture bounds")
	}
	sig := signature(s.Image, masks...)
	if o.WatchRegion != nil && sig.active == 0 {
		return nil, fingerprint{}, fmt.Errorf("watch_region has no sampled pixels; enlarge it or remove overlapping ignore regions")
	}
	// Cursor overlays are intentionally excluded from change detection.
	im := s.Image
	if s.Cursor != nil && s.Bounds.Contains(s.Cursor.X, s.Cursor.Y) {
		im = vision.WithCursorMarker(im, s.Cursor.X-s.Bounds.X, s.Cursor.Y-s.Bounds.Y)
	}
	data, mime, w, h, err := vision.Encode(ctx, im, "jpeg", o.Quality, o.MaxWidth)
	if err != nil {
		return nil, fingerprint{}, err
	}
	return &Frame{Kind: "full", FullBounds: s.Bounds, CapturedAt: time.Now().UTC(), Bounds: s.Bounds, Width: w, Height: h, MIME: mime, Data: data, Cursor: s.Cursor}, sig, nil
}
func (m *Manager) Start(ctx context.Context, o Options, sources ...func(context.Context) (Sample, error)) (Status, *Frame, error) {
	m.op.Lock()
	defer m.op.Unlock()
	o.IgnoreRegions = append([]platform.Rect(nil), o.IgnoreRegions...)
	o.WatchRegion = cloneRect(o.WatchRegion)
	o.ReadyRegion = cloneRect(o.ReadyRegion)
	o.WatchWindow = cloneWindow(o.WatchWindow)
	if err := o.validate(); err != nil {
		return m.Status(), nil, err
	}
	m.mu.Lock()
	busy := m.status.State == "running" || m.status.State == "paused"
	closed := m.closed
	m.mu.Unlock()
	if closed {
		return m.Status(), nil, fmt.Errorf("observer is closed")
	}
	if busy {
		return m.Status(), nil, fmt.Errorf("observation already active; stop it before starting another")
	}
	if err := m.check(); err != nil {
		return m.Status(), nil, err
	}
	source := m.capture
	if len(sources) > 0 {
		source = sources[0]
	}
	if source == nil {
		return m.Status(), nil, fmt.Errorf("missing capture source")
	}
	sample, err := source(ctx)
	if err != nil {
		return m.Status(), nil, err
	}
	frame, sig, err := m.prepare(ctx, sample, o)
	if err != nil {
		return m.Status(), nil, err
	}
	if err = m.check(); err != nil {
		return m.Status(), nil, err
	}
	if err = ctx.Err(); err != nil {
		return m.Status(), nil, err
	}
	var token [24]byte
	if _, err = rand.Read(token[:]); err != nil {
		return m.Status(), nil, err
	}
	id := hex.EncodeToString(token[:])
	var server *http.Server
	previewURL := ""
	if o.Preview {
		server, previewURL, err = m.startPreview(id)
		if err != nil {
			return m.Status(), nil, err
		}
	}
	root, cancel := context.WithCancel(context.Background())
	now := time.Now()
	frame.Sequence = 1
	frame.ChangeFraction = 1
	m.mu.Lock()
	m.generation++
	m.focusEpoch++
	m.waiters = 0
	generation := m.generation
	m.opts = o
	m.cancel = cancel
	m.server = server
	m.sampleSig = sig
	m.markerSig = markerSignature(sample, o)
	m.baseline = sig
	m.baselineBounds = frame.Bounds
	m.latest = frame
	m.preview = frame
	m.previewSequence = 1
	m.deliveredSequence = 1
	m.lastDelivery = now
	m.lastFull = now
	m.dirty = image.Rectangle{}
	m.pendingFull = false
	m.lastActivity = now
	m.persistent = persistence{}
	m.status = Status{ModelMaxWidth: o.ModelMaxWidth, StabilityMS: o.Stability.Milliseconds(), CaptureBounds: sample.Bounds, WatchRegion: cloneRect(o.WatchRegion), ReadyRegion: cloneRect(o.ReadyRegion), FocusVersion: m.focusEpoch, IgnoredRegions: len(o.IgnoreRegions), Adaptive: o.Adaptive, Regions: o.Regions, CurrentIntervalMS: o.Interval.Milliseconds(), IdleIntervalMS: o.IdleInterval.Milliseconds(), PreviewSequence: 1, ID: id, Mode: o.Mode, State: "running", Backend: o.Backend, PreviewURL: previewURL, Sequence: 1, LocalFrames: 1, SignificantFrames: 1, ImagesReturned: 1, IntervalMS: o.Interval.Milliseconds(), MinDeliveryMS: o.MinDeliveryInterval.Milliseconds(), TaskIdleTimeoutMS: o.TaskIdleTimeout.Milliseconds(), StartedAt: now.UTC(), LastCaptureAt: frame.CapturedAt, Width: frame.Width, Height: frame.Height}
	if o.WatchWindow != nil {
		m.status.WatchWindowID = o.WatchWindow.ID
	}
	if o.Diagnostics {
		d := m.diagnostic("initial_context", fingerprint{}, sig, sig, frame.Bounds, 0, 0, false)
		d.ComparedSequence = 0
		frame.Diagnostics = &FrameDiagnostic{ChangeDiagnostic: d, Delivery: "observation_start", Image: "initial_context"}
		m.status.LastDetection = &d
	}
	returned, cropErr := m.focusedFrame(ctx, frame)
	if cropErr != nil {
		m.mu.Unlock()
		m.finish(generation, "error", "encode_failed", cropErr)
		return m.Status(), nil, cropErr
	}
	m.signalLocked()
	status := m.status
	m.mu.Unlock()
	go m.loop(root, generation, o, source)
	go m.supervise(root, generation, o)
	return status, returned, nil
}
func (m *Manager) supervise(ctx context.Context, generation uint64, o Options) {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := m.check(); err != nil {
				m.finish(generation, "stopped", "session_unavailable", err)
				return
			}
			m.mu.Lock()
			idle := time.Since(m.lastActivity)
			same := m.generation == generation
			m.mu.Unlock()
			if !same {
				return
			}
			if o.Mode == "task" && idle >= o.TaskIdleTimeout {
				m.finish(generation, "stopped", "task_idle_timeout", nil)
				return
			}
		}
	}
}
func (m *Manager) loop(ctx context.Context, generation uint64, o Options, source func(context.Context) (Sample, error)) {
	interval := o.Interval
	unchanged := 0
	ticker := time.NewTimer(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.wake:
			// Activity can accelerate an idle timer, but must never postpone a
			// capture already due at the base rate (busy input would starve it).
			unchanged = 0
			m.mu.Lock()
			base := o.Interval
			if m.waiters > 0 {
				base = min(base, 250*time.Millisecond)
			}
			m.mu.Unlock()
			if interval > base {
				if !ticker.Stop() {
					select {
					case <-ticker.C:
					default:
					}
				}
				interval = base
				ticker.Reset(interval)
			}
		case <-ticker.C:
			ticker.Reset(interval)
			m.mu.Lock()
			if m.generation != generation || m.status.State != "running" {
				m.mu.Unlock()
				continue
			}
			sampleOptions := m.opts
			epoch := m.focusEpoch
			sampleCtx, cancel := context.WithCancel(ctx)
			m.captureCancel = cancel
			m.mu.Unlock()
			captureStarted := time.Now().UTC()
			sample, err := source(sampleCtx)
			var frame *Frame
			var sig fingerprint
			if err == nil {
				frame, sig, err = m.prepare(sampleCtx, sample, sampleOptions)
				if frame != nil {
					frame.CapturedAt = captureStarted
				}
			}
			m.mu.Lock()
			active := m.generation == generation && m.status.State == "running" && m.focusEpoch == epoch
			cancelled := sampleCtx.Err() != nil
			if m.generation == generation {
				m.captureCancel = nil
			}
			m.mu.Unlock()
			cancel()
			if !active || cancelled {
				continue
			}
			if err != nil {
				m.finish(generation, "error", "capture_failed", err)
				return
			}
			if err = m.check(); err != nil {
				m.finish(generation, "stopped", "session_unavailable", err)
				return
			}
			m.mu.Lock()
			if m.generation != generation || m.status.State != "running" || m.focusEpoch != epoch {
				m.mu.Unlock()
				continue
			}
			rawFraction := difference(m.baseline, sig)
			accepted, fraction, pending := m.persistent.filter(m.baseline, sig, frame.CapturedAt, sampleOptions.Stability)
			contextChanged := m.latest == nil || frame.Bounds != m.baselineBounds
			if contextChanged {
				accepted = sig
				fraction = 1
				pending = false
				m.persistent = persistence{}
			}
			publish := contextChanged || (fraction > 0 && fraction >= o.ChangeThreshold)
			if o.Diagnostics {
				reason := "unchanged"
				switch {
				case frame.Bounds != m.baselineBounds:
					reason = "geometry_change"
				case contextChanged:
					reason = "context_refresh"
				case publish && sampleOptions.Stability > 0:
					reason = "stable_change"
				case publish:
					reason = "immediate_change"
				case pending && rawFraction >= o.ChangeThreshold:
					reason = "awaiting_stability"
				case rawFraction > 0:
					reason = "below_threshold"
				}
				d := m.diagnostic(reason, m.baseline, sig, accepted, frame.Bounds, rawFraction, fraction, pending)
				if contextChanged {
					d.ComparedSequence = 0
					d.ComparisonValid = false
					d.RawFraction = 0
					d.StableFraction = 0
					d.RawBounds = nil
					d.StableBounds = nil
				}
				frame.Diagnostics = &FrameDiagnostic{ChangeDiagnostic: d}
				m.status.LastDetection = &d
			}
			m.status.LocalFrames++
			m.status.LastCaptureAt = frame.CapturedAt
			m.status.Width = frame.Width
			m.status.Height = frame.Height
			if publish {
				if frame.Bounds != m.baselineBounds || m.latest == nil {
					m.pendingFull = true
					m.dirty = image.Rectangle{}
				} else {
					m.dirty = m.dirty.Union(changedRect(m.baseline, accepted, image.Rect(0, 0, frame.Bounds.Width, frame.Bounds.Height)))
				}
				unchanged = 0
				interval = o.Interval
				m.status.Sequence++
				m.status.SignificantFrames++
				frame.Sequence = m.status.Sequence
				frame.ChangeFraction = fraction
				m.latest = frame
				m.baseline = accepted
				m.baselineBounds = frame.Bounds
			} else {
				m.status.UnchangedFrames++
				unchanged++
				if o.Adaptive {
					interval = nextInterval(o.Interval, interval, o.IdleInterval, unchanged)
				}
			}
			if !publish && rawFraction > 0 {
				m.status.FilteredFrames++
			}
			if pending {
				interval = min(o.Interval, 250*time.Millisecond)
				unchanged = 0
			}
			if m.waiters > 0 {
				interval = min(o.Interval, 250*time.Millisecond)
			}
			frame.Sequence = m.status.Sequence
			m.sampleSig = sig
			m.markerSig = markerSignature(sample, sampleOptions)
			m.preview = frame
			m.previewSequence++
			m.status.PreviewSequence = m.previewSequence
			m.status.CurrentIntervalMS = interval.Milliseconds()
			ticker.Reset(interval)
			m.signalLocked()
			m.mu.Unlock()
		}
	}
}
func (m *Manager) finish(generation uint64, state, reason string, err error) {
	m.mu.Lock()
	if generation != m.generation || m.status.State == "stopped" || m.status.State == "error" {
		m.mu.Unlock()
		return
	}
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	if m.captureCancel != nil {
		m.captureCancel()
		m.captureCancel = nil
	}
	server := m.server
	m.server = nil
	m.latest = nil
	m.dirty = image.Rectangle{}
	m.preview = nil
	m.baseline = fingerprint{}
	m.persistent = persistence{}
	m.sampleSig = fingerprint{}
	m.markerSig = fingerprint{}
	m.status.State = state
	m.status.Reason = reason
	m.status.PreviewURL = ""
	m.status.Error = ""
	if err != nil {
		m.status.Error = err.Error()
	}
	m.signalLocked()
	m.mu.Unlock()
	if server != nil {
		_ = server.Close()
	}
}
func (m *Manager) Stop() {
	m.op.Lock()
	defer m.op.Unlock()
	m.mu.Lock()
	generation := m.generation
	m.mu.Unlock()
	m.finish(generation, "stopped", "requested", nil)
}
func (m *Manager) Close() {
	m.op.Lock()
	defer m.op.Unlock()
	m.mu.Lock()
	m.closed = true
	generation := m.generation
	m.mu.Unlock()
	m.finish(generation, "stopped", "connection_closed", nil)
}
func (m *Manager) Control(id, action string) (Status, error) {
	m.op.Lock()
	defer m.op.Unlock()
	m.mu.Lock()
	if id == "" || id != m.status.ID {
		m.mu.Unlock()
		return m.Status(), fmt.Errorf("unknown observation_id")
	}
	state := m.status.State
	generation := m.generation
	m.lastActivity = time.Now()
	if action == "pause" && state == "running" {
		m.status.LastDetection = nil
		m.status.State = "paused"
		m.focusEpoch++
		m.status.FocusVersion = m.focusEpoch
		m.persistent = persistence{}
		m.sampleSig = fingerprint{}
		m.markerSig = fingerprint{}
		if m.captureCancel != nil {
			m.captureCancel()
		}
		m.signalLocked()
		status := m.status
		m.mu.Unlock()
		return status, nil
	}
	if action == "resume" && state == "paused" {
		m.mu.Unlock()
		if err := m.check(); err != nil {
			m.finish(generation, "stopped", "session_unavailable", err)
			return m.Status(), err
		}
		m.mu.Lock()
		if m.generation != generation || m.status.State != "paused" {
			m.mu.Unlock()
			return m.Status(), fmt.Errorf("observation stopped while resuming")
		}
		m.status.State = "running"
		m.status.LastDetection = nil
		m.latest = nil
		m.signalLocked()
		status := m.status
		m.mu.Unlock()
		return status, nil
	}
	m.mu.Unlock()
	if action == "stop" {
		m.finish(generation, "stopped", "requested", nil)
		return m.Status(), nil
	}
	return m.Status(), fmt.Errorf("cannot %s observation in state %s", action, state)
}

// Next returns only new significant content, coalescing changes and suppressing
// repeated deliveries even if a client accidentally reuses an old sequence.
func (m *Manager) Next(ctx context.Context, id string, after uint64, wait time.Duration) (Status, *Frame, error) {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	m.Touch()
	for {
		if err := ctx.Err(); err != nil {
			return m.Status(), nil, err
		}
		m.mu.Lock()
		if id == "" || id != m.status.ID {
			m.mu.Unlock()
			return m.Status(), nil, fmt.Errorf("unknown observation_id")
		}
		status := m.status
		if after > status.Sequence {
			m.mu.Unlock()
			return status, nil, fmt.Errorf("after_sequence exceeds current sequence")
		}
		if status.State != "running" {
			m.mu.Unlock()
			return status, nil, nil
		}
		cooldown := time.Until(m.lastDelivery.Add(m.opts.MinDeliveryInterval))
		if m.latest != nil && m.latest.Sequence > max(after, m.deliveredSequence) && cooldown <= 0 {
			frame, err := m.regionFrame(ctx, after)
			if err != nil {
				m.mu.Unlock()
				return status, nil, err
			}
			if frame.Kind == "full" {
				m.lastFull = time.Now()
			}
			m.dirty = image.Rectangle{}
			m.pendingFull = false
			m.deliveredSequence = frame.Sequence
			m.lastDelivery = time.Now()
			m.status.ImagesReturned++
			status = m.status
			m.mu.Unlock()
			return status, frame, nil
		}
		changed := m.changed
		m.mu.Unlock()
		if wait == 0 {
			return status, nil, nil
		}
		// A cooldown expiry is also a wake-up even if pixels stay unchanged.
		var wake <-chan time.Time
		var delay *time.Timer
		if cooldown > 0 {
			delay = time.NewTimer(cooldown)
			wake = delay.C
		}
		select {
		case <-ctx.Done():
			if delay != nil {
				delay.Stop()
			}
			return m.Status(), nil, ctx.Err()
		case <-timer.C:
			if delay != nil {
				delay.Stop()
			}
			return m.Status(), nil, nil
		case <-changed:
		case <-wake:
		}
		if delay != nil {
			delay.Stop()
		}
	}
}
