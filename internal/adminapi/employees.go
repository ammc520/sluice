package adminapi

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/company/api-key/internal/portalcfg"
	"github.com/company/api-key/internal/store"
)

// EmployeeHandler serves employee administration endpoints.
type EmployeeHandler struct {
	employees store.EmployeeStore
	quotas    store.EmployeeQuotaStore
	cfg       *portalcfg.Provider
}

// NewEmployeeHandler creates the employee handler. quotas and cfg may be nil
// in tests; newly created employees then get the built-in default allowance.
func NewEmployeeHandler(employees store.EmployeeStore, quotas store.EmployeeQuotaStore, cfg *portalcfg.Provider) *EmployeeHandler {
	return &EmployeeHandler{employees: employees, quotas: quotas, cfg: cfg}
}

type employeeDTO struct {
	ID               string     `json:"id"`
	EmployeeNo       string     `json:"employee_no"`
	Name             string     `json:"name"`
	Email            string     `json:"email"`
	DepartmentID     *int64     `json:"department_id"`
	DepartmentName   string     `json:"department_name"`
	Status           int16      `json:"status"`
	QuotaType        *string    `json:"quota_type"`
	QuotaPeriod      *string    `json:"quota_period"`
	QuotaTotal       *int64     `json:"quota_total"`
	QuotaUsed        int64      `json:"quota_used"`
	QuotaPeriodStart *time.Time `json:"quota_period_start"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

func toEmployeeDTO(e *store.Employee) employeeDTO {
	return employeeDTO{
		ID:               e.ID.String(),
		EmployeeNo:       e.EmployeeNo,
		Name:             e.Name,
		Email:            e.Email,
		DepartmentID:     e.DepartmentID,
		DepartmentName:   e.DepartmentName,
		Status:           e.Status,
		QuotaType:        e.QuotaType,
		QuotaPeriod:      e.QuotaPeriod,
		QuotaTotal:       e.QuotaTotal,
		QuotaUsed:        e.QuotaUsed,
		QuotaPeriodStart: e.QuotaPeriodStart,
		CreatedAt:        e.CreatedAt,
		UpdatedAt:        e.UpdatedAt,
	}
}

type employeePayload struct {
	EmployeeNo   string `json:"employee_no"`
	Name         string `json:"name"`
	Email        string `json:"email"`
	DepartmentID *int64 `json:"department_id"`
	Status       *int16 `json:"status"`
}

func (p *employeePayload) validate() string {
	p.EmployeeNo = strings.TrimSpace(p.EmployeeNo)
	p.Name = strings.TrimSpace(p.Name)
	p.Email = strings.TrimSpace(p.Email)
	switch {
	case p.EmployeeNo == "":
		return "employee_no is required"
	case len(p.EmployeeNo) > 64:
		return "employee_no is too long"
	case p.Name == "":
		return "name is required"
	case len(p.Name) > 128:
		return "name is too long"
	case len(p.Email) > 255:
		return "email is too long"
	case p.DepartmentID != nil && *p.DepartmentID <= 0:
		return "department_id must be positive"
	case p.Status != nil && *p.Status != 0 && *p.Status != 1:
		return "status must be 0 or 1"
	}
	return ""
}

// List handles GET /admin/api/employees.
func (h *EmployeeHandler) List(c *gin.Context) {
	page, pageSize := pagination(c)
	filter := store.EmployeeFilter{
		Query:  strings.TrimSpace(c.Query("q")),
		Limit:  pageSize,
		Offset: (page - 1) * pageSize,
	}
	if raw := strings.TrimSpace(c.Query("department_id")); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			BadRequest(c, "invalid department_id")
			return
		}
		filter.DepartmentID = &id
	}
	if raw := strings.TrimSpace(c.Query("unassigned")); raw != "" {
		switch raw {
		case "1", "true":
			filter.Unassigned = true
		default:
			BadRequest(c, "invalid unassigned")
			return
		}
	}
	if filter.DepartmentID != nil && filter.Unassigned {
		BadRequest(c, "department_id and unassigned are mutually exclusive")
		return
	}
	if raw := strings.TrimSpace(c.Query("status")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || (value != 0 && value != 1) {
			BadRequest(c, "invalid status")
			return
		}
		status := int16(value)
		filter.Status = &status
	}

	items, total, err := h.employees.List(c.Request.Context(), filter)
	if err != nil {
		InternalError(c)
		return
	}
	out := make([]employeeDTO, 0, len(items))
	for i := range items {
		out = append(out, toEmployeeDTO(&items[i]))
	}
	OK(c, http.StatusOK, pageResponse(out, total, page, pageSize))
}

// Create handles POST /admin/api/employees.
func (h *EmployeeHandler) Create(c *gin.Context) {
	var payload employeePayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		BadRequest(c, "invalid JSON body")
		return
	}
	if msg := payload.validate(); msg != "" {
		BadRequest(c, msg)
		return
	}

	status := int16(1)
	if payload.Status != nil {
		status = *payload.Status
	}
	e := &store.Employee{
		EmployeeNo:   payload.EmployeeNo,
		Name:         payload.Name,
		Email:        payload.Email,
		DepartmentID: payload.DepartmentID,
		Status:       status,
	}
	ctx := c.Request.Context()
	if err := h.employees.Create(ctx, e); err != nil {
		writeStoreError(c, err, "employee not found", "employee_no already exists", "department does not exist")
		return
	}
	h.ensureQuota(ctx, e.ID)
	stored, err := h.employees.Get(ctx, e.ID)
	if err != nil {
		InternalError(c)
		return
	}
	OK(c, http.StatusCreated, toEmployeeDTO(stored))
}

// ensureQuota seeds the employee's shared allowance from the portal policy.
// Failure is not fatal: the gateway lazily creates the row on first use.
func (h *EmployeeHandler) ensureQuota(ctx context.Context, employeeID uuid.UUID) {
	if h.quotas == nil {
		return
	}
	defaults := store.EmployeeQuotaDefaults{Type: "tokens", Period: "monthly", Total: 1_000_000}
	if h.cfg != nil {
		quota := h.cfg.Get(ctx).DefaultQuota
		defaults = store.EmployeeQuotaDefaults{Type: quota.Type, Period: quota.Period, Total: quota.Total}
	}
	_, _ = h.quotas.Ensure(ctx, employeeID, defaults)
}

// Update handles PUT /admin/api/employees/:id.
func (h *EmployeeHandler) Update(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var payload employeePayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		BadRequest(c, "invalid JSON body")
		return
	}
	if msg := payload.validate(); msg != "" {
		BadRequest(c, msg)
		return
	}

	ctx := c.Request.Context()
	e, err := h.employees.Get(ctx, id)
	if err != nil {
		writeStoreError(c, err, "employee not found", "employee_no already exists", "department does not exist")
		return
	}
	e.EmployeeNo = payload.EmployeeNo
	e.Name = payload.Name
	e.Email = payload.Email
	e.DepartmentID = payload.DepartmentID
	if payload.Status != nil {
		e.Status = *payload.Status
	}
	if err := h.employees.Update(ctx, e); err != nil {
		writeStoreError(c, err, "employee not found", "employee_no already exists", "department does not exist")
		return
	}
	stored, err := h.employees.Get(ctx, e.ID)
	if err != nil {
		InternalError(c)
		return
	}
	OK(c, http.StatusOK, toEmployeeDTO(stored))
}
