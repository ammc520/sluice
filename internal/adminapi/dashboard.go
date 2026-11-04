package adminapi

import (
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/company/sluice/internal/store"
)

const (
	defaultTopUsersLimit = 10
	maxTopUsersLimit     = 100
)

// DashboardHandler serves aggregate usage and DLP statistics.
type DashboardHandler struct {
	audit     store.AuditStore
	dashboard store.DashboardStore
	now       func() time.Time
	cache     *summaryCache
}

// NewDashboardHandler creates the dashboard handler.
func NewDashboardHandler(audit store.AuditStore, dashboard store.DashboardStore, clock func() time.Time) *DashboardHandler {
	if clock == nil {
		clock = time.Now
	}
	return &DashboardHandler{
		audit:     audit,
		dashboard: dashboard,
		now:       clock,
		cache:     newSummaryCache(),
	}
}

type dashboardOverviewDTO struct {
	From        time.Time `json:"from"`
	To          time.Time `json:"to"`
	TotalCalls  int64     `json:"total_calls"`
	ActiveUsers int64     `json:"active_users"`
	TotalTokens int64     `json:"total_tokens"`
	DLPHits     int64     `json:"dlp_hits"`
	ErrorCalls  int64     `json:"error_calls"`
	ErrorRate   float64   `json:"error_rate"`
}

type trendPointDTO struct {
	Day    string `json:"day"`
	Calls  int64  `json:"calls"`
	Tokens int64  `json:"tokens"`
}

type topUserDTO struct {
	EmployeeID string `json:"employee_id"`
	Name       string `json:"name"`
	Calls      int64  `json:"calls"`
	Tokens     int64  `json:"tokens"`
}

type dlpStatDTO struct {
	RuleName string `json:"rule_name"`
	Action   string `json:"action"`
	Hits     int64  `json:"hits"`
}

// Overview handles GET /admin/api/dashboard/overview.
func (h *DashboardHandler) Overview(c *gin.Context) {
	from, to, ok := queryTimeRange(c, h.now())
	if !ok {
		return
	}
	overview, err := h.audit.Overview(c.Request.Context(), from, to)
	if err != nil {
		InternalError(c)
		return
	}
	var errorRate float64
	if overview.TotalCalls > 0 {
		errorRate = math.Round(float64(overview.ErrorCalls)/float64(overview.TotalCalls)*10000) / 10000
	}
	OK(c, http.StatusOK, dashboardOverviewDTO{
		From:        from,
		To:          to,
		TotalCalls:  overview.TotalCalls,
		ActiveUsers: overview.ActiveUsers,
		TotalTokens: overview.TotalTokens,
		DLPHits:     overview.DLPHits,
		ErrorCalls:  overview.ErrorCalls,
		ErrorRate:   errorRate,
	})
}

// Trend handles GET /admin/api/dashboard/trend.
func (h *DashboardHandler) Trend(c *gin.Context) {
	interval := strings.TrimSpace(c.Query("interval"))
	if interval == "" {
		interval = "day"
	}
	if interval != "day" {
		BadRequest(c, "interval must be day")
		return
	}
	from, to, ok := queryTimeRange(c, h.now())
	if !ok {
		return
	}
	points, err := h.audit.Trend(c.Request.Context(), from, to)
	if err != nil {
		InternalError(c)
		return
	}
	items := make([]trendPointDTO, 0, len(points))
	for _, point := range points {
		items = append(items, trendPointDTO{
			Day:    point.Day.Format("2006-01-02"),
			Calls:  point.Calls,
			Tokens: point.Tokens,
		})
	}
	OK(c, http.StatusOK, gin.H{"interval": interval, "items": items})
}

// TopUsers handles GET /admin/api/dashboard/top-users.
func (h *DashboardHandler) TopUsers(c *gin.Context) {
	limit := defaultTopUsersLimit
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			BadRequest(c, "limit must be a positive integer")
			return
		}
		limit = parsed
		if limit > maxTopUsersLimit {
			limit = maxTopUsersLimit
		}
	}
	from, to, ok := queryTimeRange(c, h.now())
	if !ok {
		return
	}
	users, err := h.audit.TopUsers(c.Request.Context(), from, to, limit)
	if err != nil {
		InternalError(c)
		return
	}
	items := make([]topUserDTO, 0, len(users))
	for _, user := range users {
		items = append(items, topUserDTO{
			EmployeeID: user.EmployeeID.String(),
			Name:       user.Name,
			Calls:      user.Calls,
			Tokens:     user.Tokens,
		})
	}
	OK(c, http.StatusOK, gin.H{"items": items})
}

// DLP handles GET /admin/api/dashboard/dlp.
func (h *DashboardHandler) DLP(c *gin.Context) {
	from, to, ok := queryTimeRange(c, h.now())
	if !ok {
		return
	}
	stats, err := h.audit.DLPStats(c.Request.Context(), from, to)
	if err != nil {
		InternalError(c)
		return
	}
	items := make([]dlpStatDTO, 0, len(stats))
	for _, stat := range stats {
		items = append(items, dlpStatDTO{
			RuleName: stat.RuleName,
			Action:   stat.Action,
			Hits:     stat.Hits,
		})
	}
	OK(c, http.StatusOK, gin.H{"items": items})
}
