package postgres

import (
	"context"

	"github.com/google/uuid"

	"github.com/company/api-key/internal/store"
)

const dlpRuleColumns = `id, name, description, category, match_type, pattern, action,
	replacement, priority, enabled, builtin, alert_enabled, created_at, updated_at`

func scanDLPRule(row rowScanner) (*store.DLPRule, error) {
	var r store.DLPRule
	err := row.Scan(&r.ID, &r.Name, &r.Description, &r.Category, &r.MatchType, &r.Pattern,
		&r.Action, &r.Replacement, &r.Priority, &r.Enabled, &r.Builtin, &r.AlertEnabled,
		&r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return nil, mapError(err)
	}
	return &r, nil
}

func (s *DLPRuleStore) Create(ctx context.Context, r *store.DLPRule) error {
	if r.ID == uuid.Nil {
		r.ID = uuid.New()
	}
	if r.Replacement == "" {
		r.Replacement = "***"
	}
	if r.Priority == 0 {
		r.Priority = 100
	}
	return mapError(s.pool.QueryRow(ctx, `
		INSERT INTO dlp_rules (id, name, description, category, match_type, pattern, action,
			replacement, priority, enabled, builtin, alert_enabled)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		RETURNING created_at, updated_at`,
		r.ID, r.Name, r.Description, r.Category, r.MatchType, r.Pattern, r.Action,
		r.Replacement, r.Priority, r.Enabled, r.Builtin, r.AlertEnabled,
	).Scan(&r.CreatedAt, &r.UpdatedAt))
}

func (s *DLPRuleStore) Get(ctx context.Context, id uuid.UUID) (*store.DLPRule, error) {
	return scanDLPRule(s.pool.QueryRow(ctx, `SELECT `+dlpRuleColumns+` FROM dlp_rules WHERE id=$1`, id))
}

func (s *DLPRuleStore) List(ctx context.Context) ([]store.DLPRule, error) {
	return s.queryRules(ctx, false)
}

func (s *DLPRuleStore) ListEnabled(ctx context.Context) ([]store.DLPRule, error) {
	return s.queryRules(ctx, true)
}

func (s *DLPRuleStore) queryRules(ctx context.Context, enabledOnly bool) ([]store.DLPRule, error) {
	q := `SELECT ` + dlpRuleColumns + ` FROM dlp_rules`
	if enabledOnly {
		q += ` WHERE enabled=true`
	}
	q += ` ORDER BY priority ASC, created_at ASC`
	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []store.DLPRule{}
	for rows.Next() {
		r, err := scanDLPRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

func (s *DLPRuleStore) Update(ctx context.Context, r *store.DLPRule) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE dlp_rules SET name=$2, description=$3, category=$4, match_type=$5,
			pattern=$6, action=$7, replacement=$8, priority=$9, enabled=$10,
			alert_enabled=$11, updated_at=now()
		WHERE id=$1`,
		r.ID, r.Name, r.Description, r.Category, r.MatchType, r.Pattern, r.Action,
		r.Replacement, r.Priority, r.Enabled, r.AlertEnabled)
	if err != nil {
		return mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *DLPRuleStore) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM dlp_rules WHERE id=$1 AND builtin=false`, id)
	if err != nil {
		return mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}
