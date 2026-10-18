package observation

import "time"

// persistence checks each sampled pixel independently. A spinner/caret in one
// place cannot prevent a stable text/layout change elsewhere from being seen.
// This is temporal filtering, not semantic recognition or privacy redaction.
type persistence struct {
	anchor    fingerprint
	candidate []byte
	since     []time.Time
}

func pixelChanged(a, b []byte, i int) bool {
	for c := 0; c < 3; c++ {
		d := int(a[i+c]) - int(b[i+c])
		if d >= 12 || d <= -12 {
			return true
		}
	}
	return false
}
func (p *persistence) filter(anchor, current fingerprint, at time.Time, hold time.Duration) (fingerprint, float64, bool) {
	if hold == 0 || len(anchor.rgb) == 0 || anchor.width != current.width || anchor.height != current.height {
		return current, difference(anchor, current), false
	}
	if len(p.anchor.rgb) == 0 || &p.anchor.rgb[0] != &anchor.rgb[0] {
		p.anchor = anchor
		p.candidate = make([]byte, len(current.rgb))
		p.since = make([]time.Time, len(current.rgb)/3)
	}
	out := anchor
	out.rgb = append([]byte(nil), anchor.rgb...)
	stable, pending := 0, false
	for i := 0; i < len(current.rgb); i += 3 {
		cell := i / 3
		if !pixelChanged(anchor.rgb, current.rgb, i) {
			p.since[cell] = time.Time{}
			continue
		}
		if p.since[cell].IsZero() || pixelChanged(p.candidate, current.rgb, i) {
			copy(p.candidate[i:i+3], current.rgb[i:i+3])
			p.since[cell] = at
		}
		if at.Sub(p.since[cell]) < hold {
			pending = true
			continue
		}
		copy(out.rgb[i:i+3], current.rgb[i:i+3])
		stable++
	}
	if current.active == 0 {
		return out, 0, pending
	}
	return out, float64(stable) / float64(current.active), pending
}
