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
	requestReasonMin = 10
	requestReasonMax = 500
	requestListLimit = 50
)

type requestHandler struct {
	requests       store.TokenRequestStore
	quotas         store.EmployeeQuotaStore
	auditLogs      store.EmployeeAuditStore
	cfg            *portalcfg.Provider
	clock          func() time.Time
	trustedProxies []string
}

type portalRequestDTO struct {
	ID                 string     `json:"id"`
	Kind               string     `json:"kind"`
	Reason             string     `json:"reason"`
	RequestedTotal     *int64     `json:"requested_total"`
	Status             string     `json:"status"`
	ReviewNote         string     `json:"review_note"`
	ReviewedAt         *time.Time `json:"reviewed_at"`
	GrantedQuotaType   *string    `json:"granted_quota_type"`
	GrantedQuotaPeriod *string    `json:"granted_quota_period"`
	GrantedTotal       *int64     `json:"granted_total"`
	GrantedRPM         *int       `json:"granted_rpm"`
	GrantedExpiresAt   *time.Time `json:"granted_expires_at"`
	ExpiresAt          time.Time  `json:"expires_at"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
	CanCancel          bool       `json:"can_cancel"`
	CanClaim           bool       `json:"can_claim"`
}

func toPortalRequestDTO(r *store.TokenRequest, now time.Time) portalRequestDTO {
	kind := r.Kind
	if kind == "" {
		kind = "token"
	}
	return portalRequestDTO{
		ID:                 r.ID.String(),
		Kind:               kind,
		Reason:             r.Reason,
		RequestedTotal:     r.RequestedTotal,
		Status:             r.Status,
		ReviewNote:         r.ReviewNote,
		ReviewedAt:         r.ReviewedAt,
		GrantedQuotaType:   r.GrantedQuotaType,
		GrantedQuotaPeriod: r.GrantedQuotaPeriod,
		GrantedTotal:       r.GrantedTotal,
		GrantedRPM:         r.GrantedRPM,
		GrantedExpiresAt:   r.GrantedExpiresAt,
		ExpiresAt:          r.ExpiresAt,
		CreatedAt:          r.CreatedAt,
		UpdatedAt:          r.UpdatedAt,
		CanCancel:          r.Status == "pending",
		CanClaim:           kind == "token" && r.Status == "approved" && r.ExpiresAt.After(now),
	}
}

// List handles GET /portal/api/me/token-requests.
func (h *requestHandler) List(c *gin.Context) {
	employee := portalauth.CurrentEmployee(c)
	if employee == nil {
		Fail(c, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}
	now := h.clock()
	// Keep stale statuses out of the employee view; the partial unique index
	// is also swept on create, so this is best effort.
	_ = h.requests.ExpireStale(c.Request.Context(), employee.ID, now)

	items, err := h.requests.ListByEmployee(c.Request.Context(), employee.ID, requestListLimit)
	if err != nil {
		InternalError(c)
		return
	}
	out := make([]portalRequestDTO, 0, len(items))
	for i := range items {
		out = append(out, toPortalRequestDTO(&items[i], now))
	}
	OK(c, http.StatusOK, gin.H{"items": out})
}

type createRequestPayload struct {
	Reason         string `json:"reason"`
	RequestedTotal *int64 `json:"requested_total"`
}

// Create handles POST /portal/api/me/token-requests.
func (h *requestHandler) Create(c *gin.Context) {
	employee := portalauth.CurrentEmployee(c)
	if employee == nil {
		Fail(c, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}

	var payload createRequestPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		BadRequest(c, "invalid JSON body")
		return
	}
	payload.Reason = strings.TrimSpace(payload.Reason)
	if n := utf8.RuneCountInString(payload.Reason); n < requestReasonMin || n > requestReasonMax {
		BadRequest(c, "reason must be between 10 and 500 characters")
		return
	}

	settings := h.cfg.Get(c.Request.Context())
	if h.quotas == nil {
		InternalError(c)
		return
	}
	current, err := h.quotas.Ensure(c.Request.Context(), employee.ID, store.EmployeeQuotaDefaults{
		Type:   settings.DefaultQuota.Type,
		Period: settings.DefaultQuota.Period,
		Total:  settings.DefaultQuota.Total,
	})
	if err != nil {
		InternalError(c)
		return
	}
	if payload.RequestedTotal == nil {
		BadRequest(c, "requested_total is required")
		return
	}
	requested := *payload.RequestedTotal
	if requested <= current.QuotaTotal {
		BadRequest(c, "requested_total must exceed the current quota")
		return
	}
	if requested > settings.MaxRequestTotal {
		BadRequest(c, "requested_total exceeds the policy limit")
		return
	}

	now := h.clock()
	req := &store.TokenRequest{
		EmployeeID:     employee.ID,
		Kind:           "quota",
		Reason:         payload.Reason,
		RequestedTotal: &requested,
		ExpiresAt:      now.AddDate(0, 0, settings.RequestTTLDays),
	}
	if err := h.requests.Create(c.Request.Context(), req); err != nil {
		if errors.Is(err, store.ErrConflict) {
			Fail(c, http.StatusConflict, "active_request_exists", "an active request already exists")
			return
		}
		InternalError(c)
		return
	}

	if settings.AutoApproveQuota {
		granted, err := h.requests.ApproveQuota(c.Request.Context(), req.ID, nil, requested, "", "auto-approved by policy", now)
		if err == nil {
			h.record(c, employee.ID, "quota_request_auto_approved", "token_request", req.ID.String(), map[string]any{
				"requested_total": requested,
			})
			OK(c, http.StatusCreated, toPortalRequestDTO(granted, now))
			return
		}
		// Fall back to the normal pending flow rather than losing the request.
	}

	h.record(c, employee.ID, "quota_request_create", "token_request", req.ID.String(), map[string]any{
		"requested_total": requested,
		"current_total":   current.QuotaTotal,
	})
	OK(c, http.StatusCreated, toPortalRequestDTO(req, now))
}

// Cancel handles POST /portal/api/me/token-requests/:id/cancel.
func (h *requestHandler) Cancel(c *gin.Context) {
	employee := portalauth.CurrentEmployee(c)
	if employee == nil {
		Fail(c, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}
	id, ok := portalPathID(c)
	if !ok {
		return
	}

	now := h.clock()
	req, err := h.requests.Cancel(c.Request.Context(), id, employee.ID, now)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			Fail(c, http.StatusNotFound, "not_found", "request not found")
		case errors.Is(err, store.ErrConflict):
			Fail(c, http.StatusConflict, "not_cancellable", "request is no longer pending")
		default:
			InternalError(c)
		}
		return
	}
	h.record(c, employee.ID, "token_request_cancel", "token_request", req.ID.String(), nil)
	OK(c, http.StatusOK, toPortalRequestDTO(req, now))
}

// Claim handles POST /portal/api/me/token-requests/:id/claim. The plaintext
// token is generated here and returned exactly once; only its hash is stored.
func (h *requestHandler) Claim(c *gin.Context) {
	employee := portalauth.CurrentEmployee(c)
	if employee == nil {
		Fail(c, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}
	id, ok := portalPathID(c)
	if !ok {
		return
	}

	now := h.clock()
	req, err := h.requests.Get(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			Fail(c, http.StatusNotFound, "not_found", "request not found")
			return
		}
		InternalError(c)
		return
	}
	if req.EmployeeID != employee.ID {
		// Do not reveal another employee's request.
		Fail(c, http.StatusNotFound, "not_found", "request not found")
		return
	}
	if req.Kind == "quota" {
		Fail(c, http.StatusConflict, "not_claimable", "quota requests are granted automatically")
		return
	}
	if req.Status != "approved" {
		Fail(c, http.StatusConflict, "not_claimable", "request is not approved")
		return
	}
	if !req.ExpiresAt.After(now) {
		_ = h.requests.ExpireStale(c.Request.Context(), employee.ID, now)
		Fail(c, http.StatusConflict, "claim_expired", "claim window has expired")
		return
	}
	if req.GrantedQuotaType == nil || req.GrantedQuotaPeriod == nil || req.GrantedRPM == nil {
		InternalError(c)
		return
	}

	plain, hash, prefix, last4, err := crypto.GenerateToken()
	if err != nil {
		InternalError(c)
		return
	}
	token := &store.Token{
		EmployeeID:   employee.ID,
		TokenHash:    hash,
		TokenPrefix:  prefix,
		TokenLast4:   last4,
		QuotaType:    *req.GrantedQuotaType,
		QuotaPeriod:  *req.GrantedQuotaPeriod,
		QuotaTotal:   req.GrantedTotal,
		RateLimitRPM: *req.GrantedRPM,
		Status:       1,
		ExpiresAt:    req.GrantedExpiresAt,
	}

	claimed, err := h.requests.Claim(c.Request.Context(), id, employee.ID, token,
		auth.ClientIP(c.Request, h.trustedProxies), now)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			Fail(c, http.StatusNotFound, "not_found", "request not found")
		case errors.Is(err, store.ErrRequestExpired):
			Fail(c, http.StatusConflict, "claim_expired", "claim window has expired")
		case errors.Is(err, store.ErrConflict):
			Fail(c, http.StatusConflict, "not_claimable", "request was already claimed")
		default:
			InternalError(c)
		}
		return
	}

	NoStore(c)
	OK(c, http.StatusOK, gin.H{
		"token":        plain,
		"token_id":     token.ID.String(),
		"token_prefix": token.TokenPrefix,
		"token_last4":  token.TokenLast4,
		"request":      toPortalRequestDTO(claimed, now),
	})
}

// record writes a best-effort employee audit row. The workflow row itself is
// the authoritative record; audit I/O must not fail the employee action.
func (h *requestHandler) record(c *gin.Context, employeeID uuid.UUID, action, targetType, targetID string, detail map[string]any) {
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
		TargetType: targetType,
		TargetID:   targetID,
		Detail:     payload,
		ClientIP:   auth.ClientIP(c.Request, h.trustedProxies),
	})
}

func portalPathID(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		BadRequest(c, "invalid id")
		return uuid.Nil, false
	}
	return id, true
}
