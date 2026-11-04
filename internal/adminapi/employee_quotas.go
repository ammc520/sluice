package adminapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/company/sluice/internal/adminauth"
	"github.com/company/sluice/internal/store"
)

// EmployeeQuotaHandler lets operators inspect and adjust the shared employee
// allowance directly, without going through the approval workflow.
type EmployeeQuotaHandler struct {
	quotas         store.EmployeeQuotaStore
	employees      store.EmployeeStore
	admins         store.AdminStore
	clock          func() time.Time
	trustedProxies []string
}

// NewEmployeeQuotaHandler creates the quota administration handler.
func NewEmployeeQuotaHandler(quotas store.EmployeeQuotaStore, employees store.EmployeeStore, admins store.AdminStore, clock func() time.Time, trustedProxies []string) *EmployeeQuotaHandler {
	if clock == nil {
		clock = time.Now
	}
	return &EmployeeQuotaHandler{
		quotas:         quotas,
		employees:      employees,
		admins:         admins,
		clock:          clock,
		trustedProxies: append([]string(nil), trustedProxies...),
	}
}

type employeeQuotaDTO struct {
	EmployeeID     string    `json:"employee_id"`
	QuotaType      string    `json:"quota_type"`
	QuotaPeriod    string    `json:"quota_period"`
	QuotaTotal     int64     `json:"quota_total"`
	QuotaUsed      int64     `json:"quota_used"`
	QuotaRemaining int64     `json:"quota_remaining"`
	PeriodStart    time.Time `json:"period_start"`
	ResetsAt       time.Time `json:"resets_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func toEmployeeQuotaDTO(q *store.EmployeeQuota, now time.Time) employeeQuotaDTO {
	remaining := q.QuotaTotal - q.QuotaUsed
	if remaining < 0 {
		remaining = 0
	}
	return employeeQuotaDTO{
		EmployeeID:     q.EmployeeID.String(),
		QuotaType:      q.QuotaType,
		QuotaPeriod:    q.QuotaPeriod,
		QuotaTotal:     q.QuotaTotal,
		QuotaUsed:      q.QuotaUsed,
		QuotaRemaining: remaining,
		PeriodStart:    q.PeriodStart,
		ResetsAt:       nextPeriodStart(q.QuotaPeriod, now),
		UpdatedAt:      q.UpdatedAt,
	}
}

type employeeQuotaPayload struct {
	QuotaType   string `json:"quota_type"`
	QuotaPeriod string `json:"quota_period"`
	QuotaTotal  *int64 `json:"quota_total"`
}

// Get handles GET /admin/api/employees/:id/quota.
func (h *EmployeeQuotaHandler) Get(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	if _, err := h.employees.Get(ctx, id); err != nil {
		writeStoreError(c, err, "employee not found", "employee not found", "employee not found")
		return
	}
	q, err := h.quotas.Get(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			Fail(c, http.StatusNotFound, "not_found", "employee quota not initialised")
			return
		}
		InternalError(c)
		return
	}
	OK(c, http.StatusOK, toEmployeeQuotaDTO(q, h.clock()))
}

// Update handles PUT /admin/api/employees/:id/quota.
func (h *EmployeeQuotaHandler) Update(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var payload employeeQuotaPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		BadRequest(c, "invalid JSON body")
		return
	}
	payload.QuotaType = strings.TrimSpace(payload.QuotaType)
	payload.QuotaPeriod = strings.TrimSpace(payload.QuotaPeriod)
	if payload.QuotaType == "" {
		payload.QuotaType = "tokens"
	}
	if payload.QuotaType != "requests" && payload.QuotaType != "tokens" {
		BadRequest(c, "quota_type must be requests or tokens")
		return
	}
	if payload.QuotaPeriod == "" {
		payload.QuotaPeriod = "monthly"
	}
	switch payload.QuotaPeriod {
	case "daily", "monthly", "total":
	default:
		BadRequest(c, "quota_period must be daily, monthly, or total")
		return
	}
	if payload.QuotaTotal == nil || *payload.QuotaTotal < 0 {
		BadRequest(c, "quota_total must be a non-negative integer")
		return
	}

	ctx := c.Request.Context()
	if _, err := h.employees.Get(ctx, id); err != nil {
		writeStoreError(c, err, "employee not found", "employee not found", "employee not found")
		return
	}
	reviewer := adminauth.CurrentUser(c)
	if reviewer == nil {
		Fail(c, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}
	before, _ := h.quotas.Get(ctx, id)
	q, err := h.quotas.SetTotal(ctx, id, payload.QuotaType, payload.QuotaPeriod,
		*payload.QuotaTotal, &reviewer.ID, h.clock())
	if err != nil {
		writeStoreError(c, err, "employee not found", "employee not found", "employee not found")
		return
	}

	detail := map[string]any{
		"employee_id":  id.String(),
		"quota_type":   q.QuotaType,
		"quota_period": q.QuotaPeriod,
		"quota_total":  q.QuotaTotal,
	}
	if before != nil {
		detail["previous_total"] = before.QuotaTotal
		detail["previous_period"] = before.QuotaPeriod
		detail["previous_type"] = before.QuotaType
	}
	_ = recordAdminAudit(c, h.admins, h.trustedProxies, "update_employee_quota",
		"employee", id.String(), detail)
	OK(c, http.StatusOK, toEmployeeQuotaDTO(q, h.clock()))
}

func nextPeriodStart(period string, now time.Time) time.Time {
	local := now.In(shanghaiLocation())
	switch period {
	case "daily":
		return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, shanghaiLocation()).AddDate(0, 0, 1)
	case "monthly":
		return time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, shanghaiLocation()).AddDate(0, 1, 0)
	default:
		return time.Time{}
	}
}
