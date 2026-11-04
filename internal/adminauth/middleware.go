package adminauth

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/company/api-key/internal/store"
)

const (
	// ContextUser holds *store.AdminUser in the Gin context.
	ContextUser = "adminauth.user"
	// ContextSession holds *store.AdminSession in the Gin context.
	ContextSession = "adminauth.session"

	SessionCookie = "admin_session"
	CSRFCookie    = "csrf_token"
	CSRFHeader    = "X-CSRF-Token"

	// SessionCookiePath keeps the administrator session away from /portal,
	// /v1 and the admin SPA's static assets.
	SessionCookiePath = "/admin/api"
	// CSRFCookiePath must cover the admin SPA (which reads the token with
	// document.cookie) as well as every /admin/api request that echoes it.
	CSRFCookiePath = "/admin"
	// LegacyCookiePath is the pre-migration scope. Login and logout expire
	// cookies left under it so a stale root-scoped session cannot shadow the
	// narrowly scoped one.
	LegacyCookiePath = "/"
)

// RequireSession rejects requests without a valid session cookie.
func RequireSession(svc *Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, err := c.Cookie(SessionCookie)
		if err != nil {
			abortUnauthorized(c)
			return
		}
		user, sess, err := svc.Validate(c.Request.Context(), token)
		if err != nil {
			abortUnauthorized(c)
			return
		}
		c.Set(ContextUser, user)
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
		v, ok := c.Get(ContextSession)
		if !ok {
			abortForbidden(c, "csrf_failed", "invalid CSRF token")
			return
		}
		sess, ok := v.(*store.AdminSession)
		if !ok || !ValidateCSRF(sess, c.GetHeader(CSRFHeader)) {
			abortForbidden(c, "csrf_failed", "invalid CSRF token")
			return
		}
		c.Next()
	}
}

// RequireRole enforces an administrator role. super_admin passes every
// non-super-admin role check.
func RequireRole(role string) gin.HandlerFunc {
	return func(c *gin.Context) {
		v, ok := c.Get(ContextUser)
		if !ok {
			abortUnauthorized(c)
			return
		}
		user, ok := v.(*store.AdminUser)
		if !ok {
			abortUnauthorized(c)
			return
		}
		if user.Role == role || (role != "super_admin" && user.Role == "super_admin") {
			c.Next()
			return
		}
		abortForbidden(c, "forbidden", "insufficient role")
	}
}

// CurrentUser returns the authenticated administrator.
func CurrentUser(c *gin.Context) *store.AdminUser {
	v, ok := c.Get(ContextUser)
	if !ok {
		return nil
	}
	u, _ := v.(*store.AdminUser)
	return u
}

// CurrentSession returns the authenticated session.
func CurrentSession(c *gin.Context) *store.AdminSession {
	v, ok := c.Get(ContextSession)
	if !ok {
		return nil
	}
	s, _ := v.(*store.AdminSession)
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
