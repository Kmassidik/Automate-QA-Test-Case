// Package readiness tracks whether qa-ai can actually serve generations: the
// Ollama daemon must be reachable AND the model present. The warmup loop sets
// the state; /healthz reports it and /generate refuses early (with a clear
// reason) instead of hanging when the model isn't ready yet.
package readiness

import "sync"

type State string

const (
	StateStarting   State = "starting"    // initial; warmup not finished a cycle yet
	StateOllamaDown State = "ollama_down" // daemon unreachable
	StatePulling    State = "pulling"     // model is downloading
	StateReady      State = "ready"       // good to generate
)

type Readiness struct {
	mu       sync.RWMutex
	state    State
	detail   string // human-readable reason / guidance / progress
	progress string // optional pull progress, e.g. "62%"
}

func New() *Readiness {
	return &Readiness{state: StateStarting, detail: "starting up"}
}

func (r *Readiness) Set(s State, detail string) {
	r.mu.Lock()
	r.state, r.detail = s, detail
	if s != StatePulling {
		r.progress = ""
	}
	r.mu.Unlock()
}

func (r *Readiness) SetProgress(p string) {
	r.mu.Lock()
	r.progress = p
	r.mu.Unlock()
}

func (r *Readiness) Ready() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.state == StateReady
}

type Snapshot struct {
	State    State  `json:"state"`
	Detail   string `json:"detail"`
	Progress string `json:"progress,omitempty"`
}

func (r *Readiness) Snapshot() Snapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return Snapshot{State: r.state, Detail: r.detail, Progress: r.progress}
}
