package quota

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/company/sluice/internal/store"
)

// Endpoint classifies how a request consumes quota.
type Endpoint string

const (
	EndpointGeneration Endpoint = "generation"
	EndpointEmbedding  Endpoint = "embedding"
	EndpointMetadata   Endpoint = "metadata"
)

// MaxReservePerRequest caps the per-request token hold. A client-supplied
// max_tokens above this value is clamped so it cannot overflow the quota
// arithmetic or hold an absurd amount of quota.
const MaxReservePerRequest int64 = 1_000_000

// Service applies quota rules on top of the employee quota store.
type Service struct {
	quotas         store.EmployeeQuotaStore
	defaultReserve int64
	clock          func() time.Time
}

// New creates a quota service. defaultReserve is used when a generation
// request does not declare max_tokens.
func New(quotas store.EmployeeQuotaStore, defaultReserve int64, clock func() time.Time) *Service {
	if defaultReserve <= 0 {
		defaultReserve = 4096
	}
	if defaultReserve > MaxReservePerRequest {
		defaultReserve = MaxReservePerRequest
	}
	if clock == nil {
		clock = time.Now
	}
	return &Service{quotas: quotas, defaultReserve: defaultReserve, clock: clock}
}

// Reserve holds quota from the employee's shared pool before the upstream
// call. The key's optional cap is enforced in the same transaction.
func (s *Service) Reserve(ctx context.Context, employeeID uuid.UUID, t *store.Token, endpoint Endpoint, maxTokens int64) (store.Reservation, error) {
	if endpoint == EndpointMetadata {
		return store.Reservation{}, nil
	}

	reserve := maxTokens
	if endpoint == EndpointEmbedding {
		// Embeddings report input tokens, which can arrive in parallel.
		// Holding zero would let every concurrent call pass the remaining
		// check at once (TOCTOU), so use the default hold and settle it.
		reserve = s.defaultReserve
	}
	if reserve > MaxReservePerRequest {
		reserve = MaxReservePerRequest
	}
	return s.quotas.Reserve(ctx, employeeID, t.ID, reserve, s.defaultReserve, s.clock())
}

// Settle adjusts the hold using the actual usage reported by the upstream.
func (s *Service) Settle(ctx context.Context, r store.Reservation, actual int64) error {
	if r.EmployeeID == uuid.Nil || r.TokenID == uuid.Nil {
		return nil
	}
	return s.quotas.Settle(ctx, r, actual)
}
