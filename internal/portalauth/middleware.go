package portalauth

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/company/sluice/internal/portalcfg"
	"github.com/company/sluice/internal/store"
)

const (
	// ContextEmployee holds *store.Employee in the Gin context.
	ContextEmployee = "portalauth.employee"
	// ContextAccount holds *store.EmployeeAccount in the Gin context.
	ContextAccount = "portalauth.account"
	// ContextSession holds *store.EmployeeSession in the Gin context.
	ContextSession = "portalauth.session"

	SessionCookie = "employee_session"
	CSRFCookie    = "employee_csrf"
	CSRFHeader    = "X-CSRF-Token"

	// CookiePath keeps portal cookies away from /admin/api and /v1.
	CookiePath = "/portal"
)

// RequirePortalEnabled hides the whole portal surface while it is disabled.
// It returns 404 rather than 403 so the routes do not advertise themselves.
func RequirePortalEnabled(cfg *portalcfg.Provider) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cfg == nil || !cfg.Get(c.Request.Context()).Enabled {
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{
				"data":  nil,
				"error": gin.H{"code": "not_found", "message": "not found"},
			})
			return
		}
		c.Next()
	}
}

// RequireSession rejects requests without a valid employee session.
func RequireSession(svc *Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, err := c.Cookie(SessionCookie)
		if err != nil {
			abortUnauthorized(c)
			return
		}
		employee, account, sess, err := svc.Validate(c.Request.Context(), token)
		if err != nil {
			switch {
			case errors.Is(err, ErrSessionNotFound),
				errors.Is(err, ErrSessionExpired),
				errors.Is(err, ErrAccountDisabled):
				abortUnauthorized(c)
			default:
				// Authentication infrastructure failed: fail closed.
				c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
					"data":  nil,
					"error": gin.H{"code": "auth_unavailable", "message": "authentication temporarily unavailable"},
				})
			}
			return
		}
		c.Set(ContextEmployee, employee)
		c.Set(ContextAccount, account)
		c.Set(ContextSession, sess)
		c.Next()
	}
}

// RequireCSRF rejects unsafe methods without a matching CSRF header.
func RequireCSRF() gin.HandlerFunc {
	return func(c *gin.Context) {
		switch c.Request.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			c.Next()
			return
		}
		sess := CurrentSession(c)
		if sess == nil || !ValidateCSRF(sess, c.GetHeader(CSRFHeader)) {
			abortForbidden(c, "csrf_failed", "invalid CSRF token")
			return
		}
		c.Next()
	}
}

// RequirePasswordChanged blocks portal data endpoints until the employee
// replaces an administrator-issued initial password.
func RequirePasswordChanged() gin.HandlerFunc {
	return func(c *gin.Context) {
		account := CurrentAccount(c)
		if account != nil && account.MustChangePassword {
			abortForbidden(c, "password_change_required", "password change required")
			return
		}
		c.Next()
	}
}

// CurrentEmployee returns the authenticated employee.
func CurrentEmployee(c *gin.Context) *store.Employee {
	v, ok := c.Get(ContextEmployee)
	if !ok {
		return nil
	}
	e, _ := v.(*store.Employee)
	return e
}

// CurrentAccount returns the authenticated employee's credential record.
func CurrentAccount(c *gin.Context) *store.EmployeeAccount {
	v, ok := c.Get(ContextAccount)
	if !ok {
		return nil
	}
	a, _ := v.(*store.EmployeeAccount)
	return a
}

// CurrentSession returns the authenticated portal session.
func CurrentSession(c *gin.Context) *store.EmployeeSession {
	v, ok := c.Get(ContextSession)
	if !ok {
		return nil
	}
	s, _ := v.(*store.EmployeeSession)
	return s
}

func abortUnauthorized(c *gin.Context) {
	c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
		"data":  nil,
		"error": gin.H{"code": "unauthorized", "message": "authentication required"},
	})
}

func abortForbidden(c *gin.Context, code, message string) {
	c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
		"data":  nil,
		"error": gin.H{"code": code, "message": message},
	})
}
