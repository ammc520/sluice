package adminapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// OK writes the standard success envelope.
func OK(c *gin.Context, status int, data any) {
	c.JSON(status, gin.H{"data": data, "error": nil})
}

// Fail writes the standard error envelope without internal details.
func Fail(c *gin.Context, status int, code, message string) {
	c.JSON(status, gin.H{
		"data":  nil,
		"error": gin.H{"code": code, "message": message},
	})
}

// BadRequest is a shorthand for validation failures.
func BadRequest(c *gin.Context, message string) {
	Fail(c, http.StatusBadRequest, "invalid_request", message)
}

// InternalError hides implementation details from clients.
func InternalError(c *gin.Context) {
	Fail(c, http.StatusInternalServerError, "internal_error", "internal server error")
}
