package postgres

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/company/api-key/internal/store"
)

const tokenColumns = `id, employee_id, token_hash, token_prefix, token_last4, quota_type, quota_period,
	quota_total, quota_used, period_start, rate_limit_rpm, ip_allowlist, status, expires_at, created_at, last_used_at`

func itoa(n int) string { return strconv.Itoa(n) }

func scanToken(row rowScanner) (*store.Token, error) {
	var t store.Token
	var allowlist []byte
	err := row.Scan(&t.ID, &t.EmployeeID, &t.TokenHash, &t.TokenPrefix, &t.TokenLast4,
		&t.QuotaType, &t.QuotaPeriod, &t.QuotaTotal, &t.QuotaUsed, &t.PeriodStart,
		&t.RateLimitRPM, &allowlist, &t.Status, &t.ExpiresAt, &t.CreatedAt, &t.LastUsedAt)
	if err != nil {
		return nil, mapError(err)
	}
	t.IPAllowlist = []string{}
	if len(allowlist) > 0 {
		_ = json.Unmarshal(allowlist, &t.IPAllowlist)
	}
	return &t, nil
}

func (s *TokenStore) Create(ctx context.Context, t *store.Token) error {
	if t.ID == uuid.Nil {
		t.ID = uuid.New()
	}
	if t.IPAllowlist == nil {
		t.IPAllowlist = []string{}
	}
	allowlist, _ := json.Marshal(t.IPAllowlist)
	return mapError(s.pool.QueryRow(ctx, `
		INSERT INTO tokens (id, employee_id, token_hash, token_prefix, token_last4, quota_type,
			quota_period, quota_total, quota_used, period_start, rate_limit_rpm, ip_allowlist,
			status, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		RETURNING created_at`,
		t.ID, t.EmployeeID, t.TokenHash, t.TokenPrefix, t.TokenLast4, t.QuotaType,
		t.QuotaPeriod, t.QuotaTotal, t.QuotaUsed, t.PeriodStart, t.RateLimitRPM, allowlist,
		t.Status, t.ExpiresAt,
	).Scan(&t.CreatedAt))
}

func (s *TokenStore) Get(ctx context.Context, id uuid.UUID) (*store.Token, error) {
	return scanToken(s.pool.QueryRow(ctx, `SELECT `+tokenColumns+` FROM tokens WHERE id=$1`, id))
}

func (s *TokenStore) FindByHash(ctx context.Context, hash string) (*store.Token, error) {
	return scanToken(s.pool.QueryRow(ctx, `SELECT `+tokenColumns+` FROM tokens WHERE token_hash=$1`, hash))
}

func (s *TokenStore) Update(ctx context.Context, t *store.Token) error {
	allowlist, _ := json.Marshal(t.IPAllowlist)
	tag, err := s.pool.Exec(ctx, `
		UPDATE tokens SET quota_type=$2, quota_period=$3, quota_total=$4, quota_used=$5,
			period_start=$6, rate_limit_rpm=$7, ip_allowlist=$8, status=$9, expires_at=$10
		WHERE id=$1`,
		t.ID, t.QuotaType, t.QuotaPeriod, t.QuotaTotal, t.QuotaUsed, t.PeriodStart,
		t.RateLimitRPM, allowlist, t.Status, t.ExpiresAt)
	if err != nil {
		return mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *TokenStore) List(ctx context.Context, f store.TokenFilter) ([]store.Token, int, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}

	where := "WHERE 1=1"
	args := []any{}
	if f.EmployeeID != nil {
		args = append(args, *f.EmployeeID)
		where += " AND employee_id=$" + itoa(len(args))
	}
	if f.Status != nil {
		args = append(args, *f.Status)
		where += " AND status=$" + itoa(len(args))
	}
	var total int
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM tokens "+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	args = append(args, limit, offset)
	rows, err := s.pool.Query(ctx, `SELECT `+tokenColumns+` FROM tokens `+where+
		" ORDER BY created_at DESC LIMIT $"+itoa(len(args)-1)+" OFFSET $"+itoa(len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	out := []store.Token{}
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *t)
	}
	return out, total, rows.Err()
}

var shanghai = func() *time.Location {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return time.FixedZone("CST", 8*3600)
	}
	return loc
}()

func periodStart(period string, now time.Time) time.Time {
	local := now.In(shanghai)
	switch period {
	case "daily":
		return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, shanghai)
	case "monthly":
		return time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, shanghai)
	default:
		return now
	}
}

func (s *TokenStore) ReserveQuota(ctx context.Context, id uuid.UUID, reserve int64, now time.Time) (store.Reservation, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return store.Reservation{}, err
	}
	defer tx.Rollback(ctx)

	var total *int64
	var used int64
	var period string
	var start *time.Time
	if err := tx.QueryRow(ctx, `
		SELECT quota_total, quota_used, quota_period, period_start
		FROM tokens WHERE id=$1 FOR UPDATE`, id).Scan(&total, &used, &period, &start); err != nil {
		return store.Reservation{}, mapError(err)
	}

	startAt := periodStart(period, now)
	if period == "daily" || period == "monthly" {
		if start == nil || start.Before(startAt) {
			used = 0
			start = &startAt
		}
	} else if start == nil {
		start = &startAt
	}

	if total != nil {
		if reserve == 0 && used >= *total {
			return store.Reservation{}, store.ErrQuotaExceeded
		}
		if reserve > 0 && used+reserve > *total {
			return store.Reservation{}, store.ErrQuotaExceeded
		}
	}

	used += reserve
	if _, err := tx.Exec(ctx, `UPDATE tokens SET quota_used=$2, period_start=$3 WHERE id=$1`, id, used, start); err != nil {
		return store.Reservation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return store.Reservation{}, err
	}
	return store.Reservation{TokenID: id, PeriodStart: *start, Reserved: reserve}, nil
}

func (s *TokenStore) SettleQuota(ctx context.Context, r store.Reservation, actual int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var used int64
	var start *time.Time
	if err := tx.QueryRow(ctx, `
		SELECT quota_used, period_start FROM tokens WHERE id=$1 FOR UPDATE`, r.TokenID).
		Scan(&used, &start); err != nil {
		return mapError(err)
	}
	if start != nil && !start.Equal(r.PeriodStart) {
		return tx.Commit(ctx)
	}

	newUsed := used - r.Reserved + actual
	if newUsed < 0 {
		newUsed = 0
	}
	if _, err := tx.Exec(ctx, `UPDATE tokens SET quota_used=$2 WHERE id=$1`, r.TokenID, newUsed); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *TokenStore) Touch(ctx context.Context, id uuid.UUID, at time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE tokens SET last_used_at=$2 WHERE id=$1`, id, at)
	return err
}
