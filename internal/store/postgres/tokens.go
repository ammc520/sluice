package postgres

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/company/sluice/internal/store"
)

const tokenColumns = `id, employee_id, name, token_hash, token_prefix, token_last4, quota_type,
	quota_period, quota_total, quota_used, period_start, rate_limit_rpm, ip_allowlist,
	status, expires_at, created_at, last_used_at`

func itoa(n int) string { return strconv.Itoa(n) }

func scanToken(row rowScanner) (*store.Token, error) {
	var t store.Token
	var allowlist []byte
	err := row.Scan(&t.ID, &t.EmployeeID, &t.Name, &t.TokenHash, &t.TokenPrefix, &t.TokenLast4,
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

// tokenInserter is satisfied by both pgxpool.Pool and pgx.Tx.
type tokenInserter interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func insertToken(ctx context.Context, db tokenInserter, t *store.Token) error {
	if t.ID == uuid.Nil {
		t.ID = uuid.New()
	}
	if t.IPAllowlist == nil {
		t.IPAllowlist = []string{}
	}
	allowlist, _ := json.Marshal(t.IPAllowlist)
	return mapError(db.QueryRow(ctx, `
		INSERT INTO tokens (id, employee_id, name, token_hash, token_prefix, token_last4,
			quota_type, quota_period, quota_total, quota_used, period_start, rate_limit_rpm,
			ip_allowlist, status, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
		RETURNING created_at`,
		t.ID, t.EmployeeID, t.Name, t.TokenHash, t.TokenPrefix, t.TokenLast4, t.QuotaType,
		t.QuotaPeriod, t.QuotaTotal, t.QuotaUsed, t.PeriodStart, t.RateLimitRPM, allowlist,
		t.Status, t.ExpiresAt,
	).Scan(&t.CreatedAt))
}

func (s *TokenStore) Create(ctx context.Context, t *store.Token) error {
	return insertToken(ctx, s.pool, t)
}

// CreateWithLimit serializes creation per employee by locking the employee
// row, then counts active keys and inserts in the same transaction. A plain
// count-then-insert in the handler would let concurrent requests exceed the
// limit.
func (s *TokenStore) CreateWithLimit(ctx context.Context, t *store.Token, maxActive int, now time.Time) error {
	if maxActive <= 0 {
		return store.ErrTokenLimitReached
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var employeeExists int
	if err := tx.QueryRow(ctx, `SELECT 1 FROM employees WHERE id=$1 FOR UPDATE`, t.EmployeeID).
		Scan(&employeeExists); err != nil {
		return mapError(err)
	}
	var active int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM tokens
		WHERE employee_id=$1 AND status=1 AND (expires_at IS NULL OR expires_at > $2)`,
		t.EmployeeID, now).Scan(&active); err != nil {
		return err
	}
	if active >= maxActive {
		return store.ErrTokenLimitReached
	}
	if err := insertToken(ctx, tx, t); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Rotate locks the old key, verifies that it is still active and unexpired,
// inserts the replacement, and revokes the old key in one transaction. The
// row lock makes concurrent rotations of the same key serialize; the second
// request sees status=0 and fails instead of minting an extra live key.
func (s *TokenStore) Rotate(ctx context.Context, employeeID, oldID uuid.UUID, fresh *store.Token, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var status int16
	var expiresAt *time.Time
	if err := tx.QueryRow(ctx,
		`SELECT status, expires_at FROM tokens WHERE id=$1 AND employee_id=$2 FOR UPDATE`,
		oldID, employeeID).Scan(&status, &expiresAt); err != nil {
		if isNoRows(err) {
			return store.ErrNotFound
		}
		return mapError(err)
	}
	if status != 1 {
		return store.ErrTokenRevoked
	}
	if expiresAt != nil && !expiresAt.After(now) {
		return store.ErrTokenExpired
	}
	if err := insertToken(ctx, tx, fresh); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE tokens SET status=0 WHERE id=$1`, oldID); err != nil {
		return err
	}
	return tx.Commit(ctx)
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
		UPDATE tokens SET name=$2, quota_type=$3, quota_period=$4, quota_total=$5,
			quota_used=$6, period_start=$7, rate_limit_rpm=$8, ip_allowlist=$9,
			status=$10, expires_at=$11
		WHERE id=$1`,
		t.ID, t.Name, t.QuotaType, t.QuotaPeriod, t.QuotaTotal, t.QuotaUsed, t.PeriodStart,
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
	if reserve < 0 {
		return store.Reservation{}, store.ErrQuotaExceeded
	}
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
		// Use subtraction instead of used+reserve so a huge reserve cannot
		// overflow int64 and slip past the quota check.
		if used >= *total || reserve > *total-used {
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
	if actual < 0 {
		actual = 0
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var used int64
	var total *int64
	var start *time.Time
	if err := tx.QueryRow(ctx, `
		SELECT quota_used, quota_total, period_start FROM tokens WHERE id=$1 FOR UPDATE`, r.TokenID).
		Scan(&used, &total, &start); err != nil {
		return mapError(err)
	}
	if start != nil && !start.Equal(r.PeriodStart) {
		return tx.Commit(ctx)
	}

	newUsed := used - r.Reserved + actual
	if newUsed < 0 {
		newUsed = 0
	}
	// Actual upstream usage can exceed the hold. Cap the counter at the
	// quota so later reservations cannot use an over-counted balance.
	if total != nil && newUsed > *total {
		newUsed = *total
	}
	if _, err := tx.Exec(ctx, `UPDATE tokens SET quota_used=$2 WHERE id=$1`, r.TokenID, newUsed); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *TokenStore) CountActiveByEmployee(ctx context.Context, employeeID uuid.UUID, now time.Time) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM tokens
		WHERE employee_id=$1 AND status=1 AND (expires_at IS NULL OR expires_at > $2)`,
		employeeID, now).Scan(&count)
	return count, err
}

func (s *TokenStore) Touch(ctx context.Context, id uuid.UUID, at time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE tokens SET last_used_at=$2 WHERE id=$1`, id, at)
	return err
}
