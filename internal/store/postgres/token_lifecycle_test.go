package postgres

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/company/api-key/internal/store"
)

func newLifecycleToken(employeeID uuid.UUID) *store.Token {
	return &store.Token{
		ID:           uuid.New(),
		EmployeeID:   employeeID,
		Name:         "Key",
		TokenHash:    lifecycleHash(),
		TokenPrefix:  "sk-company-abc",
		TokenLast4:   "wxyz",
		QuotaType:    "tokens",
		QuotaPeriod:  "monthly",
		RateLimitRPM: 60,
		IPAllowlist:  []string{},
		Status:       1,
	}
}

func lifecycleHash() string {
	sum := sha256.Sum256([]byte(uuid.NewString()))
	return hex.EncodeToString(sum[:])
}

func TestCreateWithLimitIsAtomic(t *testing.T) {
	s := newTestStore(t)
	e := seedEmployee(t, s)

	const (
		limit      = 3
		goroutines = 12
	)
	var wg sync.WaitGroup
	results := make(chan error, goroutines)
	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- s.Tokens.CreateWithLimit(ctx, newLifecycleToken(e.ID), limit, time.Now())
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	created, limited := 0, 0
	for err := range results {
		switch {
		case err == nil:
			created++
		case errors.Is(err, store.ErrTokenLimitReached):
			limited++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if created != limit || limited != goroutines-limit {
		t.Fatalf("created=%d limited=%d, want %d/%d", created, limited, limit, goroutines-limit)
	}
	if active, err := s.Tokens.CountActiveByEmployee(ctx, e.ID, time.Now()); err != nil || active != limit {
		t.Fatalf("active=%d err=%v, want %d", active, err, limit)
	}
}

func TestCreateWithLimitIgnoresExpiredAndRevokedKeys(t *testing.T) {
	s := newTestStore(t)
	e := seedEmployee(t, s)

	expired := newLifecycleToken(e.ID)
	expired.ExpiresAt = ptr(time.Now().Add(-time.Hour))
	if err := s.Tokens.Create(ctx, expired); err != nil {
		t.Fatalf("seed expired: %v", err)
	}
	revoked := newLifecycleToken(e.ID)
	revoked.Status = 0
	if err := s.Tokens.Create(ctx, revoked); err != nil {
		t.Fatalf("seed revoked: %v", err)
	}

	if err := s.Tokens.CreateWithLimit(ctx, newLifecycleToken(e.ID), 1, time.Now()); err != nil {
		t.Fatalf("create with limit: %v", err)
	}
	if err := s.Tokens.CreateWithLimit(ctx, newLifecycleToken(e.ID), 1, time.Now()); !errors.Is(err, store.ErrTokenLimitReached) {
		t.Fatalf("second create err=%v, want limit reached", err)
	}
}

func TestRotateRevokesOldAndRejectsReuse(t *testing.T) {
	s := newTestStore(t)
	e := seedEmployee(t, s)
	old := newLifecycleToken(e.ID)
	if err := s.Tokens.Create(ctx, old); err != nil {
		t.Fatalf("seed: %v", err)
	}

	fresh := newLifecycleToken(e.ID)
	if err := s.Tokens.Rotate(ctx, e.ID, old.ID, fresh, time.Now()); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	gotOld, err := s.Tokens.Get(ctx, old.ID)
	if err != nil {
		t.Fatalf("get old: %v", err)
	}
	if gotOld.Status != 0 {
		t.Fatalf("old status=%d, want revoked", gotOld.Status)
	}
	gotFresh, err := s.Tokens.Get(ctx, fresh.ID)
	if err != nil {
		t.Fatalf("get fresh: %v", err)
	}
	if gotFresh.Status != 1 {
		t.Fatalf("fresh status=%d, want active", gotFresh.Status)
	}

	if err := s.Tokens.Rotate(ctx, e.ID, old.ID, newLifecycleToken(e.ID), time.Now()); !errors.Is(err, store.ErrTokenRevoked) {
		t.Fatalf("second rotate err=%v, want revoked", err)
	}
	if active, err := s.Tokens.CountActiveByEmployee(ctx, e.ID, time.Now()); err != nil || active != 1 {
		t.Fatalf("active=%d err=%v, want 1", active, err)
	}
}

func TestRotateConcurrentOnlyOneSucceeds(t *testing.T) {
	s := newTestStore(t)
	e := seedEmployee(t, s)
	old := newLifecycleToken(e.ID)
	if err := s.Tokens.Create(ctx, old); err != nil {
		t.Fatalf("seed: %v", err)
	}

	const goroutines = 8
	var wg sync.WaitGroup
	results := make(chan error, goroutines)
	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- s.Tokens.Rotate(ctx, e.ID, old.ID, newLifecycleToken(e.ID), time.Now())
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	rotated := 0
	for err := range results {
		switch {
		case err == nil:
			rotated++
		case errors.Is(err, store.ErrTokenRevoked), errors.Is(err, store.ErrNotFound):
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if rotated != 1 {
		t.Fatalf("rotated=%d, want exactly 1", rotated)
	}
	if active, err := s.Tokens.CountActiveByEmployee(ctx, e.ID, time.Now()); err != nil || active != 1 {
		t.Fatalf("active=%d err=%v, want 1", active, err)
	}
}

func TestRotateRejectsExpiredKey(t *testing.T) {
	s := newTestStore(t)
	e := seedEmployee(t, s)
	old := newLifecycleToken(e.ID)
	old.ExpiresAt = ptr(time.Now().Add(-time.Minute))
	if err := s.Tokens.Create(ctx, old); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := s.Tokens.Rotate(ctx, e.ID, old.ID, newLifecycleToken(e.ID), time.Now()); !errors.Is(err, store.ErrTokenExpired) {
		t.Fatalf("rotate err=%v, want expired", err)
	}
	if active, err := s.Tokens.CountActiveByEmployee(ctx, e.ID, time.Now()); err != nil || active != 0 {
		t.Fatalf("active=%d err=%v, want 0", active, err)
	}
}

func TestRotateForeignEmployeeIsNotFound(t *testing.T) {
	s := newTestStore(t)
	owner := seedEmployee(t, s)
	other := seedEmployee(t, s)
	old := newLifecycleToken(owner.ID)
	if err := s.Tokens.Create(ctx, old); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := s.Tokens.Rotate(ctx, other.ID, old.ID, newLifecycleToken(other.ID), time.Now()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("rotate err=%v, want not found", err)
	}
}
