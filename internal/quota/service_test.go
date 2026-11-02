package quota

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/company/api-key/internal/store"
)

type fakeTokenStore struct {
	reserveCalls int
	lastReserve  int64
	settled      int64
}

func (f *fakeTokenStore) Create(context.Context, *store.Token) error { return nil }
func (f *fakeTokenStore) Get(context.Context, uuid.UUID) (*store.Token, error) {
	return nil, store.ErrNotFound
}
func (f *fakeTokenStore) FindByHash(context.Context, string) (*store.Token, error) {
	return nil, store.ErrNotFound
}
func (f *fakeTokenStore) Update(context.Context, *store.Token) error { return nil }
func (f *fakeTokenStore) List(context.Context, store.TokenFilter) ([]store.Token, int, error) {
	return nil, 0, nil
}
func (f *fakeTokenStore) ReserveQuota(_ context.Context, id uuid.UUID, reserve int64, now time.Time) (store.Reservation, error) {
	f.reserveCalls++
	f.lastReserve = reserve
	return store.Reservation{TokenID: id, PeriodStart: now, Reserved: reserve}, nil
}
func (f *fakeTokenStore) SettleQuota(_ context.Context, _ store.Reservation, actual int64) error {
	f.settled = actual
	return nil
}
func (f *fakeTokenStore) Touch(context.Context, uuid.UUID, time.Time) error { return nil }

func ptr(v int64) *int64 { return &v }

func TestQuotaMetadataDoesNotConsume(t *testing.T) {
	fake := &fakeTokenStore{}
	svc := New(fake, 4096, time.Now)
	r, err := svc.Reserve(context.Background(), &store.Token{QuotaType: "tokens", QuotaTotal: ptr(1)}, EndpointMetadata, 0)
	if err != nil || r.Reserved != 0 || fake.reserveCalls != 0 {
		t.Fatalf("r=%+v calls=%d", r, fake.reserveCalls)
	}
}

func TestQuotaGenerationUsesDefaultReserve(t *testing.T) {
	fake := &fakeTokenStore{}
	svc := New(fake, 4096, time.Now)
	if _, err := svc.Reserve(context.Background(), &store.Token{QuotaType: "tokens"}, EndpointGeneration, 0); err != nil {
		t.Fatal(err)
	}
	if fake.lastReserve != 4096 {
		t.Fatalf("got %d", fake.lastReserve)
	}
}

func TestQuotaGenerationUsesMaxTokens(t *testing.T) {
	fake := &fakeTokenStore{}
	svc := New(fake, 4096, time.Now)
	svc.Reserve(context.Background(), &store.Token{QuotaType: "tokens"}, EndpointGeneration, 256)
	if fake.lastReserve != 256 {
		t.Fatalf("got %d", fake.lastReserve)
	}
}

func TestQuotaRequestCountReservesOne(t *testing.T) {
	fake := &fakeTokenStore{}
	svc := New(fake, 4096, time.Now)
	svc.Reserve(context.Background(), &store.Token{QuotaType: "requests"}, EndpointGeneration, 8192)
	if fake.lastReserve != 1 {
		t.Fatalf("got %d", fake.lastReserve)
	}
}

func TestQuotaEmbeddingChecksWithoutReserving(t *testing.T) {
	fake := &fakeTokenStore{}
	svc := New(fake, 4096, time.Now)
	if _, err := svc.Reserve(context.Background(), &store.Token{QuotaType: "tokens"}, EndpointEmbedding, 0); err != nil {
		t.Fatal(err)
	}
	if fake.lastReserve != 0 || fake.reserveCalls != 1 {
		t.Fatalf("reserve=%d calls=%d", fake.lastReserve, fake.reserveCalls)
	}
}

func TestQuotaSettleSkipsEmptyReservation(t *testing.T) {
	fake := &fakeTokenStore{}
	svc := New(fake, 4096, time.Now)
	if err := svc.Settle(context.Background(), store.Reservation{}, 10); err != nil {
		t.Fatal(err)
	}
	if fake.settled != 0 {
		t.Fatalf("settled %d", fake.settled)
	}
}
