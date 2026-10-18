package observation

import (
	"context"
	"fmt"
	"image"
	"time"
)

func markerSignature(s Sample, o Options) fingerprint {
	if o.ReadyRegion == nil {
		return fingerprint{}
	}
	return signature(view(s.Image, s.Bounds, *o.ReadyRegion))
}

type sampledState struct {
	frame       *Frame
	sig, marker fingerprint
	serial      uint64
	epoch       uint64
	generation  uint64
}
type ActionWaitOptions struct {
	Settle, Wait         time.Duration
	Ready                func(context.Context) (bool, error)
	SingleChange         bool
	SuppressTimeoutImage bool
	MetadataOnReady      bool // Only a successful native predicate may replace the image.
}
type InteractionTimings struct {
	BaselineMS int64 `json:"baseline_ms"`
	InputMS    int64 `json:"input_ms"`
	WaitMS     int64 `json:"wait_ms"`
	DeliveryMS int64 `json:"delivery_ms"`
	TotalMS    int64 `json:"total_ms"`
}
type ActionWaitResult struct {
	Timings                  InteractionTimings `json:"timings"`
	ReadyConfirmed           bool               `json:"ready_confirmed"`
	Attempted                bool               `json:"action_attempted"`
	Posted                   bool               `json:"action_posted"`
	MayHaveChanged           bool               `json:"may_have_changed"`
	OwnChangeObserved        bool               `json:"own_change_observed"`
	SubsequentChangeObserved bool               `json:"subsequent_change_observed"`
	MarkerDepartureObserved  bool               `json:"marker_departure_observed"`
	TimedOut                 bool               `json:"timed_out"`
	Stage                    string             `json:"stage"`
	ResponseVerified         bool               `json:"response_verified"` // Visual predicates never prove game rules or actor identity.
	RetryAction              bool               `json:"retry_action"`      // Always false; inspect the returned position before any next action.
}

func (m *Manager) sample(ctx context.Context, id string, epoch, after uint64) (sampledState, error) {
	for {
		if err := ctx.Err(); err != nil {
			return sampledState{}, err
		}
		if err := m.check(); err != nil {
			return sampledState{}, err
		}
		m.mu.Lock()
		if m.status.ID != id || m.focusEpoch != epoch {
			m.mu.Unlock()
			return sampledState{}, fmt.Errorf("observation or attention changed; do not repeat the action")
		}
		if m.status.State != "running" {
			state := m.status.State
			m.mu.Unlock()
			return sampledState{}, fmt.Errorf("observation %s; do not repeat the action", state)
		}
		if m.preview != nil && len(m.sampleSig.rgb) > 0 && m.previewSequence > after {
			out := sampledState{m.preview, m.sampleSig, m.markerSig, m.previewSequence, epoch, m.generation}
			m.mu.Unlock()
			return out, nil
		}
		changed := m.changed
		m.mu.Unlock()
		select {
		case <-ctx.Done():
			return sampledState{}, ctx.Err()
		case <-changed:
		}
	}
}
func (m *Manager) deliverSample(ctx context.Context, id string, s sampledState) (Status, *Frame, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.status.ID != id || m.focusEpoch != s.epoch || m.status.State != "running" {
		return m.status, nil, fmt.Errorf("observation changed before delivery")
	}
	frame, err := m.focusedFrame(ctx, s.frame)
	if err != nil {
		return m.status, nil, err
	}
	copyFrame := *frame
	copyFrame.Diagnostics = copyDiagnostic(copyFrame.Diagnostics)
	if copyFrame.Diagnostics != nil {
		copyFrame.Diagnostics.Delivery = "action_result"
		copyFrame.Diagnostics.Image = "action_context"
	}
	copyFrame.Kind = "full"
	copyFrame.BaseSequence = 0
	// The model has now seen this exact raw sample, including changes that may
	// precede the background persistence threshold. Acknowledge it so next does
	// not deliver the same state again. Never erase a newer published state.
	if m.latest == nil || m.latest.Sequence <= s.frame.Sequence {
		fraction := difference(m.baseline, s.sig)
		if m.baselineBounds != s.frame.Bounds || fraction > 0 {
			m.status.Sequence++
			m.status.SignificantFrames++
			raw := *s.frame
			raw.Sequence = m.status.Sequence
			raw.ChangeFraction = fraction
			m.latest = &raw
			m.baseline = s.sig
			m.baselineBounds = s.frame.Bounds
			m.persistent = persistence{}
			copyFrame.Sequence = raw.Sequence
			copyFrame.ChangeFraction = fraction
		}
		m.dirty = image.Rectangle{}
		m.pendingFull = false
	}

	m.deliveredSequence = max(m.deliveredSequence, copyFrame.Sequence)
	m.status.ImagesReturned++
	m.lastDelivery = time.Now()
	m.lastFull = time.Now()
	// Clear accumulated deltas only through the delivered sample; newer data must
	// still be eligible for an ordinary observation_next call.
	if m.latest != nil && m.latest.Sequence <= frame.Sequence {
		m.dirty = image.Rectangle{}
		m.pendingFull = false
	}
	return m.status, &copyFrame, nil
}

// ActAndWait shares the existing local sampler. It observes a stable own change
// followed by a stable subsequent change (or a configured ready marker returning
// to its pre-action state). It does NOT recognize chess moves or prove whose turn
// it is, and it never retries the input callback.
func (m *Manager) ActAndWait(ctx context.Context, id string, o ActionWaitOptions, act func(context.Context) error) (result ActionWaitResult, status Status, frame *Frame, err error) {
	result = ActionWaitResult{Stage: "preflight"}
	started := time.Now()
	var inputStarted, waitStarted, deliveryStarted time.Time
	defer func() {
		now := time.Now()
		result.Timings.TotalMS = now.Sub(started).Milliseconds()
		if !inputStarted.IsZero() {
			end := now
			if !waitStarted.IsZero() {
				end = waitStarted
			}
			result.Timings.InputMS = end.Sub(inputStarted).Milliseconds()
		}
		if !waitStarted.IsZero() {
			end := now
			if !deliveryStarted.IsZero() {
				end = deliveryStarted
			}
			result.Timings.WaitMS = end.Sub(waitStarted).Milliseconds()
		}
		if !deliveryStarted.IsZero() {
			result.Timings.DeliveryMS = now.Sub(deliveryStarted).Milliseconds()
		}
	}()
	if act == nil {
		return result, m.Status(), nil, fmt.Errorf("action callback required")
	}
	if o.Settle < 100*time.Millisecond || o.Settle > 2*time.Second || o.Wait < 100*time.Millisecond || o.Wait > 30*time.Second {
		return result, m.Status(), nil, fmt.Errorf("settle must be 100..2000ms and wait 100..30000ms")
	}
	m.mu.Lock()
	if m.status.ID != id || m.status.State != "running" || m.opts.WatchRegion == nil {
		m.mu.Unlock()
		return result, m.Status(), nil, fmt.Errorf("a running observation with watch_region is required")
	}
	epoch, generation := m.focusEpoch, m.generation
	serial := m.previewSequence
	threshold := m.opts.ChangeThreshold
	hasMarker := m.opts.ReadyRegion != nil
	m.waiters++
	m.lastActivity = time.Now()
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		if m.generation == generation {
			m.waiters--
		}
		m.mu.Unlock()
	}()
	m.Touch()
	before, err := m.sample(ctx, id, epoch, serial)
	if err != nil {
		return result, m.Status(), nil, err
	}
	result.Timings.BaselineMS = time.Since(started).Milliseconds()
	inputStarted = time.Now()
	result.Attempted = true
	result.MayHaveChanged = true
	if err = act(ctx); err != nil {
		return result, m.Status(), nil, err
	}
	result.Posted = true
	result.MayHaveChanged = false
	result.Stage = "waiting_own_change"
	if o.Ready != nil {
		result.Stage = "waiting_ready_condition"
	}
	finished := time.Now()
	waitStarted = finished
	waiting, cancel := context.WithTimeout(ctx, o.Wait)
	defer cancel()
	last := before
	anchor := before.sig
	var stable persistence
	ownSettled := false
	for {
		sample, readErr := m.sample(waiting, id, epoch, last.serial)
		if readErr != nil {
			if waiting.Err() == context.DeadlineExceeded && ctx.Err() == nil {
				result.TimedOut = true
				break
			}
			return result, m.Status(), nil, readErr
		}
		last = sample
		if sample.frame.CapturedAt.Before(finished) {
			continue
		}
		if o.Ready != nil {
			ready, e := o.Ready(waiting)
			if e != nil && waiting.Err() == context.DeadlineExceeded && ctx.Err() == nil {
				result.TimedOut = true
				break
			}
			if e != nil {
				return result, m.Status(), nil, e
			}
			if ready {
				result.ReadyConfirmed = true
				result.Stage = "ready_condition"
				break
			}
			continue
		}
		if hasMarker && difference(before.marker, sample.marker) > 0 {
			result.MarkerDepartureObserved = true
		}
		accepted, changed, _ := stable.filter(anchor, sample.sig, sample.frame.CapturedAt, o.Settle)
		if changed == 0 || changed < threshold {
			continue
		}
		if o.SingleChange {
			result.OwnChangeObserved = true
			result.Stage = "stable_change"
			break
		}
		if !ownSettled {
			result.OwnChangeObserved = true
			if hasMarker && difference(before.marker, sample.marker) == 0 {
				result.Stage = "ready_marker_matches"
				break
			}
			ownSettled = true
			anchor = accepted
			stable = persistence{}
			result.Stage = "waiting_subsequent_change"
			continue
		}
		if !hasMarker || difference(before.marker, sample.marker) == 0 {
			result.SubsequentChangeObserved = true
			result.Stage = "subsequent_stable_change"
			break
		}
	}
	// No repeated click/drag on timeout. One final scoped image lets the model
	// decide whether a fast response merged with the own-action settling phase.
	if err := ctx.Err(); err != nil {
		return result, m.Status(), nil, err
	}
	if last.frame.CapturedAt.Before(finished) {
		result.Stage = "no_post_action_frame"
		return result, m.Status(), nil, nil
	}
	if result.TimedOut && o.SuppressTimeoutImage {
		return result, m.Status(), nil, nil
	}
	if result.ReadyConfirmed && o.MetadataOnReady {
		// Do not acknowledge unseen pixels or increment ImagesReturned. A later
		// explicit observation_next may still retrieve the visual state.
		return result, m.Status(), nil, nil
	}
	deliveryStarted = time.Now()
	status, frame, err = m.deliverSample(ctx, id, last)
	if err == nil && frame != nil && frame.Diagnostics != nil {
		frame.Diagnostics.Delivery = "action_" + result.Stage
		if result.TimedOut {
			frame.Diagnostics.Delivery = "action_wait_timeout"
		}
		frame.Diagnostics.DeliverySettleMS = o.Settle.Milliseconds()
	}
	return result, status, frame, err
}
