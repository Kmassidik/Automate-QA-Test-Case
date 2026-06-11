// Package sse is a minimal Server-Sent-Events broadcaster (PRD §5.4): a one-way
// server->browser push of "queue changed" ticks. It carries no payload — on each
// tick a subscribed handler re-reads the queue and writes a personalized frame.
// This keeps the broadcaster decoupled from queue internals.
package sse

import "sync"

type Broadcaster struct {
	mu   sync.RWMutex
	subs map[chan struct{}]struct{}
}

func NewBroadcaster() *Broadcaster {
	return &Broadcaster{subs: make(map[chan struct{}]struct{})}
}

// Subscribe registers a new listener and returns its tick channel plus a
// cancel func the handler must defer to avoid leaking subscribers.
func (b *Broadcaster) Subscribe() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1) // capacity 1: coalesces bursts into one pending tick
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()

	cancel := func() {
		b.mu.Lock()
		if _, ok := b.subs[ch]; ok {
			delete(b.subs, ch)
			close(ch)
		}
		b.mu.Unlock()
	}
	return ch, cancel
}

// Publish signals every subscriber that state changed. Non-blocking: if a
// subscriber already has a pending tick, this one is coalesced (dropped),
// because a tick is just "go re-read" — losing a duplicate is harmless.
func (b *Broadcaster) Publish() {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}
