package portalapi

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/company/api-key/internal/portalauth"
	"github.com/company/api-key/internal/portalcfg"
	"github.com/company/api-key/internal/store"
)

type quotaHandler struct {
	quotas store.EmployeeQuotaStore
	tokens store.TokenStore
	cfg    *portalcfg.Provider
	clock  func() time.Time
}

type portalQuotaDTO struct {
	QuotaType          string     `json:"quota_type"`
	QuotaPeriod        string     `json:"quota_period"`
	QuotaTotal         int64      `json:"quota_total"`
	QuotaUsed          int64      `json:"quota_used"`
	QuotaRemaining     int64      `json:"quota_remaining"`
	PeriodStart        time.Time  `json:"period_start"`
	ResetsAt           *time.Time `json:"resets_at"`
	ActiveTokens       int        `json:"active_tokens"`
	MaxActiveTokens    int        `json:"max_active_tokens"`
	SelfServiceEnabled bool       `json:"self_service_enabled"`
	AllowSelfRotate    bool       `json:"allow_self_rotate"`
	AllowSelfRevoke    bool       `json:"allow_self_revoke"`
	CanRequestIncrease bool       `json:"can_request_increase"`
}

// currentQuota loads the employee's quota, creating it from the portal policy
// on first use.
func (h *quotaHandler) currentQuota(c *gin.Context, employee *store.Employee, settings portalcfg.Settings) (*store.EmployeeQuota, error) {
	def := settings.DefaultQuota
	return h.quotas.Ensure(c.Request.Context(), employee.ID, store.EmployeeQuotaDefaults{
		Type:   def.Type,
		Period: def.Period,
		Total:  def.Total,
	})
}

func quotaDTO(q *store.EmployeeQuota, settings portalcfg.Settings, active int, now time.Time) portalQuotaDTO {
	remaining := q.QuotaTotal - q.QuotaUsed
	if remaining < 0 {
		remaining = 0
	}
	return portalQuotaDTO{
		QuotaType:          q.QuotaType,
		QuotaPeriod:        q.QuotaPeriod,
		QuotaTotal:         q.QuotaTotal,
		QuotaUsed:          q.QuotaUsed,
		QuotaRemaining:     remaining,
		PeriodStart:        q.PeriodStart,
		ResetsAt:           nextReset(q.QuotaPeriod, now),
		ActiveTokens:       active,
		MaxActiveTokens:    settings.MaxActiveTokens,
		SelfServiceEnabled: settings.SelfServiceEnabled,
		AllowSelfRotate:    settings.AllowSelfRotate,
		AllowSelfRevoke:    settings.AllowSelfRevoke,
		CanRequestIncrease: true,
	}
}

// Get handles GET /portal/api/me/quota.
func (h *quotaHandler) Get(c *gin.Context) {
	employee := portalauth.CurrentEmployee(c)
	if employee == nil {
		Fail(c, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}
	settings := h.cfg.Get(c.Request.Context())
	q, err := h.currentQuota(c, employee, settings)
	if err != nil {
		InternalError(c)
		return
	}
	active, err := h.tokens.CountActiveByEmployee(c.Request.Context(), employee.ID, h.clock())
	if err != nil {
		InternalError(c)
		return
	}
	OK(c, http.StatusOK, quotaDTO(q, settings, active, h.clock()))
}

func nextReset(period string, now time.Time) *time.Time {
	local := now.In(shanghaiLocation())
	switch period {
	case "daily":
		next := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, shanghaiLocation()).AddDate(0, 0, 1)
		return &next
	case "monthly":
		next := time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, shanghaiLocation()).AddDate(0, 1, 0)
		return &next
	default:
		return nil
	}
}

var shanghaiLoc = func() *time.Location {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return time.FixedZone("CST", 8*3600)
	}
	return loc
}()

func shanghaiLocation() *time.Location { return shanghaiLoc }
