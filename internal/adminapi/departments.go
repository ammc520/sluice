package adminapi

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/company/sluice/internal/store"
)

// DepartmentHandler serves the department directory and its mutations.
type DepartmentHandler struct {
	departments store.DepartmentStore
}

// NewDepartmentHandler creates the department handler.
func NewDepartmentHandler(departments store.DepartmentStore) *DepartmentHandler {
	return &DepartmentHandler{departments: departments}
}

type departmentDTO struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	EmployeeCount int64  `json:"employee_count"`
}

type departmentPayload struct {
	Name string `json:"name"`
}

func (p *departmentPayload) validate() string {
	p.Name = strings.TrimSpace(p.Name)
	switch {
	case p.Name == "":
		return "name is required"
	case len([]rune(p.Name)) > 50:
		return "name is too long"
	}
	return ""
}

func departmentPathID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		BadRequest(c, "invalid department id")
		return 0, false
	}
	return id, true
}

// Directory handles GET /admin/api/departments.
func (h *DepartmentHandler) Directory(c *gin.Context) {
	directory, err := h.departments.Directory(c.Request.Context())
	if err != nil {
		InternalError(c)
		return
	}
	items := make([]departmentDTO, 0, len(directory.Items))
	for _, item := range directory.Items {
		items = append(items, departmentDTO{
			ID:            item.ID,
			Name:          item.Name,
			EmployeeCount: item.EmployeeCount,
		})
	}
	OK(c, http.StatusOK, gin.H{
		"items":            items,
		"unassigned_count": directory.Unassigned,
		"total_employees":  directory.Total,
	})
}

// Create handles POST /admin/api/departments.
func (h *DepartmentHandler) Create(c *gin.Context) {
	var payload departmentPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		BadRequest(c, "invalid JSON body")
		return
	}
	if msg := payload.validate(); msg != "" {
		BadRequest(c, msg)
		return
	}
	department, err := h.departments.Create(c.Request.Context(), payload.Name)
	if err != nil {
		writeStoreError(c, err, "department not found", "department name already exists", "department not found")
		return
	}
	OK(c, http.StatusCreated, departmentDTO{
		ID:   department.ID,
		Name: department.Name,
	})
}

// Rename handles PUT /admin/api/departments/:id.
func (h *DepartmentHandler) Rename(c *gin.Context) {
	id, ok := departmentPathID(c)
	if !ok {
		return
	}
	var payload departmentPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		BadRequest(c, "invalid JSON body")
		return
	}
	if msg := payload.validate(); msg != "" {
		BadRequest(c, msg)
		return
	}
	department, err := h.departments.Rename(c.Request.Context(), id, payload.Name)
	if err != nil {
		writeStoreError(c, err, "department not found", "department name already exists", "department not found")
		return
	}
	OK(c, http.StatusOK, departmentDTO{
		ID:            department.ID,
		Name:          department.Name,
		EmployeeCount: department.EmployeeCount,
	})
}

// Delete handles DELETE /admin/api/departments/:id.
//
// Without move_to the department must be empty. With move_to=<id> or
// move_to=none its employees are reassigned before the department is removed.
func (h *DepartmentHandler) Delete(c *gin.Context) {
	id, ok := departmentPathID(c)
	if !ok {
		return
	}
	opts := store.DepartmentDeleteOptions{}
	if raw, present := c.GetQuery("move_to"); present {
		raw = strings.TrimSpace(raw)
		switch {
		case raw == "none":
			opts.Reassign = true
		case raw != "":
			target, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || target <= 0 {
				BadRequest(c, "invalid move_to")
				return
			}
			if target == id {
				BadRequest(c, "move_to must differ from the department being deleted")
				return
			}
			opts.Reassign = true
			opts.MoveTo = &target
		default:
			BadRequest(c, "invalid move_to")
			return
		}
	}

	moved, err := h.departments.Delete(c.Request.Context(), id, opts)
	if err != nil {
		writeStoreError(c, err,
			"department not found",
			"department still has employees; reassign them first",
			"move_to department does not exist")
		return
	}
	OK(c, http.StatusOK, gin.H{"deleted": true, "moved_employees": moved})
}
