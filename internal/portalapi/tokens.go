package portalapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/company/api-key/internal/auth"
	"github.com/company/api-key/internal/crypto"
	"github.com/company/api-key/internal/portalauth"
	"github.com/company/api-key/internal/portalcfg"
	"github.com/company/api-key/internal/store"
)

const (
	tokenNameMin = 2
	tokenNameMax = 40
)

type tokenHandler struct {
	tokens         store.TokenStore
	quotas         store.EmployeeQuotaStore
	auditLogs      store.EmployeeAuditStore
	cfg            *portalcfg.Provider
	clock          func() time.Time
	trustedProxies []string
}

type portalTokenDTO struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	TokenPrefix  string     `json:"token_prefix"`
	TokenLast4   string     `json:"token_last4"`
	QuotaType    string     `json:"quota_type"`
	QuotaPeriod  string     `json:"quota_period"`
	QuotaTotal   *int64     `json:"quota_total"`
	QuotaUsed    int64      `json:"quota_used"`
	RateLimitRPM int        `json:"rate_limit_rpm"`
	Status       int16      `json:"status"`
	ExpiresAt    *time.Time `json:"expires_at"`
	CreatedAt    time.Time  `json:"created_at"`
	LastUsedAt   *time.Time `json:"last_used_at"`
}

func toPortalTokenDTO(t *store.Token) portalTokenDTO {
	return portalTokenDTO{
		ID:           t.ID.String(),
		Name:         t.Name,
		TokenPrefix:  t.TokenPrefix,
		TokenLast4:   t.TokenLast4,
		QuotaType:    t.QuotaType,
		QuotaPeriod:  t.QuotaPeriod,
		QuotaTotal:   t.QuotaTotal,
		QuotaUsed:    t.QuotaUsed,
		RateLimitRPM: t.RateLimitRPM,
		Status:       t.Status,
		ExpiresAt:    t.ExpiresAt,
		CreatedAt:    t.CreatedAt,
		LastUsedAt:   t.LastUsedAt,
	}
}

// List handles GET /portal/api/me/tokens. It returns only masked identifiers;
// the token hash and plaintext never leave the store.
func (h *tokenHandler) List(c *gin.Context) {
	employee := portalauth.CurrentEmployee(c)
	if employee == nil {
		Fail(c, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}

	items, _, err := h.tokens.List(c.Request.Context(), store.TokenFilter{
		EmployeeID: &employee.ID,
		Limit:      100,
	})
	if err != nil {
		InternalError(c)
		return
	}

	out := make([]portalTokenDTO, 0, len(items))
	for i := range items {
		out = append(out, toPortalTokenDTO(&items[i]))
	}
	OK(c, http.StatusOK, gin.H{"items": out})
}

type createTokenPayload struct {
	Name         string `json:"name"`
	QuotaTotal   *int64 `json:"quota_total"`
	RateLimitRPM *int   `json:"rate_limit_rpm"`
}

// Create handles POST /portal/api/me/tokens. The plaintext is returned once.
func (h *tokenHandler) Create(c *gin.Context) {
	employee := portalauth.CurrentEmployee(c)
	if employee == nil {
		Fail(c, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}
	var payload createTokenPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		BadRequest(c, "invalid JSON body")
		return
	}
	payload.Name = strings.TrimSpace(payload.Name)
	if n := utf8.RuneCountInString(payload.Name); n < tokenNameMin || n > tokenNameMax {
		BadRequest(c, "name must be between 2 and 40 characters")
		return
	}
	if payload.QuotaTotal != nil && *payload.QuotaTotal < 0 {
		BadRequest(c, "quota_total must be a non-negative integer")
		return
	}

	settings := h.cfg.Get(c.Request.Context())
	if !settings.SelfServiceEnabled {
		Fail(c, http.StatusForbidden, "self_service_disabled", "self-service key creation is disabled")
		return
	}

	rpm := settings.DefaultQuota.RPM
	if payload.RateLimitRPM != nil {
		rpm = *payload.RateLimitRPM
	}
	if rpm < 1 || rpm > 100000 {
		BadRequest(c, "rate_limit_rpm must be between 1 and 100000")
		return
	}

	now := h.clock()
	var expiresAt *time.Time
	if settings.DefaultQuota.ExpiresDays > 0 {
		expiry := now.AddDate(0, 0, settings.DefaultQuota.ExpiresDays)
		expiresAt = &expiry
	}
	plain, hash, prefix, last4, err := crypto.GenerateToken()
	if err != nil {
		InternalError(c)
		return
	}
	t := &store.Token{
		EmployeeID:   employee.ID,
		Name:         payload.Name,
		TokenHash:    hash,
		TokenPrefix:  prefix,
		TokenLast4:   last4,
		QuotaType:    settings.DefaultQuota.Type,
		QuotaPeriod:  settings.DefaultQuota.Period,
		QuotaTotal:   payload.QuotaTotal,
		RateLimitRPM: rpm,
		Status:       1,
		ExpiresAt:    expiresAt,
	}
	if err := h.tokens.CreateWithLimit(c.Request.Context(), t, settings.MaxActiveTokens, h.clock()); err != nil {
		if errors.Is(err, store.ErrTokenLimitReached) {
			Fail(c, http.StatusConflict, "token_limit_reached", "active key limit reached")
			return
		}
		InternalError(c)
		return
	}
	h.record(c, employee.ID, "token_create", t.ID, map[string]any{
		"name":        t.Name,
		"quota_total": t.QuotaTotal,
	})

	NoStore(c)
	OK(c, http.StatusCreated, gin.H{"token": plain, "item": toPortalTokenDTO(t)})
}

// Rotate handles POST /portal/api/me/tokens/:id/rotate. The old key is
// revoked and the new plaintext is shown exactly once.
func (h *tokenHandler) Rotate(c *gin.Context) {
	employee := portalauth.CurrentEmployee(c)
	if employee == nil {
		Fail(c, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}
	settings := h.cfg.Get(c.Request.Context())
	if !settings.AllowSelfRotate {
		Fail(c, http.StatusForbidden, "self_rotate_disabled", "self-service rotation is disabled")
		return
	}
	old, ok := h.ownedToken(c, employee.ID)
	if !ok {
		return
	}
	now := h.clock()
	plain, hash, prefix, last4, err := crypto.GenerateToken()
	if err != nil {
		InternalError(c)
		return
	}
	fresh := &store.Token{
		EmployeeID:   old.EmployeeID,
		Name:         old.Name,
		TokenHash:    hash,
		TokenPrefix:  prefix,
		TokenLast4:   last4,
		QuotaType:    old.QuotaType,
		QuotaPeriod:  old.QuotaPeriod,
		QuotaTotal:   old.QuotaTotal,
		RateLimitRPM: old.RateLimitRPM,
		IPAllowlist:  append([]string(nil), old.IPAllowlist...),
		Status:       1,
		ExpiresAt:    old.ExpiresAt,
	}
	if err := h.tokens.Rotate(c.Request.Context(), employee.ID, old.ID, fresh, now); err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			Fail(c, http.StatusNotFound, "not_found", "token not found")
		case errors.Is(err, store.ErrTokenExpired):
			Fail(c, http.StatusConflict, "token_expired", "cannot rotate an expired key")
		case errors.Is(err, store.ErrTokenRevoked):
			Fail(c, http.StatusConflict, "token_revoked", "cannot rotate a revoked key")
		default:
			InternalError(c)
		}
		return
	}
	h.record(c, employee.ID, "token_rotate", fresh.ID, map[string]any{
		"rotated_from": old.ID.String(),
		"name":         fresh.Name,
	})

	NoStore(c)
	OK(c, http.StatusOK, gin.H{"token": plain, "item": toPortalTokenDTO(fresh)})
}

// Revoke handles POST /portal/api/me/tokens/:id/revoke.
func (h *tokenHandler) Revoke(c *gin.Context) {
	employee := portalauth.CurrentEmployee(c)
	if employee == nil {
		Fail(c, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}
	settings := h.cfg.Get(c.Request.Context())
	if !settings.AllowSelfRevoke {
		Fail(c, http.StatusForbidden, "self_revoke_disabled", "self-service revocation is disabled")
		return
	}
	t, ok := h.ownedToken(c, employee.ID)
	if !ok {
		return
	}
	t.Status = 0
	if err := h.tokens.Update(c.Request.Context(), t); err != nil {
		InternalError(c)
		return
	}
	h.record(c, employee.ID, "token_revoke", t.ID, map[string]any{"name": t.Name})
	OK(c, http.StatusOK, toPortalTokenDTO(t))
}

// ownedToken loads a token and hides other employees' keys behind a 404.
func (h *tokenHandler) ownedToken(c *gin.Context, employeeID uuid.UUID) (*store.Token, bool) {
	id, ok := portalPathID(c)
	if !ok {
		return nil, false
	}
	t, err := h.tokens.Get(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			Fail(c, http.StatusNotFound, "not_found", "token not found")
			return nil, false
		}
		InternalError(c)
		return nil, false
	}
	if t.EmployeeID != employeeID {
		Fail(c, http.StatusNotFound, "not_found", "token not found")
		return nil, false
	}
	return t, true
}

func (h *tokenHandler) record(c *gin.Context, employeeID uuid.UUID, action string, targetID uuid.UUID, detail map[string]any) {
	if h.auditLogs == nil {
		return
	}
	payload := []byte("{}")
	if detail != nil {
		if encoded, err := json.Marshal(detail); err == nil {
			payload = encoded
		}
	}
	_ = h.auditLogs.Insert(c.Request.Context(), &store.EmployeeAuditLog{
		EmployeeID: employeeID,
		Action:     action,
		TargetType: "token",
		TargetID:   targetID.String(),
		Detail:     payload,
		ClientIP:   auth.ClientIP(c.Request, h.trustedProxies),
	})
}
