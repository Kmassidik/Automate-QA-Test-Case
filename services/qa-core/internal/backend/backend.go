// Package backend monitors the qa-ai generation backend so the UI can show its
// real state — model name when ready, download progress while pulling, or
// offline — instead of a hard-coded "local LLM" string. It polls qa-ai/healthz
// on an interval and republishes (via onChange) whenever the state changes, so
// the existing SSE stream pushes backend updates to browsers with no refresh.
package backend

import (
	"context"
	"sync"
	"time"

	"qa-core/internal/aiclient"
)

type Monitor struct {
	ai       *aiclient.Client
	mu       sync.RWMutex
	cur      aiclient.BackendStatus
	curStats aiclient.Stats
	onChange func()
}

func NewMonitor(ai *aiclient.Client, onChange func()) *Monitor {
	if onChange == nil {
		onChange = func() {}
	}
	return &Monitor{ai: ai, onChange: onChange}
}

// Start polls every interval until ctx is done, doing one immediate poll first
// so the UI has truth at startup.
func (m *Monitor) Start(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		m.poll(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				m.poll(ctx)
			}
		}
	}()
}

func (m *Monitor) poll(ctx context.Context) {
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	s := m.ai.Status(cctx)
	stats := m.ai.Stats(cctx)

	m.mu.Lock()
	m.cur = s
	m.curStats = stats
	m.mu.Unlock()

	// Push every poll: backend state may change (pull %, became ready) and the
	// resource stats (CPU/RAM/loaded model) update continuously, so the widget
	// stays live without a refresh.
	m.onChange()
}

func (m *Monitor) Current() aiclient.BackendStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cur
}

func (m *Monitor) CurrentStats() aiclient.Stats {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.curStats
}
