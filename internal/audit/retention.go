package audit

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// AuditRetentionStore deletes audit records older than a cutoff.
type AuditRetentionStore interface {
	DeleteBefore(ctx context.Context, before time.Time) (int64, error)
}

// Retention deletes expired audit records once at startup and then daily at
// 03:00 Asia/Shanghai.
type Retention struct {
	store  AuditRetentionStore
	days   func(context.Context) int
	clock  func() time.Time
	logger *slog.Logger
}

// NewRetention creates the audit retention job. clock may be nil.
func NewRetention(store AuditRetentionStore, retentionDays int, clock func() time.Time) *Retention {
	return NewRetentionFunc(store, func(context.Context) int { return retentionDays }, clock)
}

// NewRetentionFunc creates a retention job whose window can change between
// runs, for example when an administrator edits the audit settings. days is
// evaluated once per run and must return a positive value.
func NewRetentionFunc(store AuditRetentionStore, days func(context.Context) int, clock func() time.Time) *Retention {
	if clock == nil {
		clock = time.Now
	}
	if days == nil {
		days = func(context.Context) int { return 0 }
	}
	return &Retention{
		store:  store,
		days:   days,
		clock:  clock,
		logger: slog.Default(),
	}
}

// SetLogger overrides the logger used by Run.
func (r *Retention) SetLogger(logger *slog.Logger) {
	if logger != nil {
		r.logger = logger
	}
}

// RunOnce deletes records older than the configured retention window. The
// cutoff is computed in Asia/Shanghai so daily boundaries are stable.
func (r *Retention) RunOnce(ctx context.Context) (int64, error) {
	if r.store == nil {
		return 0, errors.New("audit retention: store is required")
	}
	days := 0
	if r.days != nil {
		days = r.days(ctx)
	}
	if days <= 0 {
		return 0, errors.New("audit retention: retention days must be positive")
	}
	now := r.clock().In(shanghaiLocation())
	cutoff := now.AddDate(0, 0, -days)
	return r.store.DeleteBefore(ctx, cutoff)
}

// Run executes once immediately, then once per day at 03:00 Asia/Shanghai.
// Failures are logged and retried on the next scheduled run.
func (r *Retention) Run(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, err := r.RunOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
		r.logger.Error("audit retention run failed", "error", err)
	}

	for {
		next := nextRetentionRun(r.clock())
		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		case <-timer.C:
		}

		if _, err := r.RunOnce(ctx); err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			r.logger.Error("audit retention run failed", "error", err)
		}
	}
}

func nextRetentionRun(now time.Time) time.Time {
	loc := shanghaiLocation()
	local := now.In(loc)
	next := time.Date(local.Year(), local.Month(), local.Day(), 3, 0, 0, 0, loc)
	if !next.After(local) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

func shanghaiLocation() *time.Location {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err == nil {
		return loc
	}
	return time.FixedZone("CST", 8*60*60)
}
