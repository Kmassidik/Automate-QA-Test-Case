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

// Kind distinguishes the two job types that share the single FIFO queue: a
// validation pass (analysis only) and a full generation. Both serialize through
// the same worker, so global queue visibility is identical for either.
type Kind string

const (
	KindGenerate Kind = "generate"
	KindValidate Kind = "validate"
	KindMOM      Kind = "mom" // PM tab: audio -> transcript -> minutes
)

// StepState is the status of one step in a job's plan (the 1..N checklist a
// multi-step runner advances through, surfaced live to the user).
type StepState string

const (
	StepPending StepState = "pending"
	StepRunning StepState = "running"
	StepDone    StepState = "done"
	StepFailed  StepState = "failed"
)

// Step is one unit of work in a job's plan, e.g. "Test cases for AC-3".
type Step struct {
	Label  string    `json:"label"`
	State  StepState `json:"state"`
	Detail string    `json:"detail,omitempty"`
}

type Job struct {
	ID         string
	Kind       Kind
	State      State
	Req        contract.GenerateRequest
	Plan       []Step           // 1..N checklist the runner advances through
	Partial    *contract.Result // accumulates as steps complete (in-memory temp store)
	Result     *contract.Result
	Err        string
	EnqueuedAt time.Time
	StartedAt  time.Time
	FinishedAt time.Time
}

// Runner performs the actual generation (the qa-ai HTTP call). Injected so the
// queue stays transport-agnostic and testable. A runner reports its 1..N plan
// and advances it via the Progress handle, which the worker binds to the job.
type Runner func(ctx context.Context, req contract.GenerateRequest, p *Progress) (contract.Result, error)

// Progress lets a Runner publish its step plan and advance through it. Every
// mutation updates the job (under the manager lock) and fires a broadcast, so
// the browser sees "step 3 of 8" live over SSE — no polling.
type Progress struct {
	m   *Manager
	job *Job
}

// NewProgress returns a Progress that reports against job using the manager's
// lock + broadcast. The worker uses it; orchestrators/tests can too.
func (m *Manager) NewProgress(job *Job) *Progress {
	return &Progress{m: m, job: job}
}

// JobID is the id of the job this Progress reports for (immutable; lock-free).
func (p *Progress) JobID() string { return p.job.ID }

// Plan sets the initial step list (all pending).
func (p *Progress) Plan(labels ...string) {
	p.m.mu.Lock()
	p.job.Plan = make([]Step, len(labels))
	for i, l := range labels {
		p.job.Plan[i] = Step{Label: l, State: StepPending}
	}
	p.m.mu.Unlock()
	p.m.onChange()
}

// Append adds a step to a dynamic plan (used when N is discovered mid-run, e.g.
// once acceptance criteria are known) and returns its index.
func (p *Progress) Append(label string) int {
	p.m.mu.Lock()
	p.job.Plan = append(p.job.Plan, Step{Label: label, State: StepPending})
	i := len(p.job.Plan) - 1
	p.m.mu.Unlock()
	p.m.onChange()
	return i
}

func (p *Progress) set(i int, st StepState, detail string) {
	p.m.mu.Lock()
	if i >= 0 && i < len(p.job.Plan) {
		p.job.Plan[i].State = st
		if detail != "" {
			p.job.Plan[i].Detail = detail
		}
	}
	p.m.mu.Unlock()
	p.m.onChange()
}

// Start/Done/Fail advance step i. Partial records intermediate results so a
// streaming UI (and crash recovery) can read work-in-progress.
func (p *Progress) Start(i int)               { p.set(i, StepRunning, "") }
func (p *Progress) Done(i int, detail string) { p.set(i, StepDone, detail) }
func (p *Progress) Fail(i int, detail string) { p.set(i, StepFailed, detail) }

// Partial publishes the work-in-progress result (in-memory temp store) so a
// streaming view can render rows as they land.
func (p *Progress) Partial(res contract.Result) {
	p.m.mu.Lock()
	cp := res
	p.job.Partial = &cp
	p.m.mu.Unlock()
	p.m.onChange()
}

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
	validator  Runner
	momRunner  Runner
	genTimeout time.Duration
	retain     int
	onChange   func()
}

type Config struct {
	Buffer     int           // channel capacity (backpressure threshold)
	GenTimeout time.Duration // per-job hard timeout
	Retain     int           // finished jobs kept for result/export retrieval
	ETA        *eta.Tracker
	Runner     Runner // handles KindGenerate
	Validator  Runner // handles KindValidate
	MOMRunner  Runner // handles KindMOM (audio -> minutes)
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
		validator:  cfg.Validator,
		momRunner:  cfg.MOMRunner,
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

// Submit enqueues a job of the given kind and returns it in StateQueued, or
// ErrQueueFull. Both kinds share the one FIFO queue and single worker.
func (m *Manager) Submit(kind Kind, req contract.GenerateRequest) (*Job, error) {
	job := &Job{
		ID:         newID(),
		Kind:       kind,
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

	run := m.runner
	switch {
	case job.Kind == KindValidate && m.validator != nil:
		run = m.validator
	case job.Kind == KindMOM && m.momRunner != nil:
		run = m.momRunner
	}

	prog := m.NewProgress(job)
	jobCtx, cancel := context.WithTimeout(ctx, m.genTimeout)
	res, err := run(jobCtx, job.Req, prog)
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
	Plan     []Step // 1..N step checklist (nil for single-shot / queued jobs)
}

func (m *Manager) JobView(id string) (JobView, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	job, ok := m.jobs[id]
	if !ok {
		return JobView{}, false
	}
	v := JobView{ID: id, State: job.State, Err: job.Err}
	if len(job.Plan) > 0 {
		v.Plan = append([]Step(nil), job.Plan...) // copy under lock
	}

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
