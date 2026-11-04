package postgres

import (
	"errors"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/company/sluice/internal/store"
)

func uuidMust() uuid.UUID { return uuid.New() }

func monthStart(now time.Time) time.Time {
	local := now.In(shanghai)
	return time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, shanghai)
}

func TestTokenFindByHash(t *testing.T) {
	s, tok := seedToken(t, 100, 0)
	got, err := s.Tokens.FindByHash(ctx, tok.TokenHash)
	if err != nil || got.ID != tok.ID {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	if got.QuotaTotal == nil || *got.QuotaTotal != 100 {
		t.Fatalf("quota total lost: %+v", got)
	}
}

func TestReserveQuotaRejectsExhausted(t *testing.T) {
	s, tok := seedToken(t, 100, 0)
	now := time.Now()
	if _, err := s.Tokens.ReserveQuota(ctx, tok.ID, 100, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Tokens.ReserveQuota(ctx, tok.ID, 1, now); !errors.Is(err, store.ErrQuotaExceeded) {
		t.Fatalf("got %v", err)
	}
}

func TestSettleQuotaRefundsDifference(t *testing.T) {
	s, tok := seedToken(t, 100, 0)
	now := time.Now()
	r, err := s.Tokens.ReserveQuota(ctx, tok.ID, 60, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Tokens.SettleQuota(ctx, r, 20); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Tokens.Get(ctx, tok.ID)
	if got.QuotaUsed != 20 {
		t.Fatalf("got %d", got.QuotaUsed)
	}
}

func TestReserveQuotaResetsMonthlyPeriod(t *testing.T) {
	s, tok := seedToken(t, 10, 10)
	now := time.Now()
	prev := now.AddDate(0, -1, 0)
	tok.PeriodStart = &prev
	if err := s.Tokens.Update(ctx, tok); err != nil {
		t.Fatal(err)
	}
	r, err := s.Tokens.ReserveQuota(ctx, tok.ID, 1, now)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := s.Tokens.Get(ctx, tok.ID)
	if got.QuotaUsed != 1 || !r.PeriodStart.Equal(monthStart(now)) {
		t.Fatalf("used=%d start=%v want=%v", got.QuotaUsed, r.PeriodStart, monthStart(now))
	}
}

func TestReserveQuotaUnlimitedToken(t *testing.T) {
	s, tok := seedToken(t, 100, 0)
	now := time.Now()
	tok.QuotaTotal = nil
	if err := s.Tokens.Update(ctx, tok); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Tokens.ReserveQuota(ctx, tok.ID, 1000000, now); err != nil {
		t.Fatal(err)
	}
}

func TestReserveQuotaConcurrentOnlyOneSucceeds(t *testing.T) {
	s, tok := seedToken(t, 100, 0)
	now := time.Now()
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Tokens.ReserveQuota(ctx, tok.ID, 60, now)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	ok, exceeded := 0, 0
	for err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, store.ErrQuotaExceeded):
			exceeded++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if ok != 1 || exceeded != 1 {
		t.Fatalf("ok=%d exceeded=%d", ok, exceeded)
	}
}

func TestReserveQuotaHugeReserveCannotOverflow(t *testing.T) {
	s, tok := seedToken(t, 100, 0)
	if _, err := s.Tokens.ReserveQuota(ctx, tok.ID, math.MaxInt64, time.Now()); !errors.Is(err, store.ErrQuotaExceeded) {
		t.Fatalf("got %v", err)
	}
	if _, err := s.Tokens.ReserveQuota(ctx, tok.ID, -1, time.Now()); !errors.Is(err, store.ErrQuotaExceeded) {
		t.Fatalf("negative reserve got %v", err)
	}
}

func TestSettleQuotaClampsToTotal(t *testing.T) {
	s, tok := seedToken(t, 100, 0)
	r, err := s.Tokens.ReserveQuota(ctx, tok.ID, 10, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Tokens.SettleQuota(ctx, r, 1000); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Tokens.Get(ctx, tok.ID)
	if got.QuotaUsed != 100 {
		t.Fatalf("used=%d, want clamped to 100", got.QuotaUsed)
	}
}

func TestReserveQuotaZeroReserveChecksRemaining(t *testing.T) {
	s, tok := seedToken(t, 5, 5)
	if _, err := s.Tokens.ReserveQuota(ctx, tok.ID, 0, time.Now()); !errors.Is(err, store.ErrQuotaExceeded) {
		t.Fatalf("got %v", err)
	}
}

func TestTokenUpdateAndList(t *testing.T) {
	s, tok := seedToken(t, 100, 0)
	tok.RateLimitRPM = 120
	tok.Status = 0
	if err := s.Tokens.Update(ctx, tok); err != nil {
		t.Fatal(err)
	}
	status := int16(0)
	list, total, err := s.Tokens.List(ctx, store.TokenFilter{Status: &status})
	if err != nil || total != 1 || len(list) != 1 || list[0].RateLimitRPM != 120 {
		t.Fatalf("total=%d list=%+v err=%v", total, list, err)
	}
}

func TestTouchUpdatesLastUsed(t *testing.T) {
	s, tok := seedToken(t, 100, 0)
	at := time.Now().Add(-time.Minute)
	if err := s.Tokens.Touch(ctx, tok.ID, at); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Tokens.Get(ctx, tok.ID)
	if got.LastUsedAt == nil || got.LastUsedAt.Sub(at).Abs() > time.Second {
		t.Fatalf("last used %v", got.LastUsedAt)
	}
}
