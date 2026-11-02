package adminapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/company/api-key/internal/store"
)

// EmployeeHandler serves employee administration endpoints.
type EmployeeHandler struct {
	employees store.EmployeeStore
}

// NewEmployeeHandler creates the employee handler.
func NewEmployeeHandler(employees store.EmployeeStore) *EmployeeHandler {
	return &EmployeeHandler{employees: employees}
}

type employeeDTO struct {
	ID           string    `json:"id"`
	EmployeeNo   string    `json:"employee_no"`
	Name         string    `json:"name"`
	Email        string    `json:"email"`
	DepartmentID *int64    `json:"department_id"`
	Status       int16     `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func toEmployeeDTO(e *store.Employee) employeeDTO {
	return employeeDTO{
		ID:           e.ID.String(),
		EmployeeNo:   e.EmployeeNo,
		Name:         e.Name,
		Email:        e.Email,
		DepartmentID: e.DepartmentID,
		Status:       e.Status,
		CreatedAt:    e.CreatedAt,
		UpdatedAt:    e.UpdatedAt,
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
	if err := h.employees.Create(c.Request.Context(), e); err != nil {
		writeStoreError(c, err, "employee not found", "employee_no already exists", "department does not exist")
		return
	}
	OK(c, http.StatusCreated, toEmployeeDTO(e))
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
	OK(c, http.StatusOK, toEmployeeDTO(e))
}
