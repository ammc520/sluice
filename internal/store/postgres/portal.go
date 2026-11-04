package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/company/api-key/internal/store"
)

const employeeAccountColumns = `employee_id, password_hash, must_change_password,
	failed_attempts, locked_until, last_login_at, created_at, updated_at`

func scanEmployeeAccount(row rowScanner) (*store.EmployeeAccount, error) {
	var a store.EmployeeAccount
	err := row.Scan(&a.EmployeeID, &a.PasswordHash, &a.MustChangePassword,
		&a.FailedAttempts, &a.LockedUntil, &a.LastLoginAt, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return nil, mapError(err)
	}
	return &a, nil
}

// FindAccountByEmployeeNo resolves the credential and its employee in one
// query. Callers must not reveal which half was missing.
func (s *EmployeeAccountStore) FindAccountByEmployeeNo(ctx context.Context, employeeNo string) (*store.EmployeeAccount, *store.Employee, error) {
	var (
		a store.EmployeeAccount
		e store.Employee
	)
	err := s.pool.QueryRow(ctx, `
		SELECT a.employee_id, a.password_hash, a.must_change_password,
			a.failed_attempts, a.locked_until, a.last_login_at, a.created_at,
			a.updated_at, e.employee_no, e.name, coalesce(e.email,''),
			e.department_id, e.status, e.created_at, e.updated_at
		FROM employee_accounts a
		JOIN employees e ON e.id = a.employee_id
		WHERE e.employee_no = $1`, employeeNo).
		Scan(&a.EmployeeID, &a.PasswordHash, &a.MustChangePassword,
			&a.FailedAttempts, &a.LockedUntil, &a.LastLoginAt, &a.CreatedAt,
			&a.UpdatedAt, &e.EmployeeNo, &e.Name, &e.Email, &e.DepartmentID,
			&e.Status, &e.CreatedAt, &e.UpdatedAt)
	if err != nil {
		return nil, nil, mapError(err)
	}
	e.ID = a.EmployeeID
	return &a, &e, nil
}

func (s *EmployeeAccountStore) GetAccount(ctx context.Context, employeeID uuid.UUID) (*store.EmployeeAccount, error) {
	return scanEmployeeAccount(s.pool.QueryRow(ctx,
		`SELECT `+employeeAccountColumns+` FROM employee_accounts WHERE employee_id=$1`, employeeID))
}

func (s *EmployeeAccountStore) CreateAccount(ctx context.Context, a *store.EmployeeAccount) error {
	return mapError(s.pool.QueryRow(ctx, `
		INSERT INTO employee_accounts (employee_id, password_hash, must_change_password,
			failed_attempts, locked_until, last_login_at)
		VALUES ($1,$2,$3,$4,$5,$6)
		RETURNING created_at, updated_at`,
		a.EmployeeID, a.PasswordHash, a.MustChangePassword, a.FailedAttempts,
		a.LockedUntil, a.LastLoginAt,
	).Scan(&a.CreatedAt, &a.UpdatedAt))
}

func (s *EmployeeAccountStore) ReplacePassword(ctx context.Context, employeeID uuid.UUID, passwordHash string, mustChange bool) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE employee_accounts SET password_hash=$2, must_change_password=$3,
			failed_attempts=0, locked_until=NULL, updated_at=now()
		WHERE employee_id=$1`, employeeID, passwordHash, mustChange)
	if err != nil {
		return mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

// ChangePassword is a compare-and-swap on password_hash. An administrator
// reset between the caller's read and write therefore wins instead of being
// silently overwritten by the employee's stale copy.
func (s *EmployeeAccountStore) ChangePassword(ctx context.Context, employeeID uuid.UUID, currentHash, newHash string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE employee_accounts SET password_hash=$3, must_change_password=false,
			failed_attempts=0, locked_until=NULL, updated_at=now()
		WHERE employee_id=$1 AND password_hash=$2`, employeeID, currentHash, newHash)
	if err != nil {
		return false, mapError(err)
	}
	return tag.RowsAffected() == 1, nil
}

// RecordLoginFailure is intentionally a single statement: PostgreSQL evaluates
// every SET expression against the old row, so both assignments agree on the
// post-increment count even when logins race.
func (s *EmployeeAccountStore) RecordLoginFailure(ctx context.Context, employeeID uuid.UUID, maxAttempts int, lockout time.Duration, now time.Time) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE employee_accounts SET
			failed_attempts = CASE WHEN failed_attempts + 1 >= $2 THEN 0 ELSE failed_attempts + 1 END,
			locked_until = CASE WHEN failed_attempts + 1 >= $2 THEN $3 ELSE locked_until END,
			updated_at = now()
		WHERE employee_id=$1`, employeeID, maxAttempts, now.Add(lockout))
	if err != nil {
		return mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *EmployeeAccountStore) RecordLoginSuccess(ctx context.Context, employeeID uuid.UUID, at time.Time) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE employee_accounts SET failed_attempts=0, locked_until=NULL,
			last_login_at=$2, updated_at=now()
		WHERE employee_id=$1`, employeeID, at)
	if err != nil {
		return mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

// DeleteAccount removes the credential and every live session together so a
// disabled account cannot keep an authenticated session around.
func (s *EmployeeAccountStore) DeleteAccount(ctx context.Context, employeeID uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `DELETE FROM employee_sessions WHERE employee_id=$1`, employeeID); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM employee_accounts WHERE employee_id=$1`, employeeID)
	if err != nil {
		return mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return tx.Commit(ctx)
}

const employeeSessionColumns = `id, employee_id, token_hash, csrf_hash,
	coalesce(host(ip),''), user_agent, expires_at, created_at, last_seen_at`

func scanEmployeeSession(row rowScanner) (*store.EmployeeSession, error) {
	var s store.EmployeeSession
	err := row.Scan(&s.ID, &s.EmployeeID, &s.TokenHash, &s.CSRFHash, &s.IP,
		&s.UserAgent, &s.ExpiresAt, &s.CreatedAt, &s.LastSeenAt)
	if err != nil {
		return nil, mapError(err)
	}
	return &s, nil
}

func (s *EmployeeAccountStore) CreateSession(ctx context.Context, sess *store.EmployeeSession) error {
	if sess.ID == uuid.Nil {
		sess.ID = uuid.New()
	}
	return mapError(s.pool.QueryRow(ctx, `
		INSERT INTO employee_sessions (id, employee_id, token_hash, csrf_hash, ip,
			user_agent, expires_at)
		VALUES ($1,$2,$3,$4,NULLIF($5,'')::inet,$6,$7)
		RETURNING created_at, last_seen_at`,
		sess.ID, sess.EmployeeID, sess.TokenHash, sess.CSRFHash, sess.IP,
		sess.UserAgent, sess.ExpiresAt,
	).Scan(&sess.CreatedAt, &sess.LastSeenAt))
}

func (s *EmployeeAccountStore) FindSessionByHash(ctx context.Context, hash string) (*store.EmployeeSession, error) {
	return scanEmployeeSession(s.pool.QueryRow(ctx,
		`SELECT `+employeeSessionColumns+` FROM employee_sessions WHERE token_hash=$1`, hash))
}

func (s *EmployeeAccountStore) TouchSession(ctx context.Context, id uuid.UUID, at time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE employee_sessions SET last_seen_at=$2 WHERE id=$1`, id, at)
	return err
}

func (s *EmployeeAccountStore) DeleteSession(ctx context.Context, hash string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM employee_sessions WHERE token_hash=$1`, hash)
	return err
}

func (s *EmployeeAccountStore) DeleteSessionsByEmployeeID(ctx context.Context, employeeID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM employee_sessions WHERE employee_id=$1`, employeeID)
	return err
}

func (s *EmployeeAccountStore) DeleteExpiredSessions(ctx context.Context, before time.Time) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM employee_sessions WHERE expires_at < $1`, before)
	return err
}

const tokenRequestColumns = `id, employee_id, kind, reason, requested_total, status,
	reviewed_by, review_note, reviewed_at, granted_quota_type, granted_quota_period,
	granted_total, granted_rpm, granted_expires_at, token_id, expires_at, created_at,
	updated_at`

func scanTokenRequest(row rowScanner) (*store.TokenRequest, error) {
	var r store.TokenRequest
	err := row.Scan(&r.ID, &r.EmployeeID, &r.Kind, &r.Reason, &r.RequestedTotal, &r.Status,
		&r.ReviewedBy, &r.ReviewNote, &r.ReviewedAt, &r.GrantedQuotaType,
		&r.GrantedQuotaPeriod, &r.GrantedTotal, &r.GrantedRPM, &r.GrantedExpiresAt,
		&r.TokenID, &r.ExpiresAt, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return nil, mapError(err)
	}
	return &r, nil
}

func (s *TokenRequestStore) Create(ctx context.Context, r *store.TokenRequest) error {
	if r.ID == uuid.Nil {
		r.ID = uuid.New()
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Free the partial unique index before inserting a replacement request.
	if _, err := tx.Exec(ctx, `
		UPDATE token_requests SET status='expired', updated_at=now()
		WHERE employee_id=$1 AND status IN ('pending','approved') AND expires_at <= now()`,
		r.EmployeeID); err != nil {
		return err
	}

	if r.Kind == "" {
		r.Kind = "quota"
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO token_requests (id, employee_id, kind, reason, requested_total,
			status, expires_at)
		VALUES ($1,$2,$3,$4,$5,'pending',$6)
		RETURNING created_at, updated_at`,
		r.ID, r.EmployeeID, r.Kind, r.Reason, r.RequestedTotal, r.ExpiresAt,
	).Scan(&r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return mapError(err)
	}
	r.Status = "pending"
	return tx.Commit(ctx)
}

func (s *TokenRequestStore) Get(ctx context.Context, id uuid.UUID) (*store.TokenRequest, error) {
	return scanTokenRequest(s.pool.QueryRow(ctx,
		`SELECT `+tokenRequestColumns+` FROM token_requests WHERE id=$1`, id))
}

func (s *TokenRequestStore) ListByEmployee(ctx context.Context, employeeID uuid.UUID, limit int) ([]store.TokenRequest, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+tokenRequestColumns+` FROM token_requests
		WHERE employee_id=$1 ORDER BY created_at DESC LIMIT $2`, employeeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []store.TokenRequest{}
	for rows.Next() {
		r, err := scanTokenRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

func (s *TokenRequestStore) List(ctx context.Context, f store.TokenRequestFilter) ([]store.TokenRequest, int, error) {
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
	if f.Status != "" {
		args = append(args, f.Status)
		where += " AND r.status=$" + strconv.Itoa(len(args))
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		args = append(args, "%"+q+"%")
		where += " AND (e.employee_no ILIKE $" + strconv.Itoa(len(args)) +
			" OR e.name ILIKE $" + strconv.Itoa(len(args)) + ")"
	}
	if f.EmployeeID != nil {
		args = append(args, *f.EmployeeID)
		where += " AND r.employee_id=$" + strconv.Itoa(len(args))
	}

	var total int
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM token_requests r
		JOIN employees e ON e.id = r.employee_id `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	args = append(args, limit, offset)
	rows, err := s.pool.Query(ctx, `
		SELECT r.id, r.employee_id, r.kind, r.reason, r.requested_total, r.status,
			r.reviewed_by, r.review_note, r.reviewed_at, r.granted_quota_type,
			r.granted_quota_period, r.granted_total, r.granted_rpm,
			r.granted_expires_at, r.token_id, r.expires_at, r.created_at,
			r.updated_at, coalesce(e.employee_no,''), coalesce(e.name,''),
			coalesce(d.name,'')
		FROM token_requests r
		JOIN employees e ON e.id = r.employee_id
		LEFT JOIN departments d ON d.id = e.department_id
		`+where+`
		ORDER BY CASE r.status WHEN 'pending' THEN 0 WHEN 'approved' THEN 1 ELSE 2 END,
			r.created_at DESC
		LIMIT $`+strconv.Itoa(len(args)-1)+` OFFSET $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	out := []store.TokenRequest{}
	for rows.Next() {
		var r store.TokenRequest
		if err := rows.Scan(&r.ID, &r.EmployeeID, &r.Kind, &r.Reason, &r.RequestedTotal, &r.Status,
			&r.ReviewedBy, &r.ReviewNote, &r.ReviewedAt, &r.GrantedQuotaType,
			&r.GrantedQuotaPeriod, &r.GrantedTotal, &r.GrantedRPM, &r.GrantedExpiresAt,
			&r.TokenID, &r.ExpiresAt, &r.CreatedAt, &r.UpdatedAt,
			&r.EmployeeNo, &r.EmployeeName, &r.DepartmentName); err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}

func (s *TokenRequestStore) Approve(ctx context.Context, id, reviewer uuid.UUID, grant store.TokenRequestGrant, now time.Time) (*store.TokenRequest, error) {
	var r store.TokenRequest
	err := s.pool.QueryRow(ctx, `
		UPDATE token_requests SET status='approved', reviewed_by=$2, review_note=$3,
			reviewed_at=$4, granted_quota_type=$5, granted_quota_period=$6,
			granted_total=$7, granted_rpm=$8, granted_expires_at=$9, expires_at=$10,
			updated_at=$4
		WHERE id=$1 AND kind='token' AND status='pending'
		RETURNING `+tokenRequestColumns,
		id, reviewer, grant.Note, now, grant.QuotaType, grant.QuotaPeriod,
		grant.QuotaTotal, grant.RateLimitRPM, grant.ExpiresAt, grant.ClaimDeadline,
	).Scan(&r.ID, &r.EmployeeID, &r.Kind, &r.Reason, &r.RequestedTotal, &r.Status,
		&r.ReviewedBy, &r.ReviewNote, &r.ReviewedAt, &r.GrantedQuotaType,
		&r.GrantedQuotaPeriod, &r.GrantedTotal, &r.GrantedRPM, &r.GrantedExpiresAt,
		&r.TokenID, &r.ExpiresAt, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			if _, getErr := s.Get(ctx, id); getErr != nil {
				return nil, getErr
			}
			return nil, store.ErrConflict
		}
		return nil, mapError(err)
	}
	return &r, nil
}

// ApproveQuota raises the employee allowance and marks the request granted in
// one transaction. The employee keeps using every existing key; no token is
// created here.
func (s *TokenRequestStore) ApproveQuota(ctx context.Context, id uuid.UUID, reviewer *uuid.UUID, total int64, period, note string, now time.Time) (*store.TokenRequest, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	r, err := scanTokenRequest(tx.QueryRow(ctx, `
		UPDATE token_requests SET status='granted', reviewed_by=$2, review_note=$3,
			reviewed_at=$4, granted_total=$5, updated_at=$4
		WHERE id=$1 AND kind='quota' AND status='pending'
		RETURNING `+tokenRequestColumns,
		id, reviewer, note, now, total))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			var exists bool
			if qerr := tx.QueryRow(ctx,
				`SELECT EXISTS(SELECT 1 FROM token_requests WHERE id=$1)`, id).Scan(&exists); qerr != nil {
				return nil, qerr
			}
			if exists {
				return nil, store.ErrConflict
			}
			return nil, store.ErrNotFound
		}
		return nil, err
	}

	period = strings.TrimSpace(period)
	if period != "daily" && period != "monthly" && period != "total" {
		period = ""
	}

	var currentPeriod string
	lockErr := tx.QueryRow(ctx,
		`SELECT quota_period FROM employee_quotas WHERE employee_id=$1 FOR UPDATE`,
		r.EmployeeID).Scan(&currentPeriod)
	if errors.Is(mapError(lockErr), store.ErrNotFound) {
		if period == "" {
			period = fallbackQuota.Period
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO employee_quotas (employee_id, quota_type, quota_period,
				quota_total, quota_used, period_start, updated_by, updated_at)
			VALUES ($1,$2,$3,$4,0,$5,$6,$7)`,
			r.EmployeeID, fallbackQuota.Type, period, total,
			periodStart(period, now), reviewer, now); err != nil {
			return nil, mapError(err)
		}
	} else if lockErr != nil {
		return nil, mapError(lockErr)
	} else {
		if period == "" {
			period = currentPeriod
		}
		if period == currentPeriod {
			if _, err := tx.Exec(ctx, `
				UPDATE employee_quotas SET quota_total=$2, updated_by=$3,
					updated_at=$4 WHERE employee_id=$1`,
				r.EmployeeID, total, reviewer, now); err != nil {
				return nil, mapError(err)
			}
		} else {
			if _, err := tx.Exec(ctx, `
				UPDATE employee_quotas SET quota_period=$2, quota_total=$3,
					quota_used=0, period_start=$4, updated_by=$5, updated_at=$6
				WHERE employee_id=$1`,
				r.EmployeeID, period, total, periodStart(period, now), reviewer, now); err != nil {
				return nil, mapError(err)
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return r, nil
}

func (s *TokenRequestStore) Deny(ctx context.Context, id, reviewer uuid.UUID, note string, now time.Time) (*store.TokenRequest, error) {
	var r store.TokenRequest
	err := s.pool.QueryRow(ctx, `
		UPDATE token_requests SET status='denied', reviewed_by=$2, review_note=$3,
			reviewed_at=$4, updated_at=$4
		WHERE id=$1 AND status='pending'
		RETURNING `+tokenRequestColumns, id, reviewer, note, now).
		Scan(&r.ID, &r.EmployeeID, &r.Kind, &r.Reason, &r.RequestedTotal, &r.Status,
			&r.ReviewedBy, &r.ReviewNote, &r.ReviewedAt, &r.GrantedQuotaType,
			&r.GrantedQuotaPeriod, &r.GrantedTotal, &r.GrantedRPM, &r.GrantedExpiresAt,
			&r.TokenID, &r.ExpiresAt, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			if _, getErr := s.Get(ctx, id); getErr != nil {
				return nil, getErr
			}
			return nil, store.ErrConflict
		}
		return nil, mapError(err)
	}
	return &r, nil
}

func (s *TokenRequestStore) Cancel(ctx context.Context, id, employeeID uuid.UUID, now time.Time) (*store.TokenRequest, error) {
	var r store.TokenRequest
	err := s.pool.QueryRow(ctx, `
		UPDATE token_requests SET status='cancelled', updated_at=$3
		WHERE id=$1 AND employee_id=$2 AND status='pending'
		RETURNING `+tokenRequestColumns, id, employeeID, now).
		Scan(&r.ID, &r.EmployeeID, &r.Kind, &r.Reason, &r.RequestedTotal, &r.Status,
			&r.ReviewedBy, &r.ReviewNote, &r.ReviewedAt, &r.GrantedQuotaType,
			&r.GrantedQuotaPeriod, &r.GrantedTotal, &r.GrantedRPM, &r.GrantedExpiresAt,
			&r.TokenID, &r.ExpiresAt, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, s.cancelError(ctx, id, employeeID)
		}
		return nil, mapError(err)
	}
	return &r, nil
}

// cancelError distinguishes a missing/foreign request (404) from one that is
// no longer cancellable (409) without leaking another employee's request.
func (s *TokenRequestStore) cancelError(ctx context.Context, id, employeeID uuid.UUID) error {
	var owner uuid.UUID
	var status string
	err := s.pool.QueryRow(ctx,
		`SELECT employee_id, status FROM token_requests WHERE id=$1`, id).
		Scan(&owner, &status)
	if err != nil {
		return mapError(err)
	}
	if owner != employeeID {
		return store.ErrNotFound
	}
	return store.ErrConflict
}

func (s *TokenRequestStore) Claim(ctx context.Context, id, employeeID uuid.UUID, token *store.Token, clientIP string, now time.Time) (*store.TokenRequest, error) {
	if token.ID == uuid.Nil {
		token.ID = uuid.New()
	}
	if token.IPAllowlist == nil {
		token.IPAllowlist = []string{}
	}
	allowlist, err := json.Marshal(token.IPAllowlist)
	if err != nil {
		return nil, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var r store.TokenRequest
	// Claim the request first without touching token_id: the FK is checked
	// immediately, so the token row must exist before the reference is set.
	err = tx.QueryRow(ctx, `
		UPDATE token_requests SET status='claimed', updated_at=$3
		WHERE id=$1 AND employee_id=$2 AND status='approved' AND expires_at > $3
		RETURNING `+tokenRequestColumns, id, employeeID, now).
		Scan(&r.ID, &r.EmployeeID, &r.Kind, &r.Reason, &r.RequestedTotal, &r.Status,
			&r.ReviewedBy, &r.ReviewNote, &r.ReviewedAt, &r.GrantedQuotaType,
			&r.GrantedQuotaPeriod, &r.GrantedTotal, &r.GrantedRPM, &r.GrantedExpiresAt,
			&r.TokenID, &r.ExpiresAt, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, s.claimError(ctx, tx, id, employeeID, now)
		}
		return nil, mapError(err)
	}
	r.Status = "claimed"

	if _, err := tx.Exec(ctx, `
		INSERT INTO tokens (id, employee_id, name, token_hash, token_prefix, token_last4,
			quota_type, quota_period, quota_total, quota_used, period_start,
			rate_limit_rpm, ip_allowlist, status, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
		token.ID, token.EmployeeID, token.Name, token.TokenHash, token.TokenPrefix,
		token.TokenLast4, token.QuotaType, token.QuotaPeriod, token.QuotaTotal,
		token.QuotaUsed, token.PeriodStart, token.RateLimitRPM, allowlist,
		token.Status, token.ExpiresAt,
	); err != nil {
		return nil, mapError(err)
	}

	if _, err := tx.Exec(ctx,
		`UPDATE token_requests SET token_id=$2 WHERE id=$1`, id, token.ID); err != nil {
		return nil, mapError(err)
	}
	r.TokenID = &token.ID

	detail, _ := json.Marshal(map[string]string{"request_id": r.ID.String()})
	if _, err := tx.Exec(ctx, `
		INSERT INTO employee_audit_logs (employee_id, action, target_type, target_id,
			detail, client_ip)
		VALUES ($1,'token_claim','token',$2,$3::jsonb,NULLIF($4,'')::inet)`,
		employeeID, token.ID.String(), detail, clientIP,
	); err != nil {
		return nil, mapError(err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *TokenRequestStore) claimError(ctx context.Context, tx pgx.Tx, id, employeeID uuid.UUID, now time.Time) error {
	var (
		owner     uuid.UUID
		status    string
		expiresAt time.Time
	)
	err := tx.QueryRow(ctx,
		`SELECT employee_id, status, expires_at FROM token_requests WHERE id=$1`, id).
		Scan(&owner, &status, &expiresAt)
	if err != nil {
		return mapError(err)
	}
	if owner != employeeID {
		return store.ErrNotFound
	}
	if status == "approved" && !expiresAt.After(now) {
		return store.ErrRequestExpired
	}
	return store.ErrConflict
}

func (s *TokenRequestStore) ExpireStale(ctx context.Context, employeeID uuid.UUID, now time.Time) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE token_requests SET status='expired', updated_at=$2
		WHERE employee_id=$1 AND status IN ('pending','approved') AND expires_at <= $2`,
		employeeID, now)
	return err
}

func (s *EmployeeAuditStore) Insert(ctx context.Context, log *store.EmployeeAuditLog) error {
	detail := log.Detail
	if len(detail) == 0 {
		detail = []byte("{}")
	}
	return mapError(s.pool.QueryRow(ctx, `
		INSERT INTO employee_audit_logs (employee_id, action, target_type, target_id,
			detail, client_ip)
		VALUES ($1,$2,$3,NULLIF($4,''),$5::jsonb,NULLIF($6,'')::inet)
		RETURNING id, created_at`,
		log.EmployeeID, log.Action, log.TargetType, log.TargetID, detail, log.ClientIP,
	).Scan(&log.ID, &log.CreatedAt))
}

func (s *EmployeeAuditStore) ListByEmployee(ctx context.Context, employeeID uuid.UUID, limit int) ([]store.EmployeeAuditLog, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, employee_id, action, target_type, coalesce(target_id,''),
			detail, coalesce(host(client_ip),''), created_at
		FROM employee_audit_logs
		WHERE employee_id=$1 ORDER BY created_at DESC, id DESC LIMIT $2`,
		employeeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []store.EmployeeAuditLog{}
	for rows.Next() {
		var l store.EmployeeAuditLog
		if err := rows.Scan(&l.ID, &l.EmployeeID, &l.Action, &l.TargetType,
			&l.TargetID, &l.Detail, &l.ClientIP, &l.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// EmployeeSummary aggregates one employee's gateway usage. It never touches
// prompt_enc or response_enc.
func (s *AuditStore) EmployeeSummary(ctx context.Context, employeeID uuid.UUID, from, to time.Time) (store.PortalUsageSummary, error) {
	var s2 store.PortalUsageSummary
	err := s.pool.QueryRow(ctx, `
		SELECT count(*),
			coalesce(sum(total_tokens),0),
			coalesce(sum(prompt_tokens),0),
			coalesce(sum(completion_tokens),0),
			count(*) FILTER (WHERE status_code >= 400 AND status_code <> 499),
			count(*) FILTER (WHERE dlp_action <> 'none'),
			coalesce(sum(jsonb_array_length(dlp_hits)),0),
			coalesce((percentile_cont(0.95) WITHIN GROUP (ORDER BY latency_ms)
				FILTER (WHERE latency_ms > 0))::bigint,0)
		FROM audit_logs
		WHERE employee_id=$1 AND event_time >= $2 AND event_time < $3`,
		employeeID, from, to).
		Scan(&s2.Requests, &s2.TotalTokens, &s2.PromptTokens, &s2.CompletionTokens,
			&s2.Errors, &s2.DLPRequests, &s2.DLPHits, &s2.P95LatencyMS)
	return s2, err
}

func (s *AuditStore) EmployeeTrend(ctx context.Context, employeeID uuid.UUID, from, to time.Time) ([]store.PortalUsagePoint, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT date_trunc('day', event_time AT TIME ZONE 'Asia/Shanghai')::date AS day,
			count(*), coalesce(sum(total_tokens),0),
			count(*) FILTER (WHERE status_code >= 400 AND status_code <> 499)
		FROM audit_logs
		WHERE employee_id=$1 AND event_time >= $2 AND event_time < $3
		GROUP BY day ORDER BY day`, employeeID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []store.PortalUsagePoint{}
	for rows.Next() {
		var p store.PortalUsagePoint
		if err := rows.Scan(&p.Day, &p.Requests, &p.Tokens, &p.Errors); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// EmployeeModelUsage folds the (day, model) aggregate into one row per model.
// The ceiling of models is small in practice, so a single grouped query keeps
// the portal overview at one round trip.
func (s *AuditStore) EmployeeModelUsage(ctx context.Context, employeeID uuid.UUID, from, to time.Time) ([]store.PortalModelUsage, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT date_trunc('day', event_time AT TIME ZONE 'Asia/Shanghai')::date AS day,
			coalesce(nullif(model, ''), '未知') AS model,
			count(*), coalesce(sum(total_tokens),0),
			count(*) FILTER (WHERE status_code >= 400 AND status_code <> 499)
		FROM audit_logs
		WHERE employee_id=$1 AND event_time >= $2 AND event_time < $3
		GROUP BY day, model
		ORDER BY day`, employeeID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byModel := map[string]*store.PortalModelUsage{}
	for rows.Next() {
		var (
			day                    time.Time
			model                  string
			requests, tokens, errs int64
		)
		if err := rows.Scan(&day, &model, &requests, &tokens, &errs); err != nil {
			return nil, err
		}
		usage, ok := byModel[model]
		if !ok {
			usage = &store.PortalModelUsage{Model: model}
			byModel[model] = usage
		}
		usage.Requests += requests
		usage.Tokens += tokens
		usage.Errors += errs
		usage.Days = append(usage.Days, store.PortalModelPoint{
			Day: day, Requests: requests, Tokens: tokens,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]store.PortalModelUsage, 0, len(byModel))
	for _, usage := range byModel {
		out = append(out, *usage)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Tokens != out[j].Tokens {
			return out[i].Tokens > out[j].Tokens
		}
		if out[i].Requests != out[j].Requests {
			return out[i].Requests > out[j].Requests
		}
		return out[i].Model < out[j].Model
	})
	return out, nil
}

var (
	_ store.EmployeeAccountStore = (*EmployeeAccountStore)(nil)
	_ store.TokenRequestStore    = (*TokenRequestStore)(nil)
	_ store.EmployeeAuditStore   = (*EmployeeAuditStore)(nil)
)
