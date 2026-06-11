// Package eta keeps a rolling average of the last N completed-job durations so
// the queue ETA stays honest as model/load conditions change (PRD §5.4). It is
// goroutine-safe; the worker records durations, SSE handlers read the average.
package eta

import (
	"sync"
	"time"
)

type Tracker struct {
	mu      sync.RWMutex
	window  int
	samples []time.Duration // ring of the last `window` durations
}

// New returns a tracker over the last `window` completed jobs. window<1 falls
// back to 20 (the PRD default).
func New(window int) *Tracker {
	if window < 1 {
		window = 20
	}
	return &Tracker{window: window}
}

// Record adds one completed-job duration, evicting the oldest beyond the window.
func (t *Tracker) Record(d time.Duration) {
	if d <= 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.samples = append(t.samples, d)
	if len(t.samples) > t.window {
		t.samples = t.samples[len(t.samples)-t.window:]
	}
}

// Average returns the mean of recorded durations, or ok=false if there are no
// samples yet (so the UI can show a neutral "estimating…" rather than a guess).
func (t *Tracker) Average() (avg time.Duration, ok bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if len(t.samples) == 0 {
		return 0, false
	}
	var sum time.Duration
	for _, s := range t.samples {
		sum += s
	}
	return sum / time.Duration(len(t.samples)), true
}
