package postgres

import (
	"context"

	"github.com/google/uuid"

	"github.com/company/api-key/internal/store"
)

const providerColumns = `id, name, protocol, base_url, api_key_enc, is_default, enabled,
	timeout_seconds, created_at, updated_at`

func scanProvider(row rowScanner) (*store.Provider, error) {
	var p store.Provider
	err := row.Scan(&p.ID, &p.Name, &p.Protocol, &p.BaseURL, &p.APIKeyEnc, &p.IsDefault,
		&p.Enabled, &p.TimeoutSeconds, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, mapError(err)
	}
	return &p, nil
}

func (s *ProviderStore) Create(ctx context.Context, p *store.Provider) error {
	if p.ID == uuid.Nil {
		p.ID = uuid.New()
	}
	return mapError(s.pool.QueryRow(ctx, `
		INSERT INTO upstream_providers (id, name, protocol, base_url, api_key_enc,
			is_default, enabled, timeout_seconds)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		RETURNING created_at, updated_at`,
		p.ID, p.Name, p.Protocol, p.BaseURL, p.APIKeyEnc, p.IsDefault, p.Enabled, p.TimeoutSeconds,
	).Scan(&p.CreatedAt, &p.UpdatedAt))
}

func (s *ProviderStore) Get(ctx context.Context, id uuid.UUID) (*store.Provider, error) {
	return scanProvider(s.pool.QueryRow(ctx, `SELECT `+providerColumns+` FROM upstream_providers WHERE id=$1`, id))
}

func (s *ProviderStore) List(ctx context.Context) ([]store.Provider, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+providerColumns+` FROM upstream_providers ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []store.Provider{}
	for rows.Next() {
		p, err := scanProvider(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

func (s *ProviderStore) Update(ctx context.Context, p *store.Provider) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE upstream_providers SET name=$2, protocol=$3, base_url=$4, api_key_enc=$5,
			is_default=$6, enabled=$7, timeout_seconds=$8, updated_at=now()
		WHERE id=$1`,
		p.ID, p.Name, p.Protocol, p.BaseURL, p.APIKeyEnc, p.IsDefault, p.Enabled, p.TimeoutSeconds)
	if err != nil {
		return mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *ProviderStore) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM upstream_providers WHERE id=$1`, id)
	if err != nil {
		return mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}
