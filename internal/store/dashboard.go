package store

import (
	"time"

	"github.com/google/uuid"
)

// DashboardFilter limits dashboard aggregates to a time window and optional
// department/provider dimensions.
type DashboardFilter struct {
	From         time.Time
	To           time.Time
	DepartmentID *int64
	ProviderID   *uuid.UUID
}

// DashboardKPI is one aggregate row over a dashboard window.
type DashboardKPI struct {
	Requests          int64
	ActiveUsers       int64
	Tokens            int64
	PromptTokens      int64
	CompletionTokens  int64
	Errors            int64
	ClientDisconnects int64
	DLPRequests       int64
	DLPHits           int64
	P95LatencyMS      int64
}

// DashboardTrendPoint is one hourly or daily bucket.
type DashboardTrendPoint struct {
	Bucket           time.Time
	Requests         int64
	Errors           int64
	Tokens           int64
	PromptTokens     int64
	CompletionTokens int64
	DLPRequests      int64
	P95LatencyMS     *int64
}

// DashboardProviderStat is usage attributed to one upstream provider.
type DashboardProviderStat struct {
	ID           string
	Name         string
	Requests     int64
	Tokens       int64
	Errors       int64
	P95LatencyMS int64
}

// DashboardModelStat is usage attributed to one requested model.
type DashboardModelStat struct {
	Model    string
	Requests int64
	Tokens   int64
}

// DashboardUserStat is usage attributed to one employee.
type DashboardUserStat struct {
	EmployeeID  uuid.UUID
	EmployeeNo  string
	Name        string
	Department  string
	Requests    int64
	Tokens      int64
	Errors      int64
	DLPRequests int64
}

// DashboardDLPActionStat counts requests and rule hits by final action.
type DashboardDLPActionStat struct {
	Action   string
	Requests int64
	Hits     int64
}

// DashboardDLPRuleStat counts rule hits.
type DashboardDLPRuleStat struct {
	RuleName string
	Action   string
	Hits     int64
}

// DashboardIncident is a recent DLP event with metadata only. Prompt and
// response content are never included.
type DashboardIncident struct {
	ID           int64
	EventTime    time.Time
	EmployeeID   *uuid.UUID
	EmployeeName string
	Action       string
	Rules        []string
	Model        string
	RequestID    string
}

// DashboardQuota is the quota state of one active token.
type DashboardQuota struct {
	TokenID      uuid.UUID
	EmployeeID   uuid.UUID
	EmployeeName string
	Department   string
	TokenPrefix  string
	TokenLast4   string
	QuotaType    string
	QuotaPeriod  string
	Used         int64
	Total        int64
	PeriodStart  *time.Time
	ExpiresAt    *time.Time
}

// DashboardData is the raw aggregate backing one summary response.
type DashboardData struct {
	KPI           DashboardKPI
	PreviousKPI   DashboardKPI
	Trend         []DashboardTrendPoint
	PreviousTrend []DashboardTrendPoint
	Providers     []DashboardProviderStat
	Models        []DashboardModelStat
	Users         []DashboardUserStat
	DLPByAction   []DashboardDLPActionStat
	DLPByRule     []DashboardDLPRuleStat
	Incidents     []DashboardIncident
	QuotaPressure []DashboardQuota
	UserQuotas    map[uuid.UUID]DashboardQuota
}
