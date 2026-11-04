package postgres

import (
	"context"
	"encoding/json"

	"github.com/company/sluice/internal/store"
)

func (s *SettingsStore) Get(ctx context.Context, key string) (json.RawMessage, error) {
	var raw []byte
	if err := s.pool.QueryRow(ctx, `SELECT value FROM settings WHERE key=$1`, key).Scan(&raw); err != nil {
		return nil, mapError(err)
	}
	return json.RawMessage(raw), nil
}

func (s *SettingsStore) Set(ctx context.Context, key string, value json.RawMessage) error {
	if len(value) == 0 {
		value = json.RawMessage("{}")
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO settings (key, value) VALUES ($1,$2::jsonb)
		ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=now()`, key, []byte(value))
	return mapError(err)
}

var _ store.SettingsStore = (*SettingsStore)(nil)
