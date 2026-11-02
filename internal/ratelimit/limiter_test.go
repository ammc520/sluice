package ratelimit

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMemoryLimiterRefills(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	l := NewMemory()
	l.SetClock(func() time.Time { return now })
	ctx := context.Background()
	if ok, _, _ := l.Allow(ctx, "t", 1); !ok {
		t.Fatal("first denied")
	}
	now = now.Add(time.Second)
	if ok, _, _ := l.Allow(ctx, "t", 1); ok {
		t.Fatal("second allowed with limit 1")
	}
	// One token refills per minute at limit=1.
	now = now.Add(time.Minute)
	if ok, _, _ := l.Allow(ctx, "t", 1); !ok {
		t.Fatal("refill denied")
	}
}

func TestMemoryLimiterRetryAfter(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	l := NewMemory()
	l.SetClock(func() time.Time { return now })
	l.Allow(context.Background(), "t", 1)
	ok, retry, _ := l.Allow(context.Background(), "t", 1)
	if ok || retry <= 0 || retry > time.Minute+10*time.Millisecond {
		t.Fatalf("ok=%v retry=%v", ok, retry)
	}
}

type failingLimiter struct{ err error }

func (f *failingLimiter) Allow(context.Context, string, int) (bool, time.Duration, error) {
	return false, 0, f.err
}

func TestFallbackLimiterUsesMemoryWhenRedisFails(t *testing.T) {
	primary := &failingLimiter{err: errors.New("redis down")}
	fallback := NewMemory()
	l := NewFallback(primary, fallback)
	if ok, _, err := l.Allow(context.Background(), "t", 1); !ok || err != nil {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if l.DegradedCount() != 1 {
		t.Fatalf("degraded=%d", l.DegradedCount())
	}
}
