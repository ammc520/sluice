// Package portalapi serves the employee self-service portal. Every route is
// scoped by the authenticated employee; the API never accepts an employee_id
// from the client.
package portalapi

import (
	"time"

	"github.com/gin-gonic/gin"

	"github.com/company/sluice/internal/portalauth"
	"github.com/company/sluice/internal/portalcfg"
	"github.com/company/sluice/internal/store"
)

// Dependencies are the portal's runtime collaborators.
type Dependencies struct {
	Auth         *portalauth.Service
	Tokens       store.TokenStore
	Quotas       store.EmployeeQuotaStore
	Requests     store.TokenRequestStore
	Audit        store.AuditStore
	AuditLogs    store.EmployeeAuditStore
	Config         *portalcfg.Provider
	Clock          func() time.Time
	CookieSecure   bool
	TrustedProxies []string
}

// Register mounts /portal/api routes on the supplied group.
func Register(group *gin.RouterGroup, deps Dependencies) {
	clock := deps.Clock
	if clock == nil {
		clock = time.Now
	}

	auth := &authHandler{
		svc:            deps.Auth,
		cookieSecure:   deps.CookieSecure,
		trustedProxies: deps.TrustedProxies,
	}
	usage := &usageHandler{audit: deps.Audit, cfg: deps.Config, clock: clock}
	tokens := &tokenHandler{
		tokens:         deps.Tokens,
		auditLogs:      deps.AuditLogs,
		cfg:            deps.Config,
		clock:          clock,
		trustedProxies: deps.TrustedProxies,
	}
	quotas := &quotaHandler{
		quotas: deps.Quotas,
		tokens: deps.Tokens,
		cfg:    deps.Config,
		clock:  clock,
	}
	requests := &requestHandler{
		requests:       deps.Requests,
		quotas:         deps.Quotas,
		auditLogs:      deps.AuditLogs,
		cfg:            deps.Config,
		clock:          clock,
		trustedProxies: deps.TrustedProxies,
	}

	// Disabled portal: every route (including login) is indistinguishable
	// from a missing route.
	group.Use(portalauth.RequirePortalEnabled(deps.Config))

	group.POST("/auth/login", auth.Login)

	authed := group.Group("", portalauth.RequireSession(deps.Auth))
	authed.GET("/auth/me", auth.Me)
	authed.POST("/auth/logout", portalauth.RequireCSRF(), auth.Logout)
	authed.POST("/auth/change-password", portalauth.RequireCSRF(), auth.ChangePassword)

	// Data endpoints require a replaced initial password.
	me := authed.Group("/me", portalauth.RequireCSRF(), portalauth.RequirePasswordChanged())
	me.GET("/profile", auth.Profile)
	me.GET("/summary", usage.Summary)
	me.GET("/usage", usage.List)
	me.GET("/quota", quotas.Get)
	me.GET("/tokens", tokens.List)
	me.POST("/tokens", tokens.Create)
	me.POST("/tokens/:id/rotate", tokens.Rotate)
	me.POST("/tokens/:id/revoke", tokens.Revoke)
	me.GET("/token-requests", requests.List)
	me.POST("/token-requests", requests.Create)
	me.POST("/token-requests/:id/cancel", requests.Cancel)
	me.POST("/token-requests/:id/claim", requests.Claim)
}
