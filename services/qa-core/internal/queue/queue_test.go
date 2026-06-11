package queue

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"qa-core/internal/contract"
	"qa-core/internal/eta"
)

// TestOneAtATime asserts the core guarantee: the single worker never runs two
// jobs concurrently (PRD §5.4/§6).
func TestOneAtATime(t *testing.T) {
	var concurrent, maxConcurrent int32
	runner := func(ctx context.Context, _ contract.GenerateRequest) (contract.Result, error) {
		c := atomic.AddInt32(&concurrent, 1)
		if c > atomic.LoadInt32(&maxConcurrent) {
			atomic.StoreInt32(&maxConcurrent, c)
		}
		time.Sleep(10 * time.Millisecond)
		atomic.AddInt32(&concurrent, -1)
		return contract.Result{TestCases: []contract.TestCase{{ID: "TC-1"}}}, nil
	}

	m := New(Config{Buffer: 64, GenTimeout: time.Second, ETA: eta.New(20), Runner: runner})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)

	for i := 0; i < 12; i++ {
		if _, err := m.Submit(contract.GenerateRequest{Requirement: fmt.Sprintf("r%d", i)}); err != nil {
			t.Fatalf("submit: %v", err)
		}
	}

	waitUntil(t, 2*time.Second, func() bool {
		_, ok := lastDone(m, 12)
		return ok
	})
	if got := atomic.LoadInt32(&maxConcurrent); got != 1 {
		t.Fatalf("max concurrent runners = %d, want 1", got)
	}
}

// TestQueueFull asserts backpressure rather than unbounded growth.
func TestQueueFull(t *testing.T) {
	block := make(chan struct{})
	runner := func(ctx context.Context, _ contract.GenerateRequest) (contract.Result, error) {
		<-block // first job blocks, holding the worker
		return contract.Result{}, nil
	}
	m := New(Config{Buffer: 1, GenTimeout: time.Second, ETA: eta.New(20), Runner: runner})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer close(block)
	m.Start(ctx)

	// 1 picked up by worker (blocks), 1 fills the buffer, rest must be rejected.
	var rejected int
	for i := 0; i < 10; i++ {
		if _, err := m.Submit(contract.GenerateRequest{Requirement: "x"}); err == ErrQueueFull {
			rejected++
		}
		time.Sleep(time.Millisecond)
	}
	if rejected == 0 {
		t.Fatal("expected some submissions to be rejected with ErrQueueFull")
	}
}

// TestJobViewPosition checks FIFO positions are reported to submitters.
func TestJobViewPosition(t *testing.T) {
	block := make(chan struct{})
	var once sync.Once
	runner := func(ctx context.Context, _ contract.GenerateRequest) (contract.Result, error) {
		once.Do(func() { <-block }) // hold only the first job
		return contract.Result{}, nil
	}
	m := New(Config{Buffer: 64, GenTimeout: time.Second, ETA: eta.New(20), Runner: runner})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer close(block)
	m.Start(ctx)

	_, _ = m.Submit(contract.GenerateRequest{Requirement: "running"})
	time.Sleep(5 * time.Millisecond)
	j2, _ := m.Submit(contract.GenerateRequest{Requirement: "second"})

	view, ok := m.JobView(j2.ID)
	if !ok || view.State != StateQueued || view.Position != 1 {
		t.Fatalf("view = %+v, want queued at position 1", view)
	}
}

func lastDone(m *Manager, n int) (int, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.finished), len(m.finished) >= n
}

func waitUntil(t *testing.T, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.After(d)
	tick := time.NewTicker(2 * time.Millisecond)
	defer tick.Stop()
	for {
		if cond() {
			return
		}
		select {
		case <-deadline:
			t.Fatal("condition not met before deadline")
		case <-tick.C:
		}
	}
}
