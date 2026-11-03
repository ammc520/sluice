package adminapi

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/company/api-key/internal/store"
)

// DepartmentHandler serves the read-only department directory.
type DepartmentHandler struct {
	departments store.DepartmentStore
}

// NewDepartmentHandler creates the department handler.
func NewDepartmentHandler(departments store.DepartmentStore) *DepartmentHandler {
	return &DepartmentHandler{departments: departments}
}

type departmentDTO struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// List handles GET /admin/api/departments.
func (h *DepartmentHandler) List(c *gin.Context) {
	items, err := h.departments.List(c.Request.Context())
	if err != nil {
		InternalError(c)
		return
	}
	out := make([]departmentDTO, 0, len(items))
	for _, item := range items {
		out = append(out, departmentDTO{ID: item.ID, Name: item.Name})
	}
	OK(c, http.StatusOK, gin.H{"items": out})
}
