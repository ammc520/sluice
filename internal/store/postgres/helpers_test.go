package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/company/sluice/internal/store"
	"github.com/company/sluice/internal/testutil"
)

var ctx = context.Background()

func newTestStore(t *testing.T) *Store {
	t.Helper()
	return newStore(testutil.OpenTestDB(t))
}

func ptr[T any](v T) *T { return &v }

func seedEmployee(t *testing.T, s *Store) *store.Employee {
	t.Helper()
	e := &store.Employee{
		ID:         uuid.New(),
		EmployeeNo: "E" + uuid.NewString()[:8],
		Name:       "Alice",
		Email:      "alice@example.com",
		Status:     1,
	}
	if err := s.Employees.Create(ctx, e); err != nil {
		t.Fatalf("seed employee: %v", err)
	}
	return e
}

func seedToken(t *testing.T, total, used int64) (*Store, *store.Token) {
	t.Helper()
	s := newTestStore(t)
	e := seedEmployee(t, s)
	sum := sha256.Sum256([]byte(uuid.NewString()))
	tok := &store.Token{
		ID:           uuid.New(),
		EmployeeID:   e.ID,
		TokenHash:    hex.EncodeToString(sum[:]),
		TokenPrefix:  "sk-company-abc",
		TokenLast4:   "wxyz",
		QuotaType:    "tokens",
		QuotaPeriod:  "monthly",
		QuotaTotal:   ptr(total),
		QuotaUsed:    used,
		RateLimitRPM: 60,
		IPAllowlist:  []string{},
		Status:       1,
	}
	ps := monthStart(time.Now())
	tok.PeriodStart = &ps
	if err := s.Tokens.Create(ctx, tok); err != nil {
		t.Fatalf("seed token: %v", err)
	}
	return s, tok
}

func seedProvider(t *testing.T, s *Store) *store.Provider {
	t.Helper()
	p := &store.Provider{
		ID:             uuid.New(),
		Name:           "p-" + uuid.NewString()[:8],
		Protocol:       "openai_compatible",
		BaseURL:        "https://example.com",
		APIKeyEnc:      []byte{1, 2, 3},
		Enabled:        true,
		TimeoutSeconds: 300,
	}
	if err := s.Providers.Create(ctx, p); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	return p
}

func mustCreateRoute(t *testing.T, s *Store, r *store.ModelRoute) {
	t.Helper()
	if r.ID == uuid.Nil {
		r.ID = uuid.New()
	}
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now()
	}
	if err := s.Routes.Create(ctx, r); err != nil {
		t.Fatalf("create route: %v", err)
	}
}
