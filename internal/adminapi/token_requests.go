package adminapi

import (
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"github.com/company/api-key/internal/adminauth"
	"github.com/company/api-key/internal/portalcfg"
	"github.com/company/api-key/internal/store"
)

// TokenRequestHandler serves the administrator side of the portal approval
// workflow.
type TokenRequestHandler struct {
	requests       store.TokenRequestStore
	admins         store.AdminStore
	cfg            *portalcfg.Provider
	clock          func() time.Time
	trustedProxies []string
}

// NewTokenRequestHandler creates the approval handler.
func NewTokenRequestHandler(requests store.TokenRequestStore, admins store.AdminStore, cfg *portalcfg.Provider, clock func() time.Time, trustedProxies []string) *TokenRequestHandler {
	if clock == nil {
		clock = time.Now
	}
	return &TokenRequestHandler{
		requests:       requests,
		admins:         admins,
		cfg:            cfg,
		clock:          clock,
		trustedProxies: append([]string(nil), trustedProxies...),
	}
}

type adminTokenRequestDTO struct {
	ID                 string     `json:"id"`
	Kind               string     `json:"kind"`
	EmployeeID         string     `json:"employee_id"`
	EmployeeNo         string     `json:"employee_no"`
	EmployeeName       string     `json:"employee_name"`
	DepartmentName     string     `json:"department_name"`
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
	TokenID            *string    `json:"token_id"`
	ExpiresAt          time.Time  `json:"expires_at"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

func toAdminTokenRequestDTO(r *store.TokenRequest) adminTokenRequestDTO {
	var tokenID *string
	if r.TokenID != nil {
		v := r.TokenID.String()
		tokenID = &v
	}
	kind := r.Kind
	if kind == "" {
		kind = "token"
	}
	return adminTokenRequestDTO{
		ID:                 r.ID.String(),
		Kind:               kind,
		EmployeeID:         r.EmployeeID.String(),
		EmployeeNo:         r.EmployeeNo,
		EmployeeName:       r.EmployeeName,
		DepartmentName:     r.DepartmentName,
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
		TokenID:            tokenID,
		ExpiresAt:          r.ExpiresAt,
		CreatedAt:          r.CreatedAt,
		UpdatedAt:          r.UpdatedAt,
	}
}

// List handles GET /admin/api/token-requests.
func (h *TokenRequestHandler) List(c *gin.Context) {
	page, pageSize := pagination(c)
	status := strings.TrimSpace(c.Query("status"))
	if status != "" && !validRequestStatus(status) {
		BadRequest(c, "invalid status")
		return
	}
	filter := store.TokenRequestFilter{
		Status: status,
		Query:  strings.TrimSpace(c.Query("q")),
		Limit:  pageSize,
		Offset: (page - 1) * pageSize,
	}
	if id, ok := queryUUID(c, "employee_id"); !ok {
		return
	} else {
		filter.EmployeeID = id
	}

	items, total, err := h.requests.List(c.Request.Context(), filter)
	if err != nil {
		InternalError(c)
		return
	}
	out := make([]adminTokenRequestDTO, 0, len(items))
	for i := range items {
		out = append(out, toAdminTokenRequestDTO(&items[i]))
	}
	OK(c, http.StatusOK, pageResponse(out, total, page, pageSize))
}

type approveRequestPayload struct {
	QuotaType    string  `json:"quota_type"`
	QuotaPeriod  string  `json:"quota_period"`
	QuotaTotal   *int64  `json:"quota_total"`
	RateLimitRPM int     `json:"rate_limit_rpm"`
	ExpiresAt    *string `json:"expires_at"`
	Note         string  `json:"note"`
}

// Approve handles POST /admin/api/token-requests/:id/approve.
func (h *TokenRequestHandler) Approve(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var payload approveRequestPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		BadRequest(c, "invalid JSON body")
		return
	}
	payload.Note = strings.TrimSpace(payload.Note)
	if utf8.RuneCountInString(payload.Note) > 500 {
		BadRequest(c, "note is too long")
		return
	}

	now := h.clock()
	ctx := c.Request.Context()
	pending, err := h.requests.Get(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			Fail(c, http.StatusNotFound, "not_found", "request not found")
			return
		}
		InternalError(c)
		return
	}
	settings := h.cfg.Get(ctx)
	reviewer := adminauth.CurrentUser(c)
	if reviewer == nil {
		Fail(c, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}

	if pending.Kind != "token" {
		// Quota increase: the approved total is the employee's new allowance
		// and applies immediately to every existing key.
		total := settings.MaxRequestTotal
		if pending.RequestedTotal != nil {
			total = *pending.RequestedTotal
		}
		if payload.QuotaTotal != nil {
			total = *payload.QuotaTotal
		}
		if total <= 0 {
			BadRequest(c, "quota_total must be positive")
			return
		}
		if total > settings.MaxRequestTotal {
			BadRequest(c, "quota_total exceeds the portal policy limit")
			return
		}
		period := strings.TrimSpace(payload.QuotaPeriod)
		if period != "" && period != "daily" && period != "monthly" && period != "total" {
			BadRequest(c, "quota_period must be daily, monthly, or total")
			return
		}
		req, err := h.requests.ApproveQuota(ctx, id, &reviewer.ID, total, period, payload.Note, now)
		if err != nil {
			writeApproveError(c, err)
			return
		}
		_ = recordAdminAudit(c, h.admins, h.trustedProxies, "approve_quota_request", "token_request", req.ID.String(), map[string]any{
			"employee_id":  req.EmployeeID.String(),
			"quota_total":  total,
			"quota_period": period,
		})
		OK(c, http.StatusOK, toAdminTokenRequestDTO(req))
		return
	}

	tokenPayload := tokenPayload{
		QuotaType:    payload.QuotaType,
		QuotaPeriod:  payload.QuotaPeriod,
		QuotaTotal:   payload.QuotaTotal,
		RateLimitRPM: payload.RateLimitRPM,
		ExpiresAt:    payload.ExpiresAt,
	}
	expiresAt, msg := tokenPayload.normalize(now)
	if msg != "" {
		BadRequest(c, msg)
		return
	}

	// The grant must carry an expiry even when the reviewer leaves it blank;
	// otherwise a portal-issued token would never expire, ignoring the
	// default_quota.expires_days policy.
	if expiresAt == nil {
		def := now.AddDate(0, 0, settings.DefaultQuota.ExpiresDays)
		expiresAt = &def
	}
	if tokenPayload.QuotaTotal == nil {
		total := settings.DefaultQuota.Total
		if total <= 0 {
			total = settings.MaxRequestTotal
		}
		tokenPayload.QuotaTotal = &total
	}
	if *tokenPayload.QuotaTotal <= 0 {
		BadRequest(c, "quota_total must be positive")
		return
	}
	if *tokenPayload.QuotaTotal > settings.MaxRequestTotal {
		BadRequest(c, "quota_total exceeds the portal policy limit")
		return
	}

	grant := store.TokenRequestGrant{
		QuotaType:     tokenPayload.QuotaType,
		QuotaPeriod:   tokenPayload.QuotaPeriod,
		QuotaTotal:    tokenPayload.QuotaTotal,
		RateLimitRPM:  tokenPayload.RateLimitRPM,
		ExpiresAt:     expiresAt,
		Note:          payload.Note,
		ClaimDeadline: now.Add(time.Duration(settings.ClaimTTLHours) * time.Hour),
	}
	req, err := h.requests.Approve(ctx, id, reviewer.ID, grant, now)
	if err != nil {
		writeApproveError(c, err)
		return
	}

	_ = recordAdminAudit(c, h.admins, h.trustedProxies, "approve_token_request", "token_request", req.ID.String(), map[string]any{
		"employee_id":    req.EmployeeID.String(),
		"quota_type":     grant.QuotaType,
		"quota_period":   grant.QuotaPeriod,
		"quota_total":    grant.QuotaTotal,
		"rate_limit_rpm": grant.RateLimitRPM,
		"expires_at":     grant.ExpiresAt,
	})
	OK(c, http.StatusOK, toAdminTokenRequestDTO(req))
}

func writeApproveError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		Fail(c, http.StatusNotFound, "not_found", "request not found")
	case errors.Is(err, store.ErrConflict):
		Fail(c, http.StatusConflict, "not_pending", "request is no longer pending")
	default:
		InternalError(c)
	}
}

type denyRequestPayload struct {
	Note string `json:"note"`
}

// Deny handles POST /admin/api/token-requests/:id/deny.
func (h *TokenRequestHandler) Deny(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var payload denyRequestPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		BadRequest(c, "invalid JSON body")
		return
	}
	payload.Note = strings.TrimSpace(payload.Note)
	if payload.Note == "" {
		BadRequest(c, "note is required")
		return
	}
	if utf8.RuneCountInString(payload.Note) > 500 {
		BadRequest(c, "note is too long")
		return
	}

	reviewer := adminauth.CurrentUser(c)
	if reviewer == nil {
		Fail(c, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}
	now := h.clock()
	req, err := h.requests.Deny(c.Request.Context(), id, reviewer.ID, payload.Note, now)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			Fail(c, http.StatusNotFound, "not_found", "request not found")
		case errors.Is(err, store.ErrConflict):
			Fail(c, http.StatusConflict, "not_pending", "request is no longer pending")
		default:
			InternalError(c)
		}
		return
	}

	_ = recordAdminAudit(c, h.admins, h.trustedProxies, "deny_token_request", "token_request", req.ID.String(), map[string]any{
		"employee_id": req.EmployeeID.String(),
		"note":        payload.Note,
	})
	OK(c, http.StatusOK, toAdminTokenRequestDTO(req))
}

func validRequestStatus(status string) bool {
	switch status {
	case "pending", "approved", "granted", "denied", "cancelled", "claimed", "expired":
		return true
	}
	return false
}
