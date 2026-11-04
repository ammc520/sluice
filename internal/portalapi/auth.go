package portalapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/company/sluice/internal/auth"
	"github.com/company/sluice/internal/portalauth"
	"github.com/company/sluice/internal/store"
)

const passwordMinLength = 10
const passwordMaxLength = 128

type authHandler struct {
	svc            *portalauth.Service
	cookieSecure   bool
	trustedProxies []string
}

type portalEmployeeDTO struct {
	ID           string `json:"id"`
	EmployeeNo   string `json:"employee_no"`
	Name         string `json:"name"`
	Email        string `json:"email"`
	DepartmentID *int64 `json:"department_id"`
}

func toPortalEmployeeDTO(e *store.Employee) portalEmployeeDTO {
	return portalEmployeeDTO{
		ID:           e.ID.String(),
		EmployeeNo:   e.EmployeeNo,
		Name:         e.Name,
		Email:        e.Email,
		DepartmentID: e.DepartmentID,
	}
}

type loginRequest struct {
	EmployeeNo string `json:"employee_no"`
	Password   string `json:"password"`
}

// Login handles POST /portal/api/auth/login.
func (h *authHandler) Login(c *gin.Context) {
	NoStore(c)

	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.EmployeeNo) == "" || req.Password == "" {
		BadRequest(c, "employee_no and password are required")
		return
	}

	sessionToken, csrfToken, employee, account, err := h.svc.Login(
		c.Request.Context(), req.EmployeeNo, req.Password,
		auth.ClientIP(c.Request, h.trustedProxies), c.Request.UserAgent())
	if err != nil {
		switch {
		case errors.Is(err, portalauth.ErrRateLimited), errors.Is(err, portalauth.ErrAccountLocked):
			c.Header("Retry-After", "60")
			Fail(c, http.StatusTooManyRequests, "rate_limited", "too many login attempts")
		case errors.Is(err, portalauth.ErrInvalidCredentials), errors.Is(err, portalauth.ErrAccountDisabled):
			Fail(c, http.StatusUnauthorized, "invalid_credentials", "invalid employee number or password")
		default:
			// A database or limiter failure must never become a login.
			Fail(c, http.StatusServiceUnavailable, "auth_unavailable", "authentication temporarily unavailable")
		}
		return
	}

	maxAge := int(h.svc.TTL() / time.Second)
	setPortalCookie(c, portalauth.SessionCookie, sessionToken, maxAge, true, h.cookieSecure)
	setPortalCookie(c, portalauth.CSRFCookie, csrfToken, maxAge, false, h.cookieSecure)
	OK(c, http.StatusOK, gin.H{
		"employee":             toPortalEmployeeDTO(employee),
		"must_change_password": account.MustChangePassword,
		"expires_in":           maxAge,
	})
}

// Logout handles POST /portal/api/auth/logout.
func (h *authHandler) Logout(c *gin.Context) {
	token, _ := c.Cookie(portalauth.SessionCookie)
	if err := h.svc.Logout(c.Request.Context(), token); err != nil {
		InternalError(c)
		return
	}
	clearPortalCookie(c, portalauth.SessionCookie, true, h.cookieSecure)
	clearPortalCookie(c, portalauth.CSRFCookie, false, h.cookieSecure)
	OK(c, http.StatusOK, gin.H{"logged_out": true})
}

// Me handles GET /portal/api/auth/me.
func (h *authHandler) Me(c *gin.Context) {
	employee := portalauth.CurrentEmployee(c)
	account := portalauth.CurrentAccount(c)
	if employee == nil || account == nil {
		Fail(c, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}
	OK(c, http.StatusOK, gin.H{
		"employee":             toPortalEmployeeDTO(employee),
		"must_change_password": account.MustChangePassword,
	})
}

// Profile handles GET /portal/api/me/profile.
func (h *authHandler) Profile(c *gin.Context) {
	employee := portalauth.CurrentEmployee(c)
	account := portalauth.CurrentAccount(c)
	if employee == nil || account == nil {
		Fail(c, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}
	OK(c, http.StatusOK, gin.H{
		"employee":             toPortalEmployeeDTO(employee),
		"must_change_password": account.MustChangePassword,
	})
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// ChangePassword handles POST /portal/api/auth/change-password. All sessions,
// including this one, are revoked so the employee must log in again.
func (h *authHandler) ChangePassword(c *gin.Context) {
	NoStore(c)

	employee := portalauth.CurrentEmployee(c)
	if employee == nil {
		Fail(c, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}

	var req changePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		BadRequest(c, "invalid JSON body")
		return
	}
	if req.CurrentPassword == "" {
		BadRequest(c, "current_password is required")
		return
	}
	if msg := validatePassword(req.NewPassword, req.CurrentPassword, employee.EmployeeNo); msg != "" {
		BadRequest(c, msg)
		return
	}

	err := h.svc.ChangePassword(c.Request.Context(), employee.ID, req.CurrentPassword, req.NewPassword)
	switch {
	case err == nil:
	case errors.Is(err, portalauth.ErrInvalidCredentials):
		Fail(c, http.StatusBadRequest, "invalid_current_password", "current password is incorrect")
		return
	case errors.Is(err, portalauth.ErrRateLimited), errors.Is(err, portalauth.ErrAccountLocked):
		c.Header("Retry-After", "60")
		Fail(c, http.StatusTooManyRequests, "rate_limited", "too many attempts")
		return
	case errors.Is(err, portalauth.ErrAuthUnavailable):
		// The limiter is a security dependency; never allow unlimited guesses
		// just because Redis is unreachable.
		Fail(c, http.StatusServiceUnavailable, "auth_unavailable", "authentication temporarily unavailable")
		return
	default:
		InternalError(c)
		return
	}

	clearPortalCookie(c, portalauth.SessionCookie, true, h.cookieSecure)
	clearPortalCookie(c, portalauth.CSRFCookie, false, h.cookieSecure)
	OK(c, http.StatusOK, gin.H{"changed": true, "relogin_required": true})
}

func validatePassword(password, current, employeeNo string) string {
	if len(password) < passwordMinLength {
		return "new_password must be at least 10 characters"
	}
	if len(password) > passwordMaxLength {
		return "new_password must be at most 128 characters"
	}
	if strings.TrimSpace(password) == "" {
		return "new_password must not be blank"
	}
	if password == current {
		return "new_password must differ from the current password"
	}
	if strings.EqualFold(password, employeeNo) {
		return "new_password must not be the employee number"
	}
	return ""
}

func setPortalCookie(c *gin.Context, name, value string, maxAge int, httpOnly, secure bool) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     portalauth.CookiePath,
		MaxAge:   maxAge,
		HttpOnly: httpOnly,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	})
}

func clearPortalCookie(c *gin.Context, name string, httpOnly, secure bool) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     portalauth.CookiePath,
		MaxAge:   -1,
		HttpOnly: httpOnly,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	})
}
