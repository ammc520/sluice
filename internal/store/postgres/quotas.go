package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/company/sluice/internal/store"
)

const maxReservePerRequest int64 = 1_000_000

func isNoRows(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}

// The per-request hold is chosen by the stored quota type: a request-count
// quota holds one unit, a token quota holds the declared max_tokens (or the
// configured default).
const insertReserveSQL = `CASE WHEN $8 = 'requests' THEN 1::bigint
	ELSE LEAST(CASE WHEN $3 > 0 THEN $3 ELSE $4 END, 1000000) END`

const conflictReserveSQL = `CASE WHEN employee_quotas.quota_type = 'requests' THEN 1::bigint
	ELSE LEAST(CASE WHEN $3 > 0 THEN $3 ELSE $4 END, 1000000) END`

const employeeRollingStartSQL = `CASE employee_quotas.quota_period
	WHEN 'daily' THEN $5::timestamptz
	WHEN 'monthly' THEN $6::timestamptz
	ELSE $2::timestamptz END`

// employeeQuotaReserveSQL creates the row when missing and otherwise performs
// the rollover check, quota check and increment in the same statement. No row
// is returned exactly when the employee's allowance is exhausted.
var employeeQuotaReserveSQL = `
INSERT INTO employee_quotas (employee_id, quota_type, quota_period, quota_total,
	quota_used, period_start)
SELECT $1, $8, $9, $10, ` + insertReserveSQL + `, $7
WHERE ` + insertReserveSQL + ` <= $10
ON CONFLICT (employee_id) DO UPDATE SET
	quota_used = CASE
		WHEN employee_quotas.quota_period <> 'total'
			AND employee_quotas.period_start < ` + employeeRollingStartSQL + `
			THEN ` + conflictReserveSQL + `
		ELSE employee_quotas.quota_used + ` + conflictReserveSQL + `
	END,
	period_start = CASE
		WHEN employee_quotas.quota_period <> 'total'
			AND employee_quotas.period_start < ` + employeeRollingStartSQL + `
			THEN ` + employeeRollingStartSQL + `
		ELSE employee_quotas.period_start
	END
WHERE ` + conflictReserveSQL + ` <= employee_quotas.quota_total - (
	CASE WHEN employee_quotas.quota_period <> 'total'
		AND employee_quotas.period_start < ` + employeeRollingStartSQL + `
		THEN 0::bigint ELSE employee_quotas.quota_used END)
RETURNING quota_type, quota_total, quota_used, period_start`

const tokenRollingStartSQL = `CASE quota_period
	WHEN 'daily' THEN $2::timestamptz
	WHEN 'monthly' THEN $3::timestamptz
	ELSE $4::timestamptz END`

// tokenQuotaReserveSQL rolls the key's optional cap in the same statement. It
// returns no row when the cap is exceeded, so the caller can tell that apart
// from a missing token with one extra (failure-only) lookup.
var tokenQuotaReserveSQL = `
UPDATE tokens SET
	quota_used = CASE
		WHEN quota_period <> 'total'
			AND (period_start IS NULL OR period_start < ` + tokenRollingStartSQL + `)
			THEN $5
		ELSE quota_used + $5
	END,
	period_start = CASE
		WHEN quota_period <> 'total'
			AND (period_start IS NULL OR period_start < ` + tokenRollingStartSQL + `)
			THEN ` + tokenRollingStartSQL + `
		ELSE COALESCE(period_start, ` + tokenRollingStartSQL + `)
	END
WHERE id=$1 AND employee_id=$6
	AND (quota_total IS NULL OR $5 <= quota_total - (
		CASE WHEN quota_period <> 'total'
			AND (period_start IS NULL OR period_start < ` + tokenRollingStartSQL + `)
			THEN 0::bigint ELSE quota_used END))
RETURNING quota_total, quota_used, period_start`

const employeeQuotaColumns = `employee_id, quota_type, quota_period, quota_total,
	quota_used, period_start, updated_by, updated_at`

// fallbackQuota is used only when a row must be created lazily (for example an
// employee created after the migration). Administrators can adjust it at any
// time, and the portal's configured default is used at employee creation.
var fallbackQuota = store.EmployeeQuotaDefaults{Type: "tokens", Period: "monthly", Total: 1_000_000}

func scanEmployeeQuota(row rowScanner) (*store.EmployeeQuota, error) {
	var q store.EmployeeQuota
	err := row.Scan(&q.EmployeeID, &q.QuotaType, &q.QuotaPeriod, &q.QuotaTotal,
		&q.QuotaUsed, &q.PeriodStart, &q.UpdatedBy, &q.UpdatedAt)
	if err != nil {
		return nil, mapError(err)
	}
	return &q, nil
}

func (s *EmployeeQuotaStore) Get(ctx context.Context, employeeID uuid.UUID) (*store.EmployeeQuota, error) {
	return scanEmployeeQuota(s.pool.QueryRow(ctx,
		`SELECT `+employeeQuotaColumns+` FROM employee_quotas WHERE employee_id=$1`, employeeID))
}

// Ensure creates the quota row on first use. The insert and the follow-up read
// are not one transaction: a concurrent creator simply wins the ON CONFLICT,
// and the read always returns the winning row.
func (s *EmployeeQuotaStore) Ensure(ctx context.Context, employeeID uuid.UUID, defaults store.EmployeeQuotaDefaults) (*store.EmployeeQuota, error) {
	if defaults.Type != "requests" {
		defaults.Type = "tokens"
	}
	switch defaults.Period {
	case "daily", "monthly", "total":
	default:
		defaults.Period = "monthly"
	}
	if defaults.Total < 0 {
		defaults.Total = 0
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO employee_quotas (employee_id, quota_type, quota_period,
			quota_total, quota_used, period_start)
		VALUES ($1,$2,$3,$4,0,$5)
		ON CONFLICT (employee_id) DO NOTHING`,
		employeeID, defaults.Type, defaults.Period, defaults.Total,
		periodStart(defaults.Period, time.Now()))
	if err != nil {
		return nil, mapError(err)
	}
	return s.Get(ctx, employeeID)
}

// Reserve applies the employee hold and the optional per-key hold in one
// transaction. Both counters are rolled over and checked inside single UPDATE
// statements so a request costs four round trips even when a quota row has to
// be created lazily.
func (s *EmployeeQuotaStore) Reserve(ctx context.Context, employeeID, tokenID uuid.UUID, maxTokens, defaultReserve int64, now time.Time) (store.Reservation, error) {
	if maxTokens < 0 || defaultReserve < 0 {
		return store.Reservation{}, &store.QuotaExceededError{Scope: "employee"}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return store.Reservation{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// The per-request hold is clamped here and repeated in SQL because the
	// unit depends on the stored quota type ('requests' holds one request).
	reserve := maxTokens
	if reserve <= 0 {
		reserve = defaultReserve
	}
	if reserve > maxReservePerRequest {
		reserve = maxReservePerRequest
	}

	dailyStart := periodStart("daily", now)
	monthlyStart := periodStart("monthly", now)
	defaults := fallbackQuota

	var (
		quotaType  string
		quotaTotal int64
		quotaUsed  int64
		start      time.Time
	)
	err = tx.QueryRow(ctx, employeeQuotaReserveSQL,
		employeeID,                        // $1
		now,                               // $2
		maxTokens,                         // $3
		defaultReserve,                    // $4
		dailyStart,                        // $5
		monthlyStart,                      // $6
		periodStart(defaults.Period, now), // $7
		defaults.Type,                     // $8
		defaults.Period,                   // $9
		defaults.Total,                    // $10
	).Scan(&quotaType, &quotaTotal, &quotaUsed, &start)
	if err != nil {
		if isNoRows(err) {
			return store.Reservation{}, &store.QuotaExceededError{Scope: "employee"}
		}
		return store.Reservation{}, mapError(err)
	}
	if quotaType == "requests" {
		reserve = 1
	}

	var (
		tokenTotal *int64
		tokenUsed  int64
		tokenStart *time.Time
	)
	err = tx.QueryRow(ctx, tokenQuotaReserveSQL,
		tokenID,      // $1
		dailyStart,   // $2
		monthlyStart, // $3
		now,          // $4
		reserve,      // $5
		employeeID,   // $6
	).Scan(&tokenTotal, &tokenUsed, &tokenStart)
	if err != nil {
		if !isNoRows(err) {
			return store.Reservation{}, mapError(err)
		}
		var exists bool
		if qerr := tx.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM tokens WHERE id=$1 AND employee_id=$2)`,
			tokenID, employeeID).Scan(&exists); qerr != nil {
			return store.Reservation{}, mapError(qerr)
		}
		if !exists {
			return store.Reservation{}, store.ErrNotFound
		}
		return store.Reservation{}, &store.QuotaExceededError{Scope: "token"}
	}
	if err := tx.Commit(ctx); err != nil {
		return store.Reservation{}, err
	}
	return store.Reservation{
		EmployeeID:       employeeID,
		TokenID:          tokenID,
		QuotaType:        quotaType,
		PeriodStart:      start,
		TokenPeriodStart: tokenStart,
		Reserved:         reserve,
		TokenReserved:    reserve,
	}, nil
}

// Settle adjusts both counters with relative arithmetic, so no read is needed
// before the writes. A row is only touched when its period start still belongs
// to the reservation: a rollover in flight means the usage belongs to the old
// period.
func (s *EmployeeQuotaStore) Settle(ctx context.Context, r store.Reservation, actual int64) error {
	if actual < 0 {
		actual = 0
	}
	if r.EmployeeID == uuid.Nil || r.TokenID == uuid.Nil {
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var matched int
	err = tx.QueryRow(ctx, `
		UPDATE employee_quotas SET quota_used =
			LEAST(quota_total, GREATEST(0, quota_used - $2 + $3))
		WHERE employee_id=$1 AND period_start=$4
		RETURNING 1`, r.EmployeeID, r.Reserved, actual, r.PeriodStart).Scan(&matched)
	if err != nil {
		if isNoRows(err) {
			return tx.Commit(ctx)
		}
		return mapError(err)
	}

	_, err = tx.Exec(ctx, `
		UPDATE tokens SET quota_used = CASE
			WHEN quota_total IS NULL
				THEN GREATEST(0, quota_used - $1 + $2)
			ELSE LEAST(quota_total, GREATEST(0, quota_used - $1 + $2))
		END
		WHERE id=$3
			AND (period_start IS NULL OR $4::timestamptz IS NULL OR period_start=$4::timestamptz)`,
		r.TokenReserved, actual, r.TokenID, r.TokenPeriodStart)
	if err != nil {
		return mapError(err)
	}
	return tx.Commit(ctx)
}

func (s *EmployeeQuotaStore) SetTotal(ctx context.Context, employeeID uuid.UUID, quotaType, period string, total int64, updatedBy *uuid.UUID, now time.Time) (*store.EmployeeQuota, error) {
	if quotaType != "requests" {
		quotaType = "tokens"
	}
	switch period {
	case "daily", "monthly", "total":
	default:
		period = "monthly"
	}
	if total < 0 {
		total = 0
	}
	start := periodStart(period, now)
	var q store.EmployeeQuota
	err := s.pool.QueryRow(ctx, `
		INSERT INTO employee_quotas (employee_id, quota_type, quota_period,
			quota_total, quota_used, period_start, updated_by, updated_at)
		VALUES ($1,$2,$3,$4,0,$5,$6,$7)
		ON CONFLICT (employee_id) DO UPDATE SET
			quota_type=EXCLUDED.quota_type,
			quota_period=EXCLUDED.quota_period,
			quota_total=EXCLUDED.quota_total,
			quota_used=CASE WHEN employee_quotas.quota_period <> EXCLUDED.quota_period
				THEN 0 ELSE employee_quotas.quota_used END,
			period_start=CASE WHEN employee_quotas.quota_period <> EXCLUDED.quota_period
				THEN EXCLUDED.period_start ELSE employee_quotas.period_start END,
			updated_by=EXCLUDED.updated_by,
			updated_at=EXCLUDED.updated_at
		RETURNING `+employeeQuotaColumns,
		employeeID, quotaType, period, total, start, updatedBy, now).
		Scan(&q.EmployeeID, &q.QuotaType, &q.QuotaPeriod, &q.QuotaTotal,
			&q.QuotaUsed, &q.PeriodStart, &q.UpdatedBy, &q.UpdatedAt)
	if err != nil {
		return nil, mapError(err)
	}
	return &q, nil
}

var _ store.EmployeeQuotaStore = (*EmployeeQuotaStore)(nil)
