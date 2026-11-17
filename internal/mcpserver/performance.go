package mcpserver

import (
	"sync"
	"time"
)

type stageMeasurement struct {
	Count   uint64 `json:"count"`
	TotalNS int64  `json:"total_ns"`
	MaxNS   int64  `json:"max_ns"`
}

// Fixed call-site categories only; never retain application text or images.
type stageMetrics struct {
	mu     sync.Mutex
	stages map[string]stageMeasurement
}

func (m *stageMetrics) record(stage string, started time.Time) {
	ns := time.Since(started).Nanoseconds()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stages == nil {
		m.stages = make(map[string]stageMeasurement)
	}
	s := m.stages[stage]
	s.Count++
	s.TotalNS += ns
	s.MaxNS = max(s.MaxNS, ns)
	m.stages[stage] = s
}

func (m *stageMetrics) snapshot() map[string]stageMeasurement {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]stageMeasurement, len(m.stages))
	for k, v := range m.stages {
		out[k] = v
	}
	return out
}
