package ratelimit

import (
	"context"
	"sync/atomic"
	"time"
)

// Limiter allows limit operations per minute for a key.
type Limiter interface {
	Allow(ctx context.Context, key string, limit int) (allowed bool, retryAfter time.Duration, err error)
}

// Fallback uses a secondary limiter when the primary fails (for example when
// Redis is unavailable). A Redis outage must not take the gateway down.
type Fallback struct {
	primary  Limiter
	fallback Limiter
	degraded atomic.Int64
}

// NewFallback builds a fallback limiter.
func NewFallback(primary, fallback Limiter) *Fallback {
	return &Fallback{primary: primary, fallback: fallback}
}

func (f *Fallback) Allow(ctx context.Context, key string, limit int) (bool, time.Duration, error) {
	allowed, retry, err := f.primary.Allow(ctx, key, limit)
	if err != nil {
		f.degraded.Add(1)
		return f.fallback.Allow(ctx, key, limit)
	}
	return allowed, retry, nil
}

// DegradedCount reports how many requests fell back.
func (f *Fallback) DegradedCount() int64 { return f.degraded.Load() }
