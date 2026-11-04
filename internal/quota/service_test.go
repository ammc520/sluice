package quota

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/company/sluice/internal/store"
)

type fakeQuotaStore struct {
	reserveCalls int
	lastReserve  int64
	settled      int64
}

func (f *fakeQuotaStore) Get(context.Context, uuid.UUID) (*store.EmployeeQuota, error) {
	return nil, store.ErrNotFound
}
func (f *fakeQuotaStore) Ensure(context.Context, uuid.UUID, store.EmployeeQuotaDefaults) (*store.EmployeeQuota, error) {
	return nil, store.ErrNotFound
}
func (f *fakeQuotaStore) SetTotal(context.Context, uuid.UUID, string, string, int64, *uuid.UUID, time.Time) (*store.EmployeeQuota, error) {
	return nil, nil
}
func (f *fakeQuotaStore) Reserve(_ context.Context, employeeID, tokenID uuid.UUID, maxTokens, defaultReserve int64, now time.Time) (store.Reservation, error) {
	f.reserveCalls++
	reserve := maxTokens
	if reserve <= 0 {
		reserve = defaultReserve
	}
	f.lastReserve = reserve
	return store.Reservation{
		EmployeeID:  employeeID,
		TokenID:     tokenID,
		PeriodStart: now,
		Reserved:    reserve,
	}, nil
}
func (f *fakeQuotaStore) Settle(_ context.Context, _ store.Reservation, actual int64) error {
	f.settled = actual
	return nil
}

func ptr(v int64) *int64 { return &v }

func TestQuotaMetadataDoesNotConsume(t *testing.T) {
	fake := &fakeQuotaStore{}
	svc := New(fake, 4096, time.Now)
	r, err := svc.Reserve(context.Background(), uuid.New(), &store.Token{QuotaType: "tokens", QuotaTotal: ptr(1)}, EndpointMetadata, 0)
	if err != nil || r.Reserved != 0 || fake.reserveCalls != 0 {
		t.Fatalf("r=%+v calls=%d", r, fake.reserveCalls)
	}
}

func TestQuotaGenerationUsesDefaultReserve(t *testing.T) {
	fake := &fakeQuotaStore{}
	svc := New(fake, 4096, time.Now)
	if _, err := svc.Reserve(context.Background(), uuid.New(), &store.Token{QuotaType: "tokens"}, EndpointGeneration, 0); err != nil {
		t.Fatal(err)
	}
	if fake.lastReserve != 4096 {
		t.Fatalf("got %d", fake.lastReserve)
	}
}

func TestQuotaGenerationUsesMaxTokens(t *testing.T) {
	fake := &fakeQuotaStore{}
	svc := New(fake, 4096, time.Now)
	_, _ = svc.Reserve(context.Background(), uuid.New(), &store.Token{QuotaType: "tokens"}, EndpointGeneration, 256)
	if fake.lastReserve != 256 {
		t.Fatalf("got %d", fake.lastReserve)
	}
}

func TestQuotaEmbeddingHoldsDefaultReserve(t *testing.T) {
	fake := &fakeQuotaStore{}
	svc := New(fake, 4096, time.Now)
	if _, err := svc.Reserve(context.Background(), uuid.New(), &store.Token{QuotaType: "tokens"}, EndpointEmbedding, 0); err != nil {
		t.Fatal(err)
	}
	if fake.lastReserve != 4096 || fake.reserveCalls != 1 {
		t.Fatalf("reserve=%d calls=%d", fake.lastReserve, fake.reserveCalls)
	}
}

func TestQuotaGenerationClampsHugeMaxTokens(t *testing.T) {
	fake := &fakeQuotaStore{}
	svc := New(fake, 4096, time.Now)
	if _, err := svc.Reserve(context.Background(), uuid.New(), &store.Token{QuotaType: "tokens"}, EndpointGeneration, 1<<62); err != nil {
		t.Fatal(err)
	}
	if fake.lastReserve != MaxReservePerRequest {
		t.Fatalf("got %d", fake.lastReserve)
	}
}

func TestQuotaSettleSkipsEmptyReservation(t *testing.T) {
	fake := &fakeQuotaStore{}
	svc := New(fake, 4096, time.Now)
	if err := svc.Settle(context.Background(), store.Reservation{}, 10); err != nil {
		t.Fatal(err)
	}
	if fake.settled != 0 {
		t.Fatalf("settled %d", fake.settled)
	}
}
