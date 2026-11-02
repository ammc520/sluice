package adminapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/company/api-key/internal/adminauth"
	"github.com/company/api-key/internal/store"
)

// AuthHandler exposes administrator login/logout/me endpoints.
type AuthHandler struct {
	svc          *adminauth.Service
	cookieSecure bool
}

// NewAuthHandler creates the auth handler.
func NewAuthHandler(svc *adminauth.Service, cookieSecure bool) *AuthHandler {
	return &AuthHandler{svc: svc, cookieSecure: cookieSecure}
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type adminUserDTO struct {
	ID          string     `json:"id"`
	Username    string     `json:"username"`
	Role        string     `json:"role"`
	Status      int16      `json:"status"`
	LastLoginAt *time.Time `json:"last_login_at,omitempty"`
}

func toAdminUserDTO(u *store.AdminUser) adminUserDTO {
	return adminUserDTO{ID: u.ID.String(), Username: u.Username, Role: u.Role, Status: u.Status, LastLoginAt: u.LastLoginAt}
}

// Login handles POST /admin/api/auth/login.
func (h *AuthHandler) Login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Username == "" || req.Password == "" {
		BadRequest(c, "username and password are required")
		return
	}

	sessionToken, csrfToken, user, err := h.svc.Login(c.Request.Context(), req.Username, req.Password, c.ClientIP())
	if err != nil {
		switch {
		case errors.Is(err, adminauth.ErrRateLimited):
			c.Header("Retry-After", "60")
			Fail(c, http.StatusTooManyRequests, "rate_limited", "too many login attempts")
		case errors.Is(err, adminauth.ErrInvalidCredentials), errors.Is(err, adminauth.ErrAccountDisabled):
			Fail(c, http.StatusUnauthorized, "invalid_credentials", "invalid username or password")
		case errors.Is(err, adminauth.ErrAuthUnavailable):
			Fail(c, http.StatusServiceUnavailable, "auth_unavailable", "authentication temporarily unavailable")
		default:
			InternalError(c)
		}
		return
	}

	maxAge := int(h.svc.TTL() / time.Second)
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     adminauth.SessionCookie,
		Value:    sessionToken,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   h.cookieSecure,
		SameSite: http.SameSiteStrictMode,
	})
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     adminauth.CSRFCookie,
		Value:    csrfToken,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: false,
		Secure:   h.cookieSecure,
		SameSite: http.SameSiteStrictMode,
	})
	OK(c, http.StatusOK, gin.H{"user": toAdminUserDTO(user), "expires_in": maxAge})
}

// Logout handles POST /admin/api/auth/logout.
func (h *AuthHandler) Logout(c *gin.Context) {
	token, _ := c.Cookie(adminauth.SessionCookie)
	if err := h.svc.Logout(c.Request.Context(), token); err != nil {
		InternalError(c)
		return
	}
	clearCookie(c, adminauth.SessionCookie, h.cookieSecure, true)
	clearCookie(c, adminauth.CSRFCookie, h.cookieSecure, false)
	OK(c, http.StatusOK, gin.H{"logged_out": true})
}

// Me handles GET /admin/api/auth/me.
func (h *AuthHandler) Me(c *gin.Context) {
	user := adminauth.CurrentUser(c)
	if user == nil {
		Fail(c, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}
	OK(c, http.StatusOK, gin.H{"user": toAdminUserDTO(user)})
}

func clearCookie(c *gin.Context, name string, secure, httpOnly bool) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: httpOnly,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	})
}
