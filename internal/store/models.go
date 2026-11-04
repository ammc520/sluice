package store

import (
	"time"

	"github.com/google/uuid"
)

// Department groups employees for reporting and filtering.
type Department struct {
	ID            int64
	Name          string
	EmployeeCount int64
}

// Employee is a company employee who may hold API tokens. The quota fields
// are a read-only projection of the employee_quotas row; nil means the row has
// not been created yet and will be lazily initialised on first use.
type Employee struct {
	ID                uuid.UUID
	EmployeeNo        string
	Name              string
	Email             string
	DepartmentID      *int64
	DepartmentName    string
	Status            int16
	QuotaType         *string
	QuotaPeriod       *string
	QuotaTotal        *int64
	QuotaUsed         int64
	QuotaPeriodStart  *time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// Token is a company-issued gateway token. Only the SHA-256 hash of the
// plaintext token is stored.
type Token struct {
	ID           uuid.UUID
	EmployeeID   uuid.UUID
	Name         string
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

// EmployeeAccount is the portal credential record for an employee. It is
// separate from Employee so organisation data can be edited without touching
// credentials.
type EmployeeAccount struct {
	EmployeeID         uuid.UUID
	PasswordHash       string
	MustChangePassword bool
	FailedAttempts     int
	LockedUntil        *time.Time
	LastLoginAt        *time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// EmployeeSession is a server-side portal session. Only hashes are stored.
type EmployeeSession struct {
	ID         uuid.UUID
	EmployeeID uuid.UUID
	TokenHash  string
	CSRFHash   string
	IP         string
	UserAgent  string
	ExpiresAt  time.Time
	CreatedAt  time.Time
	LastSeenAt time.Time
}

// EmployeeQuota is the shared, employee-level allowance. Every key held by the
// employee consumes this single counter. QuotaTotal is required; zero means
// calls are blocked until an administrator raises it.
type EmployeeQuota struct {
	EmployeeID         uuid.UUID
	EmployeeNo         string
	EmployeeName       string
	DepartmentName     string
	QuotaType          string
	QuotaPeriod        string
	QuotaTotal         int64
	QuotaUsed          int64
	PeriodStart        time.Time
	UpdatedBy          *uuid.UUID
	UpdatedAt          time.Time
}

// EmployeeQuotaDefaults seeds a missing employee quota row.
type EmployeeQuotaDefaults struct {
	Type   string
	Period string
	Total  int64
}

// TokenRequest is an employee application for a gateway token or a quota
// increase. Kind='quota' requests are granted immediately on approval;
// kind='token' requests are the legacy flow that still requires a claim.
// EmployeeNo, EmployeeName, and DepartmentName are populated by admin listings.
type TokenRequest struct {
	ID                 uuid.UUID
	EmployeeID         uuid.UUID
	EmployeeNo         string
	EmployeeName       string
	DepartmentName     string
	Kind               string
	Reason             string
	RequestedTotal     *int64
	Status             string
	ReviewedBy         *uuid.UUID
	ReviewNote         string
	ReviewedAt         *time.Time
	GrantedQuotaType   *string
	GrantedQuotaPeriod *string
	GrantedTotal       *int64
	GrantedRPM         *int
	GrantedExpiresAt   *time.Time
	TokenID            *uuid.UUID
	ExpiresAt          time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// TokenRequestGrant is the administrator decision attached to an approved
// request.
type TokenRequestGrant struct {
	QuotaType     string
	QuotaPeriod   string
	QuotaTotal    *int64
	RateLimitRPM  int
	ExpiresAt     *time.Time
	Note          string
	ClaimDeadline time.Time
}

// EmployeeAuditLog records a self-service action taken in the employee portal.
type EmployeeAuditLog struct {
	ID         int64
	EmployeeID uuid.UUID
	Action     string
	TargetType string
	TargetID   string
	Detail     []byte
	ClientIP   string
	CreatedAt  time.Time
}

// PortalUsageSummary is the employee-scoped equivalent of the admin KPI block.
type PortalUsageSummary struct {
	Requests         int64 `json:"requests"`
	TotalTokens      int64 `json:"total_tokens"`
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	Errors           int64 `json:"errors"`
	DLPRequests      int64 `json:"dlp_requests"`
	DLPHits          int64 `json:"dlp_hits"`
	P95LatencyMS     int64 `json:"p95_latency_ms"`
}

// PortalUsagePoint is one day of employee usage.
type PortalUsagePoint struct {
	Day      time.Time
	Requests int64
	Tokens   int64
	Errors   int64
}

// PortalModelPoint is one day of usage for a single model.
type PortalModelPoint struct {
	Day      time.Time
	Requests int64
	Tokens   int64
}

// PortalModelUsage is the per-model breakdown shown on the portal overview.
// Days only contains days with traffic; clients fill the gaps.
type PortalModelUsage struct {
	Model    string
	Requests int64
	Tokens   int64
	Errors   int64
	Days     []PortalModelPoint
}

// Reservation is a quota hold created before an upstream request. PeriodStart
// is the employee quota period that was charged; TokenPeriodStart is the key's
// own reporting period and may be nil when the key has no cap.
type Reservation struct {
	EmployeeID       uuid.UUID
	TokenID          uuid.UUID
	QuotaType        string
	PeriodStart      time.Time
	TokenPeriodStart *time.Time
	Reserved         int64
	TokenReserved    int64
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
