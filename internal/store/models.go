package store

import (
	"time"

	"github.com/google/uuid"
)

// Department groups employees for reporting and filtering.
type Department struct {
	ID   int64
	Name string
}

// Employee is a company employee who may hold API tokens.
type Employee struct {
	ID           uuid.UUID
	EmployeeNo   string
	Name         string
	Email        string
	DepartmentID *int64
	Status       int16
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Token is a company-issued gateway token. Only the SHA-256 hash of the
// plaintext token is stored.
type Token struct {
	ID           uuid.UUID
	EmployeeID   uuid.UUID
	TokenHash    string
	TokenPrefix  string
	TokenLast4   string
	QuotaType    string
	QuotaPeriod  string
	QuotaTotal   *int64
	QuotaUsed    int64
	PeriodStart  *time.Time
	RateLimitRPM int
	IPAllowlist  []string
	Status       int16
	ExpiresAt    *time.Time
	CreatedAt    time.Time
	LastUsedAt   *time.Time
}

// Provider is an upstream model provider.
type Provider struct {
	ID             uuid.UUID
	Name           string
	Protocol       string
	BaseURL        string
	APIKeyEnc      []byte
	IsDefault      bool
	Enabled        bool
	TimeoutSeconds int
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// ModelRoute maps a requested model name to a provider.
type ModelRoute struct {
	ID            uuid.UUID
	ProviderID    uuid.UUID
	Protocol      string
	Pattern       string
	MatchType     string
	UpstreamModel *string
	Priority      int
	Enabled       bool
	CreatedAt     time.Time
}

// DLPRule is a data loss prevention rule.
type DLPRule struct {
	ID           uuid.UUID
	Name         string
	Description  string
	Category     string
	MatchType    string
	Pattern      string
	Action       string
	Replacement  string
	Priority     int
	Enabled      bool
	Builtin      bool
	AlertEnabled bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// AuditRecord is the persisted form of one gateway request.
type AuditRecord struct {
	ID                 int64
	EventTime          time.Time
	RequestID          string
	TokenID            *uuid.UUID
	EmployeeID         *uuid.UUID
	DepartmentID       *int64
	ClientIP           string
	Method             string
	Path               string
	Model              string
	ProviderID         *uuid.UUID
	StatusCode         int
	ErrorCode          string
	LatencyMS          int
	UpstreamLatencyMS  int
	FirstByteMS        int
	PromptTokens       int
	CompletionTokens   int
	TotalTokens        int
	UsageEstimated     bool
	PromptEnc          []byte
	ResponseEnc        []byte
	PromptTruncated    bool
	ResponseTruncated  bool
	DLPAction          string
	DLPHits            []byte
	ClientDisconnected bool
	UpstreamRequestID  string
	CreatedAt          time.Time
}

// AdminUser is an independent administrator account.
type AdminUser struct {
	ID           uuid.UUID
	Username     string
	PasswordHash string
	Role         string
	Status       int16
	LastLoginAt  *time.Time
	CreatedAt    time.Time
}

// AdminSession is a server-side admin session.
type AdminSession struct {
	ID         uuid.UUID
	AdminID    uuid.UUID
	TokenHash  string
	CSRFHash   string
	ExpiresAt  time.Time
	CreatedAt  time.Time
	LastSeenAt time.Time
}

// AdminAuditLog records administrator actions.
type AdminAuditLog struct {
	ID         int64
	AdminID    uuid.UUID
	Action     string
	TargetType string
	TargetID   string
	Detail     []byte
	ClientIP   string
	CreatedAt  time.Time
}

// Reservation is a quota hold created before an upstream request.
type Reservation struct {
	TokenID     uuid.UUID
	PeriodStart time.Time
	Reserved    int64
}

// DashboardOverview is the aggregate summary for a period.
type DashboardOverview struct {
	TotalCalls  int64
	ActiveUsers int64
	TotalTokens int64
	DLPHits     int64
	ErrorCalls  int64
}

// TrendPoint is a daily aggregate.
type TrendPoint struct {
	Day    time.Time
	Calls  int64
	Tokens int64
}

// UserUsage is a per-employee aggregate.
type UserUsage struct {
	EmployeeID uuid.UUID
	Name       string
	Calls      int64
	Tokens     int64
}

// DLPStat is a per-rule DLP hit aggregate.
type DLPStat struct {
	RuleName string
	Action   string
	Hits     int64
}
