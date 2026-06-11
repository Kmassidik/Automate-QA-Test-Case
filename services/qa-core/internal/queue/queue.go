// Package queue is the concurrency core (PRD §5.4, §6): a buffered channel fed
// by submissions and drained by exactly ONE worker goroutine, guaranteeing
// strict FIFO, one-job-at-a-time execution. Because only the single worker calls
// the runner, qa-ai never receives concurrent requests (it matches the single
// Ollama instance). All state lives in memory — no Redis, no DB (v1).
package queue

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"qa-core/internal/contract"
	"qa-core/internal/eta"
)

type State string

const (
	StateQueued  State = "queued"
	StateRunning State = "running"
	StateDone    State = "done"
	StateFailed  State = "failed"
)

type Job struct {
	ID         string
	State      State
	Req        contract.GenerateRequest
	Result     *contract.Result
	Err        string
	EnqueuedAt time.Time
	StartedAt  time.Time
	FinishedAt time.Time
}

// Runner performs the actual generation (the qa-ai HTTP call). Injected so the
// queue stays transport-agnostic and testable.
type Runner func(ctx context.Context, req contract.GenerateRequest) (contract.Result, error)

// ErrQueueFull is returned when the buffered channel is saturated (backpressure
// instead of unbounded memory growth).
var ErrQueueFull = errors.New("queue is full, please retry shortly")

type Manager struct {
	mu       sync.RWMutex
	jobs     map[string]*Job
	queued   []string // FIFO order of queued job IDs
	running  *Job
	finished []string // completion order, for retention eviction

	ch         chan *Job
	eta        *eta.Tracker
	runner     Runner
	genTimeout time.Duration
	retain     int
	onChange   func()
}

type Config struct {
	Buffer     int           // channel capacity (backpressure threshold)
	GenTimeout time.Duration // per-job hard timeout
	Retain     int           // finished jobs kept for result/export retrieval
	ETA        *eta.Tracker
	Runner     Runner
	OnChange   func() // invoked after every state transition (drives SSE)
}

func New(cfg Config) *Manager {
	if cfg.Buffer < 1 {
		cfg.Buffer = 256
	}
	if cfg.Retain < 1 {
		cfg.Retain = 200
	}
	if cfg.OnChange == nil {
		cfg.OnChange = func() {}
	}
	return &Manager{
		jobs:       make(map[string]*Job),
		ch:         make(chan *Job, cfg.Buffer),
		eta:        cfg.ETA,
		runner:     cfg.Runner,
		genTimeout: cfg.GenTimeout,
		retain:     cfg.Retain,
		onChange:   cfg.OnChange,
	}
}

// Start launches the single worker goroutine. Call once. It returns when ctx is
// cancelled (graceful shutdown).
func (m *Manager) Start(ctx context.Context) {
	go m.worker(ctx)
}

// Submit enqueues a job and returns it in StateQueued, or ErrQueueFull.
func (m *Manager) Submit(req contract.GenerateRequest) (*Job, error) {
	job := &Job{
		ID:         newID(),
		State:      StateQueued,
		Req:        req,
		EnqueuedAt: time.Now(),
	}

	m.mu.Lock()
	m.jobs[job.ID] = job
	m.queued = append(m.queued, job.ID)
	m.mu.Unlock()

	select {
	case m.ch <- job:
	default:
		// Channel saturated: roll back the bookkeeping and signal backpressure.
		m.mu.Lock()
		delete(m.jobs, job.ID)
		m.queued = removeID(m.queued, job.ID)
		m.mu.Unlock()
		return nil, ErrQueueFull
	}

	m.onChange()
	return job, nil
}

func (m *Manager) worker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-m.ch:
			m.runOne(ctx, job)
		}
	}
}

func (m *Manager) runOne(ctx context.Context, job *Job) {
	m.mu.Lock()
	job.State = StateRunning
	job.StartedAt = time.Now()
	m.queued = removeID(m.queued, job.ID)
	m.running = job
	m.mu.Unlock()
	m.onChange()

	jobCtx, cancel := context.WithTimeout(ctx, m.genTimeout)
	res, err := m.runner(jobCtx, job.Req)
	cancel()

	m.mu.Lock()
	job.FinishedAt = time.Now()
	if err != nil {
		job.State = StateFailed
		job.Err = err.Error()
	} else {
		job.State = StateDone
		job.Result = &res
		if m.eta != nil {
			m.eta.Record(job.FinishedAt.Sub(job.StartedAt))
		}
	}
	m.running = nil
	m.finished = append(m.finished, job.ID)
	m.evictLocked()
	m.mu.Unlock()
	m.onChange()
}

// evictLocked drops the oldest finished jobs beyond the retention cap. Caller
// holds m.mu.
func (m *Manager) evictLocked() {
	for len(m.finished) > m.retain {
		oldest := m.finished[0]
		m.finished = m.finished[1:]
		delete(m.jobs, oldest)
	}
}

func (m *Manager) Get(id string) (*Job, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	j, ok := m.jobs[id]
	return j, ok
}

// ----- views for the UI / SSE -----

// Snapshot is the global, everyone-sees-it queue status (PRD §5.4).
type Snapshot struct {
	Busy      bool
	RunningID string
	QueueLen  int
	ETAPerJob time.Duration
	ETAKnown  bool
}

func (m *Manager) Snapshot() Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	avg, ok := m.etaAvg()
	s := Snapshot{
		Busy:      m.running != nil,
		QueueLen:  len(m.queued),
		ETAPerJob: avg,
		ETAKnown:  ok,
	}
	if m.running != nil {
		s.RunningID = m.running.ID
	}
	return s
}

// JobView is a single submitter's personalized status.
type JobView struct {
	ID       string
	State    State
	Position int           // 1-based place in line; 0 when running/done/failed
	ETA      time.Duration // estimated time until this job completes
	ETAKnown bool
	Err      string
}

func (m *Manager) JobView(id string) (JobView, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	job, ok := m.jobs[id]
	if !ok {
		return JobView{}, false
	}
	v := JobView{ID: id, State: job.State, Err: job.Err}

	if job.State == StateQueued {
		v.Position = indexOf(m.queued, id) + 1 // 1-based
		if avg, known := m.etaAvg(); known {
			// Wait = (jobs running ahead) + (jobs queued ahead) + this job.
			ahead := v.Position // includes self; add the running job if any
			if m.running != nil {
				ahead++
			}
			v.ETA = avg * time.Duration(ahead)
			v.ETAKnown = true
		}
	}
	return v, true
}

// etaAvg is a lock-free-of-its-own helper; callers already hold m.mu.
func (m *Manager) etaAvg() (time.Duration, bool) {
	if m.eta == nil {
		return 0, false
	}
	return m.eta.Average()
}

// ----- helpers -----

func newID() string {
	var b [9]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func removeID(s []string, id string) []string {
	for i, v := range s {
		if v == id {
			return append(s[:i], s[i+1:]...)
		}
	}
	return s
}

func indexOf(s []string, id string) int {
	for i, v := range s {
		if v == id {
			return i
		}
	}
	return -1
}
