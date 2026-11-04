package adminapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/company/api-key/internal/adminauth"
	"github.com/company/api-key/internal/auth"
	"github.com/company/api-key/internal/store"
)

// AuthHandler exposes administrator login/logout/me endpoints.
type AuthHandler struct {
	svc            *adminauth.Service
	cookieSecure   bool
	trustedProxies []string
}

// NewAuthHandler creates the auth handler. trustedProxies is the same CIDR
// list used by the gateway, so login rate limiting cannot be bypassed with a
// forged X-Forwarded-For header.
func NewAuthHandler(svc *adminauth.Service, cookieSecure bool, trustedProxies []string) *AuthHandler {
	return &AuthHandler{svc: svc, cookieSecure: cookieSecure, trustedProxies: trustedProxies}
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
	CreatedAt   time.Time  `json:"created_at"`
}

func toAdminUserDTO(u *store.AdminUser) adminUserDTO {
	return adminUserDTO{
		ID:          u.ID.String(),
		Username:    u.Username,
		Role:        u.Role,
		Status:      u.Status,
		LastLoginAt: u.LastLoginAt,
		CreatedAt:   u.CreatedAt,
	}
}

// Login handles POST /admin/api/auth/login.
func (h *AuthHandler) Login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Username == "" || req.Password == "" {
		BadRequest(c, "username and password are required")
		return
	}

	sessionToken, csrfToken, user, err := h.svc.Login(c.Request.Context(), req.Username, req.Password,
		auth.ClientIP(c.Request, h.trustedProxies))
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
	// Expire pre-migration root-scoped cookies first. Browsers send the
	// longer path first, but leaving a stale duplicate behind makes logout
	// and session validation ambiguous.
	clearLegacyCookies(c, h.cookieSecure)
	setCookie(c, adminauth.SessionCookie, sessionToken, adminauth.SessionCookiePath, maxAge, true, h.cookieSecure)
	setCookie(c, adminauth.CSRFCookie, csrfToken, adminauth.CSRFCookiePath, maxAge, false, h.cookieSecure)
	OK(c, http.StatusOK, gin.H{"user": toAdminUserDTO(user), "expires_in": maxAge})
}

// Logout handles POST /admin/api/auth/logout.
func (h *AuthHandler) Logout(c *gin.Context) {
	token, _ := c.Cookie(adminauth.SessionCookie)
	if err := h.svc.Logout(c.Request.Context(), token); err != nil {
		InternalError(c)
		return
	}
	clearCookie(c, adminauth.SessionCookie, adminauth.SessionCookiePath, h.cookieSecure, true)
	clearCookie(c, adminauth.CSRFCookie, adminauth.CSRFCookiePath, h.cookieSecure, false)
	clearLegacyCookies(c, h.cookieSecure)
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

func setCookie(c *gin.Context, name, value, path string, maxAge int, httpOnly, secure bool) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     path,
		MaxAge:   maxAge,
		HttpOnly: httpOnly,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	})
}

func clearCookie(c *gin.Context, name, path string, secure, httpOnly bool) {
	setCookie(c, name, "", path, -1, httpOnly, secure)
}

// clearLegacyCookies expires the root-scoped cookies written before admin
// routes moved under /admin.
func clearLegacyCookies(c *gin.Context, secure bool) {
	clearCookie(c, adminauth.SessionCookie, adminauth.LegacyCookiePath, secure, true)
	clearCookie(c, adminauth.CSRFCookie, adminauth.LegacyCookiePath, secure, false)
}
