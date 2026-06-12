package web

import (
	"strings"
	"testing"
	"time"

	"qa-core/internal/queue"
)

func TestStatusViewBusyBanner(t *testing.T) {
	avg := 45 * time.Second

	idle := statusView(queue.Snapshot{Busy: false, QueueLen: 0})
	if idle.Label != "Available — submit now." {
		t.Errorf("idle = %q", idle.Label)
	}

	// Busy, nobody waiting: "someone's generating" + ~45s (1 job ahead).
	busy := statusView(queue.Snapshot{Busy: true, QueueLen: 0, ETAPerJob: avg, ETAKnown: true})
	if !strings.Contains(busy.Label, "Someone's generating now") ||
		!strings.Contains(busy.Label, "~45s") ||
		!strings.Contains(busy.Label, "next slot") {
		t.Errorf("busy/0 = %q", busy.Label)
	}

	// Busy + 2 waiting: "2 waiting", you'd be #3, ETA over 3 jobs ahead (~135s -> ~2m).
	q := statusView(queue.Snapshot{Busy: true, QueueLen: 2, ETAPerJob: avg, ETAKnown: true})
	if !strings.Contains(q.Label, "2 waiting") || !strings.Contains(q.Label, "#3") || !strings.Contains(q.Label, "~2m") {
		t.Errorf("busy/2 = %q", q.Label)
	}

	// No ETA yet: still a sensible message, just no time.
	noeta := statusView(queue.Snapshot{Busy: true, QueueLen: 0, ETAKnown: false})
	if strings.Contains(noeta.Label, "~") || !strings.Contains(noeta.Label, "Someone's generating") {
		t.Errorf("busy/no-eta = %q", noeta.Label)
	}
}
