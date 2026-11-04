package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	// ErrNotFound is returned when a row does not exist.
	ErrNotFound = errors.New("store: not found")
	// ErrQuotaExceeded is returned when a quota reservation cannot be satisfied.
	ErrQuotaExceeded = errors.New("store: quota exceeded")
	// ErrConflict is returned when a unique constraint is violated.
	ErrConflict = errors.New("store: conflict")
	// ErrInvalidReference is returned when a foreign key target does not exist.
	ErrInvalidReference = errors.New("store: invalid reference")
	// ErrLastSuperAdmin is returned when a change would leave no active
	// super administrator.
	ErrLastSuperAdmin = errors.New("store: last super admin")
	// ErrRequestExpired is returned when a token request is approved but its
	// claim window has elapsed.
	ErrRequestExpired = errors.New("store: request expired")
	// ErrTokenLimitReached is returned when an employee already holds the
	// maximum number of active keys.
	ErrTokenLimitReached = errors.New("store: active token limit reached")
	// ErrTokenRevoked is returned when an operation requires an active key but
	// the key has already been disabled.
	ErrTokenRevoked = errors.New("store: token revoked")
	// ErrTokenExpired is returned when an operation requires an unexpired key.
	ErrTokenExpired = errors.New("store: token expired")
)

// QuotaExceededError identifies which layer rejected a request: the shared
// employee pool or an optional per-key cap. It unwraps to ErrQuotaExceeded so
// existing callers keep working.
type QuotaExceededError struct {
	Scope string // "employee" or "token"
}

func (e *QuotaExceededError) Error() string {
	if e.Scope == "" {
		return "store: quota exceeded"
	}
	return "store: quota exceeded (" + e.Scope + ")"
}

func (e *QuotaExceededError) Unwrap() error { return ErrQuotaExceeded }

// TokenFilter filters token listings.
type TokenFilter struct {
	EmployeeID *uuid.UUID
	Status     *int16
	Limit      int
	Offset     int
}

// EmployeeFilter filters employee listings.
type EmployeeFilter struct {
	Query        string
	DepartmentID *int64
	Unassigned   bool
	Status       *int16
	Limit        int
	Offset       int
}

// AuditFilter filters audit listings.
type AuditFilter struct {
	From         *time.Time
	To           *time.Time
	EmployeeID   *uuid.UUID
	DepartmentID *int64
	StatusCode   *int
	DLPAction    string
	Model        string
	Cursor       string
	Limit        int
}

// EmployeeStore persists employees.
type EmployeeStore interface {
	Create(ctx context.Context, e *Employee) error
	Get(ctx context.Context, id uuid.UUID) (*Employee, error)
	Update(ctx context.Context, e *Employee) error
	List(ctx context.Context, f EmployeeFilter) ([]Employee, int, error)
}

// EmployeeAccountStore persists portal credentials and sessions.
type EmployeeAccountStore interface {
	// FindAccountByEmployeeNo resolves the credential and its employee in one
	// query so login can distinguish a missing account from a disabled one.
	FindAccountByEmployeeNo(ctx context.Context, employeeNo string) (*EmployeeAccount, *Employee, error)
	GetAccount(ctx context.Context, employeeID uuid.UUID) (*EmployeeAccount, error)
	CreateAccount(ctx context.Context, a *EmployeeAccount) error
	// ReplacePassword sets an administrator-issued password and clears the
	// failure/lockout state in one statement.
	ReplacePassword(ctx context.Context, employeeID uuid.UUID, passwordHash string, mustChange bool) error
	// ChangePassword swaps the hash only if it still matches the hash the
	// caller verified, returning false when a concurrent reset won. Callers
	// must not fall back to a full-row update, which could undo a reset.
	ChangePassword(ctx context.Context, employeeID uuid.UUID, currentHash, newHash string) (bool, error)
	// RecordLoginFailure increments the failure counter atomically and locks
	// the account once maxAttempts is reached. A read-modify-write here would
	// lose concurrent failures and undercount the lockout.
	RecordLoginFailure(ctx context.Context, employeeID uuid.UUID, maxAttempts int, lockout time.Duration, now time.Time) error
	// RecordLoginSuccess clears the failure state after a correct password.
	RecordLoginSuccess(ctx context.Context, employeeID uuid.UUID, at time.Time) error
	DeleteAccount(ctx context.Context, employeeID uuid.UUID) error

	CreateSession(ctx context.Context, s *EmployeeSession) error
	FindSessionByHash(ctx context.Context, hash string) (*EmployeeSession, error)
	TouchSession(ctx context.Context, id uuid.UUID, at time.Time) error
	DeleteSession(ctx context.Context, hash string) error
	DeleteSessionsByEmployeeID(ctx context.Context, employeeID uuid.UUID) error
	DeleteExpiredSessions(ctx context.Context, before time.Time) error
}

// TokenRequestFilter filters admin token-request listings.
type TokenRequestFilter struct {
	EmployeeID *uuid.UUID
	Status     string
	Query      string
	Limit      int
	Offset     int
}

// TokenRequestStore persists the employee token application workflow.
type TokenRequestStore interface {
	// Create sweeps expired requests for the employee before inserting, so the
	// partial unique index never blocks a legitimate retry.
	Create(ctx context.Context, r *TokenRequest) error
	Get(ctx context.Context, id uuid.UUID) (*TokenRequest, error)
	ListByEmployee(ctx context.Context, employeeID uuid.UUID, limit int) ([]TokenRequest, error)
	List(ctx context.Context, f TokenRequestFilter) ([]TokenRequest, int, error)
	Approve(ctx context.Context, id, reviewer uuid.UUID, grant TokenRequestGrant, now time.Time) (*TokenRequest, error)
	// ApproveQuota atomically raises the employee's quota and marks the request
	// granted. A nil reviewer means the request was auto-approved by policy.
	ApproveQuota(ctx context.Context, id uuid.UUID, reviewer *uuid.UUID, total int64, period, note string, now time.Time) (*TokenRequest, error)
	Deny(ctx context.Context, id, reviewer uuid.UUID, note string, now time.Time) (*TokenRequest, error)
	Cancel(ctx context.Context, id, employeeID uuid.UUID, now time.Time) (*TokenRequest, error)
	// Claim atomically marks an approved request claimed, inserts the token,
	// and writes the employee audit row. The plaintext token never reaches
	// this layer.
	Claim(ctx context.Context, id, employeeID uuid.UUID, token *Token, clientIP string, now time.Time) (*TokenRequest, error)
	ExpireStale(ctx context.Context, employeeID uuid.UUID, now time.Time) error
}

// EmployeeAuditStore records self-service portal actions.
type EmployeeAuditStore interface {
	Insert(ctx context.Context, log *EmployeeAuditLog) error
	ListByEmployee(ctx context.Context, employeeID uuid.UUID, limit int) ([]EmployeeAuditLog, error)
}

// TokenStore persists tokens and performs atomic quota operations.
type TokenStore interface {
	Create(ctx context.Context, t *Token) error
	// CreateWithLimit inserts t only when the employee holds fewer than
	// maxActive unexpired keys. It returns ErrTokenLimitReached otherwise and
	// is atomic with respect to concurrent creates for the same employee.
	CreateWithLimit(ctx context.Context, t *Token, maxActive int, now time.Time) error
	Get(ctx context.Context, id uuid.UUID) (*Token, error)
	FindByHash(ctx context.Context, hash string) (*Token, error)
	Update(ctx context.Context, t *Token) error
	// Rotate atomically revokes oldID and inserts fresh for the same employee.
	// It returns ErrNotFound when the key is missing or not owned,
	// ErrTokenRevoked when it is already inactive, and ErrTokenExpired when it
	// is past its expiry.
	Rotate(ctx context.Context, employeeID, oldID uuid.UUID, fresh *Token, now time.Time) error
	List(ctx context.Context, f TokenFilter) ([]Token, int, error)
	ReserveQuota(ctx context.Context, id uuid.UUID, reserve int64, now time.Time) (Reservation, error)
	SettleQuota(ctx context.Context, r Reservation, actual int64) error
	CountActiveByEmployee(ctx context.Context, employeeID uuid.UUID, now time.Time) (int, error)
	Touch(ctx context.Context, id uuid.UUID, at time.Time) error
}

// EmployeeQuotaStore owns the shared employee allowance. Reserve and Settle
// also maintain the key-level reporting counter, so both layers are updated in
// one transaction.
type EmployeeQuotaStore interface {
	Get(ctx context.Context, employeeID uuid.UUID) (*EmployeeQuota, error)
	// Ensure creates the row with defaults when it does not exist yet.
	Ensure(ctx context.Context, employeeID uuid.UUID, defaults EmployeeQuotaDefaults) (*EmployeeQuota, error)
	// Reserve charges the employee pool and the key counter. maxTokens is the
	// declared generation budget; the store converts it to requests or tokens
	// using the employee quota type, clamping at one million.
	Reserve(ctx context.Context, employeeID, tokenID uuid.UUID, maxTokens, defaultReserve int64, now time.Time) (Reservation, error)
	Settle(ctx context.Context, r Reservation, actual int64) error
	// SetTotal changes the allowance. Lowering it below the current usage is
	// allowed; calls stay blocked until the period resets or the total rises.
	SetTotal(ctx context.Context, employeeID uuid.UUID, quotaType, period string, total int64, updatedBy *uuid.UUID, now time.Time) (*EmployeeQuota, error)
}

// ProviderStore persists upstream providers.
type ProviderStore interface {
	Create(ctx context.Context, p *Provider) error
	Get(ctx context.Context, id uuid.UUID) (*Provider, error)
	List(ctx context.Context) ([]Provider, error)
	Update(ctx context.Context, p *Provider) error
	Delete(ctx context.Context, id uuid.UUID) error
}

// RouteStore persists model routes.
type RouteStore interface {
	Create(ctx context.Context, r *ModelRoute) error
	List(ctx context.Context) ([]ModelRoute, error)
	ListEnabled(ctx context.Context) ([]ModelRoute, error)
	Delete(ctx context.Context, id uuid.UUID) error
}

// DLPRuleStore persists DLP rules.
type DLPRuleStore interface {
	Create(ctx context.Context, r *DLPRule) error
	Get(ctx context.Context, id uuid.UUID) (*DLPRule, error)
	List(ctx context.Context) ([]DLPRule, error)
	ListEnabled(ctx context.Context) ([]DLPRule, error)
	Update(ctx context.Context, r *DLPRule) error
	Delete(ctx context.Context, id uuid.UUID) error
}

// DepartmentDirectory is the department list plus org-wide employee counts.
type DepartmentDirectory struct {
	Items      []Department
	Unassigned int64
	Total      int64
}

// DepartmentDeleteOptions controls what happens to a department's employees
// when the department is removed. Reassign must be set to move employees;
// MoveTo nil with Reassign true moves them to "unassigned".
type DepartmentDeleteOptions struct {
	Reassign bool
	MoveTo   *int64
}

// DepartmentStore manages the department directory and its members.
type DepartmentStore interface {
	Directory(ctx context.Context) (DepartmentDirectory, error)
	Create(ctx context.Context, name string) (*Department, error)
	Rename(ctx context.Context, id int64, name string) (*Department, error)
	Delete(ctx context.Context, id int64, opts DepartmentDeleteOptions) (int64, error)
}

// DashboardStore serves the aggregated overview dashboard.
type DashboardStore interface {
	Summary(ctx context.Context, current, previous DashboardFilter, interval string, now time.Time) (DashboardData, error)
}

// AuditStore persists and queries audit records.
type AuditStore interface {
	InsertBatch(ctx context.Context, records []*AuditRecord) error
	List(ctx context.Context, f AuditFilter) ([]*AuditRecord, error)
	Get(ctx context.Context, id int64) (*AuditRecord, error)
	Overview(ctx context.Context, from, to time.Time) (DashboardOverview, error)
	Trend(ctx context.Context, from, to time.Time) ([]TrendPoint, error)
	TopUsers(ctx context.Context, from, to time.Time, limit int) ([]UserUsage, error)
	DLPStats(ctx context.Context, from, to time.Time) ([]DLPStat, error)
	DeleteBefore(ctx context.Context, before time.Time) (int64, error)
	// EmployeeSummary and EmployeeTrend power the employee portal. They are
	// scoped by employee_id and never decrypt prompt/response payloads.
	EmployeeSummary(ctx context.Context, employeeID uuid.UUID, from, to time.Time) (PortalUsageSummary, error)
	EmployeeTrend(ctx context.Context, employeeID uuid.UUID, from, to time.Time) ([]PortalUsagePoint, error)
	// EmployeeModelUsage groups the same period by model (and day) so the
	// overview can show which models an employee actually uses.
	EmployeeModelUsage(ctx context.Context, employeeID uuid.UUID, from, to time.Time) ([]PortalModelUsage, error)
}

// AdminStore persists administrators, sessions, and admin audit logs.
type AdminStore interface {
	FindByUsername(ctx context.Context, username string) (*AdminUser, error)
	Get(ctx context.Context, id uuid.UUID) (*AdminUser, error)
	Create(ctx context.Context, u *AdminUser) error
	Update(ctx context.Context, u *AdminUser) error
	List(ctx context.Context) ([]AdminUser, error)
	CreateSession(ctx context.Context, s *AdminSession) error
	FindSessionByHash(ctx context.Context, hash string) (*AdminSession, error)
	TouchSession(ctx context.Context, id uuid.UUID, at time.Time) error
	DeleteSession(ctx context.Context, hash string) error
	DeleteSessionsByAdminID(ctx context.Context, adminID uuid.UUID) error
	DeleteExpiredSessions(ctx context.Context, before time.Time) error
	InsertAdminAudit(ctx context.Context, a *AdminAuditLog) error
}

// SettingsStore reads and writes JSON settings by key.
type SettingsStore interface {
	Get(ctx context.Context, key string) (json.RawMessage, error)
	Set(ctx context.Context, key string, value json.RawMessage) error
}
