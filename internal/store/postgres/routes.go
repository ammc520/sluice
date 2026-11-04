package postgres

import (
	"context"

	"github.com/google/uuid"

	"github.com/company/sluice/internal/store"
)

const routeColumns = `id, provider_id, protocol, pattern, match_type, upstream_model,
	priority, enabled, created_at`

func scanRoute(row rowScanner) (*store.ModelRoute, error) {
	var r store.ModelRoute
	err := row.Scan(&r.ID, &r.ProviderID, &r.Protocol, &r.Pattern, &r.MatchType,
		&r.UpstreamModel, &r.Priority, &r.Enabled, &r.CreatedAt)
	if err != nil {
		return nil, mapError(err)
	}
	return &r, nil
}

func (s *RouteStore) Create(ctx context.Context, r *store.ModelRoute) error {
	if r.ID == uuid.Nil {
		r.ID = uuid.New()
	}
	if r.MatchType == "" {
		r.MatchType = "prefix"
	}
	if r.Priority == 0 {
		r.Priority = 100
	}
	return mapError(s.pool.QueryRow(ctx, `
		INSERT INTO model_routes (id, provider_id, protocol, pattern, match_type,
			upstream_model, priority, enabled)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		RETURNING created_at`,
		r.ID, r.ProviderID, r.Protocol, r.Pattern, r.MatchType, r.UpstreamModel, r.Priority, r.Enabled,
	).Scan(&r.CreatedAt))
}

func (s *RouteStore) List(ctx context.Context) ([]store.ModelRoute, error) {
	return s.queryRoutes(ctx, false)
}

func (s *RouteStore) ListEnabled(ctx context.Context) ([]store.ModelRoute, error) {
	return s.queryRoutes(ctx, true)
}

func (s *RouteStore) queryRoutes(ctx context.Context, enabledOnly bool) ([]store.ModelRoute, error) {
	q := `SELECT ` + routeColumns + ` FROM model_routes`
	if enabledOnly {
		q += ` WHERE enabled=true`
	}
	q += ` ORDER BY priority ASC, created_at ASC`
	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []store.ModelRoute{}
	for rows.Next() {
		r, err := scanRoute(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

func (s *RouteStore) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM model_routes WHERE id=$1`, id)
	if err != nil {
		return mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}
