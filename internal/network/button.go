package network

import "time"

// Hold fires once after a continuous five-second press, then requires release.
type Hold struct {
	since time.Time
	fired bool
}

func (h *Hold) Update(pressed bool, now time.Time) bool {
	if !pressed {
		h.since = time.Time{}
		h.fired = false
		return false
	}
	if h.since.IsZero() {
		h.since = now
	}
	if !h.fired && now.Sub(h.since) >= 5*time.Second {
		h.fired = true
		return true
	}
	return false
}
