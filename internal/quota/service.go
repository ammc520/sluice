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
		reserve = 0
	default:
		if t.QuotaType == "requests" {
			reserve = 1
		} else if maxTokens > 0 {
			reserve = maxTokens
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
