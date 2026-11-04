package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/company/api-key/internal/store"
)

func seedQuotaToken(t *testing.T, s *Store, employeeID uuid.UUID, total *int64) *store.Token {
	t.Helper()
	sum := sha256.Sum256([]byte(uuid.NewString()))
	tok := &store.Token{
		ID:           uuid.New(),
		EmployeeID:   employeeID,
		Name:         "key-" + uuid.NewString()[:6],
		TokenHash:    hex.EncodeToString(sum[:]),
		TokenPrefix:  "sk-company-abc",
		TokenLast4:   "wxyz",
		QuotaType:    "tokens",
		QuotaPeriod:  "monthly",
		QuotaTotal:   total,
		RateLimitRPM: 60,
		IPAllowlist:  []string{},
		Status:       1,
	}
	if err := s.Tokens.Create(ctx, tok); err != nil {
		t.Fatalf("create token: %v", err)
	}
	return tok
}

func setQuotaUsed(t *testing.T, s *Store, employeeID uuid.UUID, used int64, start time.Time) {
	t.Helper()
	if _, err := s.pool.Exec(ctx,
		`UPDATE employee_quotas SET quota_used=$2, period_start=$3 WHERE employee_id=$1`,
		employeeID, used, start); err != nil {
		t.Fatalf("set quota used: %v", err)
	}
}

func TestEmployeeQuotaSharedPoolAcrossKeys(t *testing.T) {
	s := newTestStore(t)
	e := seedEmployee(t, s)
	t1 := seedQuotaToken(t, s, e.ID, ptr(int64(1_000_000)))
	t2 := seedQuotaToken(t, s, e.ID, nil)
	now := time.Now()

	r1, err := s.EmployeeQuotas.Reserve(ctx, e.ID, t1.ID, 100, 4096, now)
	if err != nil {
		t.Fatalf("reserve key 1: %v", err)
	}
	r2, err := s.EmployeeQuotas.Reserve(ctx, e.ID, t2.ID, 50, 4096, now)
	if err != nil {
		t.Fatalf("reserve key 2: %v", err)
	}
	if err := s.EmployeeQuotas.Settle(ctx, r1, 11); err != nil {
		t.Fatalf("settle key 1: %v", err)
	}
	if err := s.EmployeeQuotas.Settle(ctx, r2, 7); err != nil {
		t.Fatalf("settle key 2: %v", err)
	}

	q, err := s.EmployeeQuotas.Get(ctx, e.ID)
	if err != nil {
		t.Fatalf("get quota: %v", err)
	}
	if q.QuotaUsed != 18 {
		t.Fatalf("shared pool used = %d, want 18", q.QuotaUsed)
	}
	tok1, err := s.Tokens.Get(ctx, t1.ID)
	if err != nil {
		t.Fatalf("get key 1: %v", err)
	}
	tok2, err := s.Tokens.Get(ctx, t2.ID)
	if err != nil {
		t.Fatalf("get key 2: %v", err)
	}
	if tok1.QuotaUsed != 11 || tok2.QuotaUsed != 7 {
		t.Fatalf("per-key usage = %d/%d, want 11/7", tok1.QuotaUsed, tok2.QuotaUsed)
	}
}

func TestEmployeeQuotaExhaustionBlocksEveryKey(t *testing.T) {
	s := newTestStore(t)
	e := seedEmployee(t, s)
	tok := seedQuotaToken(t, s, e.ID, nil)
	other := seedQuotaToken(t, s, e.ID, nil)
	now := time.Now()
	if _, err := s.EmployeeQuotas.SetTotal(ctx, e.ID, "tokens", "monthly", 100, nil, now); err != nil {
		t.Fatalf("set total: %v", err)
	}

	r, err := s.EmployeeQuotas.Reserve(ctx, e.ID, tok.ID, 100, 4096, now)
	if err != nil {
		t.Fatalf("reserve full allowance: %v", err)
	}
	if err := s.EmployeeQuotas.Settle(ctx, r, 100); err != nil {
		t.Fatalf("settle: %v", err)
	}

	_, err = s.EmployeeQuotas.Reserve(ctx, e.ID, other.ID, 1, 4096, now)
	var exceeded *store.QuotaExceededError
	if !errors.As(err, &exceeded) || exceeded.Scope != "employee" {
		t.Fatalf("second key reserve err = %v, want employee quota exceeded", err)
	}
}

func TestEmployeeQuotaKeyCapRollsBackTheEmployeeHold(t *testing.T) {
	s := newTestStore(t)
	e := seedEmployee(t, s)
	tok := seedQuotaToken(t, s, e.ID, ptr(int64(50)))
	now := time.Now()
	if _, err := s.EmployeeQuotas.SetTotal(ctx, e.ID, "tokens", "monthly", 1_000, nil, now); err != nil {
		t.Fatalf("set total: %v", err)
	}

	_, err := s.EmployeeQuotas.Reserve(ctx, e.ID, tok.ID, 60, 4096, now)
	var exceeded *store.QuotaExceededError
	if !errors.As(err, &exceeded) || exceeded.Scope != "token" {
		t.Fatalf("reserve err = %v, want token quota exceeded", err)
	}
	q, err := s.EmployeeQuotas.Get(ctx, e.ID)
	if err != nil {
		t.Fatalf("get quota: %v", err)
	}
	if q.QuotaUsed != 0 {
		t.Fatalf("employee hold leaked after key cap failure: %d", q.QuotaUsed)
	}
	got, err := s.Tokens.Get(ctx, tok.ID)
	if err != nil {
		t.Fatalf("get token: %v", err)
	}
	if got.QuotaUsed != 0 {
		t.Fatalf("token counter = %d, want 0", got.QuotaUsed)
	}
}

func TestEmployeeQuotaRequestUnit(t *testing.T) {
	s := newTestStore(t)
	e := seedEmployee(t, s)
	tok := seedQuotaToken(t, s, e.ID, nil)
	now := time.Now()
	if _, err := s.EmployeeQuotas.SetTotal(ctx, e.ID, "requests", "monthly", 10, nil, now); err != nil {
		t.Fatalf("set total: %v", err)
	}

	r, err := s.EmployeeQuotas.Reserve(ctx, e.ID, tok.ID, 500_000, 4096, now)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if r.QuotaType != "requests" || r.Reserved != 1 {
		t.Fatalf("reservation = %+v, want one request", r)
	}
	if err := s.EmployeeQuotas.Settle(ctx, r, 1); err != nil {
		t.Fatalf("settle: %v", err)
	}
	q, err := s.EmployeeQuotas.Get(ctx, e.ID)
	if err != nil {
		t.Fatalf("get quota: %v", err)
	}
	if q.QuotaUsed != 1 {
		t.Fatalf("used = %d, want 1", q.QuotaUsed)
	}
}

func TestEmployeeQuotaRollsOverRollingPeriods(t *testing.T) {
	s := newTestStore(t)
	e := seedEmployee(t, s)
	tok := seedQuotaToken(t, s, e.ID, nil)
	now := time.Now()
	if _, err := s.EmployeeQuotas.SetTotal(ctx, e.ID, "tokens", "monthly", 100, nil, now); err != nil {
		t.Fatalf("set total: %v", err)
	}
	previous := periodStart("monthly", now).AddDate(0, -1, 0)
	setQuotaUsed(t, s, e.ID, 100, previous)

	r, err := s.EmployeeQuotas.Reserve(ctx, e.ID, tok.ID, 10, 4096, now)
	if err != nil {
		t.Fatalf("reserve after rollover: %v", err)
	}
	q, err := s.EmployeeQuotas.Get(ctx, e.ID)
	if err != nil {
		t.Fatalf("get quota: %v", err)
	}
	if q.QuotaUsed != 10 || !q.PeriodStart.Equal(periodStart("monthly", now)) {
		t.Fatalf("rollover used=%d start=%v", q.QuotaUsed, q.PeriodStart)
	}
	if !r.PeriodStart.Equal(q.PeriodStart) {
		t.Fatalf("reservation start = %v, want %v", r.PeriodStart, q.PeriodStart)
	}
}

func TestEmployeeQuotaSettlementIgnoresRolledPeriod(t *testing.T) {
	s := newTestStore(t)
	e := seedEmployee(t, s)
	tok := seedQuotaToken(t, s, e.ID, nil)
	now := time.Now()
	if _, err := s.EmployeeQuotas.SetTotal(ctx, e.ID, "tokens", "monthly", 1_000, nil, now); err != nil {
		t.Fatalf("set total: %v", err)
	}

	r, err := s.EmployeeQuotas.Reserve(ctx, e.ID, tok.ID, 100, 4096, now)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	stale := r
	stale.PeriodStart = r.PeriodStart.AddDate(0, -1, 0)
	if err := s.EmployeeQuotas.Settle(ctx, stale, 999); err != nil {
		t.Fatalf("settle stale: %v", err)
	}

	q, err := s.EmployeeQuotas.Get(ctx, e.ID)
	if err != nil {
		t.Fatalf("get quota: %v", err)
	}
	if q.QuotaUsed != 100 {
		t.Fatalf("employee used changed across periods: %d", q.QuotaUsed)
	}
	got, err := s.Tokens.Get(ctx, tok.ID)
	if err != nil {
		t.Fatalf("get token: %v", err)
	}
	if got.QuotaUsed != 100 {
		t.Fatalf("key used changed across periods: %d", got.QuotaUsed)
	}
}

func TestEmployeeQuotaSetTotal(t *testing.T) {
	s := newTestStore(t)
	e := seedEmployee(t, s)
	tok := seedQuotaToken(t, s, e.ID, nil)
	now := time.Now()

	q, err := s.EmployeeQuotas.SetTotal(ctx, e.ID, "tokens", "monthly", 1_000, nil, now)
	if err != nil {
		t.Fatalf("set total: %v", err)
	}
	if q.QuotaTotal != 1_000 || q.QuotaPeriod != "monthly" {
		t.Fatalf("created quota = %+v", q)
	}

	r, err := s.EmployeeQuotas.Reserve(ctx, e.ID, tok.ID, 10, 4096, now)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if err := s.EmployeeQuotas.Settle(ctx, r, 10); err != nil {
		t.Fatalf("settle: %v", err)
	}

	// The same period preserves usage; a new period starts from zero.
	q, err = s.EmployeeQuotas.SetTotal(ctx, e.ID, "tokens", "monthly", 2_000, nil, now)
	if err != nil {
		t.Fatalf("raise same period: %v", err)
	}
	if q.QuotaUsed != 10 || q.QuotaTotal != 2_000 {
		t.Fatalf("same-period update = %+v", q)
	}
	q, err = s.EmployeeQuotas.SetTotal(ctx, e.ID, "tokens", "daily", 3_000, nil, now)
	if err != nil {
		t.Fatalf("change period: %v", err)
	}
	if q.QuotaUsed != 0 || q.QuotaPeriod != "daily" || !q.PeriodStart.Equal(periodStart("daily", now)) {
		t.Fatalf("period-change update = %+v", q)
	}
}

func TestEmployeeQuotaConcurrentReservation(t *testing.T) {
	s := newTestStore(t)
	e := seedEmployee(t, s)
	tok := seedQuotaToken(t, s, e.ID, nil)
	now := time.Now()
	if _, err := s.EmployeeQuotas.SetTotal(ctx, e.ID, "requests", "monthly", 20, nil, now); err != nil {
		t.Fatalf("set total: %v", err)
	}

	var (
		wg        sync.WaitGroup
		succeeded atomic.Int64
		exceeded  atomic.Int64
	)
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.EmployeeQuotas.Reserve(context.Background(), e.ID, tok.ID, 1, 4096, now)
			if err == nil {
				succeeded.Add(1)
				return
			}
			var qerr *store.QuotaExceededError
			if errors.As(err, &qerr) && qerr.Scope == "employee" {
				exceeded.Add(1)
				return
			}
			t.Errorf("unexpected reserve error: %v", err)
		}()
	}
	wg.Wait()

	if succeeded.Load() != 20 || exceeded.Load() != 20 {
		t.Fatalf("succeeded=%d exceeded=%d, want 20/20", succeeded.Load(), exceeded.Load())
	}
	q, err := s.EmployeeQuotas.Get(ctx, e.ID)
	if err != nil {
		t.Fatalf("get quota: %v", err)
	}
	if q.QuotaUsed != 20 {
		t.Fatalf("used = %d, want 20", q.QuotaUsed)
	}
}
