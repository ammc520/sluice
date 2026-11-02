package ratelimit

import (
	"context"
	"sync"
	"time"
)

type bucket struct {
	tokens float64
	last   time.Time
}

// Memory is an in-process token bucket keyed by token ID.
type Memory struct {
	mu        sync.Mutex
	buckets   map[string]*bucket
	clock     func() time.Time
	lastSweep time.Time
}

// NewMemory creates an in-memory limiter.
func NewMemory() *Memory {
	return &Memory{buckets: map[string]*bucket{}, clock: time.Now}
}

// SetClock replaces the clock, for tests.
func (m *Memory) SetClock(clock func() time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clock = clock
}

func (m *Memory) Allow(_ context.Context, key string, limit int) (bool, time.Duration, error) {
	if limit <= 0 {
		limit = 60
	}
	now := m.clock()

	m.mu.Lock()
	defer m.mu.Unlock()

	m.sweep(now)

	b, ok := m.buckets[key]
	capacity := float64(limit)
	rate := capacity / 60.0
	if !ok {
		b = &bucket{tokens: capacity, last: now}
		m.buckets[key] = b
	}
	if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
		b.tokens += elapsed * rate
		if b.tokens > capacity {
			b.tokens = capacity
		}
	}
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0, nil
	}
	wait := time.Duration((1-b.tokens)/rate*float64(time.Second)) + time.Millisecond
	return false, wait, nil
}

func (m *Memory) sweep(now time.Time) {
	if now.Sub(m.lastSweep) < 2*time.Minute {
		return
	}
	m.lastSweep = now
	for k, b := range m.buckets {
		if now.Sub(b.last) > 2*time.Minute {
			delete(m.buckets, k)
		}
	}
}
