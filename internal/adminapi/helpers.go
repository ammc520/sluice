package adminapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/company/sluice/internal/store"
)

const (
	defaultPageSize = 50
	maxPageSize     = 200
)

// pagination reads page/page_size and returns the clamped slice bounds.
func pagination(c *gin.Context) (page, pageSize int) {
	page = 1
	if v, err := strconv.Atoi(c.Query("page")); err == nil && v > 0 {
		page = v
	}
	pageSize = defaultPageSize
	if v, err := strconv.Atoi(c.Query("page_size")); err == nil && v > 0 {
		pageSize = v
	}
	if pageSize > maxPageSize {
		pageSize = maxPageSize
	}
	return page, pageSize
}

// pathID parses the :id path parameter as a UUID.
func pathID(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		BadRequest(c, "invalid id")
		return uuid.Nil, false
	}
	return id, true
}

// queryUUID parses an optional UUID query parameter.
func queryUUID(c *gin.Context, name string) (*uuid.UUID, bool) {
	raw := strings.TrimSpace(c.Query(name))
	if raw == "" {
		return nil, true
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		BadRequest(c, "invalid "+name)
		return nil, false
	}
	return &id, true
}

// writeStoreError maps common store failures to API responses.
func writeStoreError(c *gin.Context, err error, notFoundMsg, conflictMsg, invalidRefMsg string) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		Fail(c, http.StatusNotFound, "not_found", notFoundMsg)
	case errors.Is(err, store.ErrConflict):
		Fail(c, http.StatusConflict, "conflict", conflictMsg)
	case errors.Is(err, store.ErrInvalidReference):
		BadRequest(c, invalidRefMsg)
	case errors.Is(err, store.ErrLastSuperAdmin):
		Fail(c, http.StatusConflict, "last_super_admin", "cannot remove the last active super administrator")
	default:
		InternalError(c)
	}
}

func pageResponse(items any, total, page, pageSize int) gin.H {
	return gin.H{"items": items, "total": total, "page": page, "page_size": pageSize}
}
