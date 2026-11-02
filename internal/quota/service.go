package quota

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/company/api-key/internal/store"
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

// Service applies quota rules on top of the token store.
type Service struct {
	tokens         store.TokenStore
	defaultReserve int64
	clock          func() time.Time
}

// New creates a quota service. defaultReserve is used when a generation
// request does not declare max_tokens.
func New(tokens store.TokenStore, defaultReserve int64, clock func() time.Time) *Service {
	if defaultReserve <= 0 {
		defaultReserve = 4096
	}
	if defaultReserve > MaxReservePerRequest {
		defaultReserve = MaxReservePerRequest
	}
	if clock == nil {
		clock = time.Now
	}
	return &Service{tokens: tokens, defaultReserve: defaultReserve, clock: clock}
}

// Reserve holds quota before the upstream call.
func (s *Service) Reserve(ctx context.Context, t *store.Token, endpoint Endpoint, maxTokens int64) (store.Reservation, error) {
	if endpoint == EndpointMetadata {
		return store.Reservation{}, nil
	}

	var reserve int64
	switch endpoint {
	case EndpointEmbedding:
		// Embeddings report input tokens, which can arrive in parallel.
		// Holding zero would let every concurrent call pass the remaining
		// check at once (TOCTOU), so use the default hold and settle it.
		reserve = s.defaultReserve
	default:
		if t.QuotaType == "requests" {
			reserve = 1
		} else if maxTokens > 0 {
			reserve = maxTokens
			if reserve > MaxReservePerRequest {
				reserve = MaxReservePerRequest
			}
		} else {
			reserve = s.defaultReserve
		}
	}
	return s.tokens.ReserveQuota(ctx, t.ID, reserve, s.clock())
}

// Settle adjusts the hold using the actual usage reported by the upstream.
func (s *Service) Settle(ctx context.Context, r store.Reservation, actual int64) error {
	if r.TokenID == uuid.Nil {
		return nil
	}
	return s.tokens.SettleQuota(ctx, r, actual)
}
