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
)

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
	Limit        int
	Offset       int
}

// AuditFilter filters audit listings.
type AuditFilter struct {
	From       *time.Time
	To         *time.Time
	EmployeeID *uuid.UUID
	StatusCode *int
	DLPAction  string
	Model      string
	Cursor     string
	Limit      int
}

// EmployeeStore persists employees.
type EmployeeStore interface {
	Create(ctx context.Context, e *Employee) error
	Get(ctx context.Context, id uuid.UUID) (*Employee, error)
	Update(ctx context.Context, e *Employee) error
	List(ctx context.Context, f EmployeeFilter) ([]Employee, int, error)
}

// TokenStore persists tokens and performs atomic quota operations.
type TokenStore interface {
	Create(ctx context.Context, t *Token) error
	Get(ctx context.Context, id uuid.UUID) (*Token, error)
	FindByHash(ctx context.Context, hash string) (*Token, error)
	Update(ctx context.Context, t *Token) error
	List(ctx context.Context, f TokenFilter) ([]Token, int, error)
	ReserveQuota(ctx context.Context, id uuid.UUID, reserve int64, now time.Time) (Reservation, error)
	SettleQuota(ctx context.Context, r Reservation, actual int64) error
	Touch(ctx context.Context, id uuid.UUID, at time.Time) error
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
	DeleteExpiredSessions(ctx context.Context, before time.Time) error
	InsertAdminAudit(ctx context.Context, a *AdminAuditLog) error
}

// SettingsStore reads and writes JSON settings by key.
type SettingsStore interface {
	Get(ctx context.Context, key string) (json.RawMessage, error)
	Set(ctx context.Context, key string, value json.RawMessage) error
}
