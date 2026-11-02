package adminapi

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/company/api-key/internal/crypto"
	"github.com/company/api-key/internal/store"
)

// TokenHandler serves employee token administration endpoints.
type TokenHandler struct {
	tokens    store.TokenStore
	employees store.EmployeeStore
	clock     func() time.Time
}

// NewTokenHandler creates the token handler.
func NewTokenHandler(tokens store.TokenStore, employees store.EmployeeStore, clock func() time.Time) *TokenHandler {
	if clock == nil {
		clock = time.Now
	}
	return &TokenHandler{tokens: tokens, employees: employees, clock: clock}
}

type tokenDTO struct {
	ID           string     `json:"id"`
	EmployeeID   string     `json:"employee_id"`
	TokenPrefix  string     `json:"token_prefix"`
	TokenLast4   string     `json:"token_last4"`
	QuotaType    string     `json:"quota_type"`
	QuotaPeriod  string     `json:"quota_period"`
	QuotaTotal   *int64     `json:"quota_total"`
	QuotaUsed    int64      `json:"quota_used"`
	PeriodStart  *time.Time `json:"period_start"`
	RateLimitRPM int        `json:"rate_limit_rpm"`
	IPAllowlist  []string   `json:"ip_allowlist"`
	Status       int16      `json:"status"`
	ExpiresAt    *time.Time `json:"expires_at"`
	CreatedAt    time.Time  `json:"created_at"`
	LastUsedAt   *time.Time `json:"last_used_at"`
}

func toTokenDTO(t *store.Token) tokenDTO {
	ips := t.IPAllowlist
	if ips == nil {
		ips = []string{}
	}
	return tokenDTO{
		ID:           t.ID.String(),
		EmployeeID:   t.EmployeeID.String(),
		TokenPrefix:  t.TokenPrefix,
		TokenLast4:   t.TokenLast4,
		QuotaType:    t.QuotaType,
		QuotaPeriod:  t.QuotaPeriod,
		QuotaTotal:   t.QuotaTotal,
		QuotaUsed:    t.QuotaUsed,
		PeriodStart:  t.PeriodStart,
		RateLimitRPM: t.RateLimitRPM,
		IPAllowlist:  ips,
		Status:       t.Status,
		ExpiresAt:    t.ExpiresAt,
		CreatedAt:    t.CreatedAt,
		LastUsedAt:   t.LastUsedAt,
	}
}

type tokenPayload struct {
	EmployeeID   string   `json:"employee_id"`
	QuotaType    string   `json:"quota_type"`
	QuotaPeriod  string   `json:"quota_period"`
	QuotaTotal   *int64   `json:"quota_total"`
	RateLimitRPM int      `json:"rate_limit_rpm"`
	ExpiresAt    *string  `json:"expires_at"`
	IPAllowlist  []string `json:"ip_allowlist"`
}

// normalize validates the payload, applies defaults, and returns the parsed
// expiration time.
func (p *tokenPayload) normalize(now time.Time) (*time.Time, string) {
	p.QuotaType = strings.TrimSpace(p.QuotaType)
	if p.QuotaType == "" {
		p.QuotaType = "tokens"
	}
	if p.QuotaType != "requests" && p.QuotaType != "tokens" {
		return nil, "quota_type must be requests or tokens"
	}

	p.QuotaPeriod = strings.TrimSpace(p.QuotaPeriod)
	if p.QuotaPeriod == "" {
		p.QuotaPeriod = "monthly"
	}
	switch p.QuotaPeriod {
	case "daily", "monthly", "total":
	default:
		return nil, "quota_period must be daily, monthly, or total"
	}

	if p.QuotaTotal != nil && *p.QuotaTotal < 0 {
		return nil, "quota_total must be non-negative"
	}
	if p.RateLimitRPM == 0 {
		p.RateLimitRPM = 60
	}
	if p.RateLimitRPM < 0 || p.RateLimitRPM > 100000 {
		return nil, "rate_limit_rpm must be between 1 and 100000"
	}

	ips := make([]string, 0, len(p.IPAllowlist))
	for _, raw := range p.IPAllowlist {
		ip := strings.TrimSpace(raw)
		if ip == "" {
			return nil, "ip_allowlist entries must not be empty"
		}
		if net.ParseIP(ip) == nil {
			if _, _, err := net.ParseCIDR(ip); err != nil {
				return nil, "invalid IP or CIDR in ip_allowlist: " + ip
			}
		}
		ips = append(ips, ip)
	}
	if len(ips) > 100 {
		return nil, "ip_allowlist supports at most 100 entries"
	}
	p.IPAllowlist = ips

	var expiresAt *time.Time
	if p.ExpiresAt != nil && strings.TrimSpace(*p.ExpiresAt) != "" {
		t, err := parseAPITime(strings.TrimSpace(*p.ExpiresAt))
		if err != nil {
			return nil, "expires_at must be an RFC3339 timestamp"
		}
		if !t.After(now) {
			return nil, "expires_at must be in the future"
		}
		expiresAt = &t
	}
	return expiresAt, ""
}

func parseAPITime(raw string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t, nil
	}
	for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, raw, shanghaiLocation()); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported time format")
}

var shanghaiLoc = func() *time.Location {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return time.FixedZone("CST", 8*3600)
	}
	return loc
}()

func shanghaiLocation() *time.Location { return shanghaiLoc }

// List handles GET /admin/api/tokens.
func (h *TokenHandler) List(c *gin.Context) {
	page, pageSize := pagination(c)
	filter := store.TokenFilter{
		Limit:  pageSize,
		Offset: (page - 1) * pageSize,
	}
	employeeID, ok := queryUUID(c, "employee_id")
	if !ok {
		return
	}
	filter.EmployeeID = employeeID

	if raw := strings.TrimSpace(c.Query("status")); raw != "" {
		status, err := strconv.ParseInt(raw, 10, 16)
		if err != nil || (status != 0 && status != 1) {
			BadRequest(c, "status must be 0 or 1")
			return
		}
		s := int16(status)
		filter.Status = &s
	}

	items, total, err := h.tokens.List(c.Request.Context(), filter)
	if err != nil {
		InternalError(c)
		return
	}
	out := make([]tokenDTO, 0, len(items))
	for i := range items {
		out = append(out, toTokenDTO(&items[i]))
	}
	OK(c, http.StatusOK, pageResponse(out, total, page, pageSize))
}

// Create handles POST /admin/api/tokens. The plaintext token is returned
// exactly once, in this response.
func (h *TokenHandler) Create(c *gin.Context) {
	var payload tokenPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		BadRequest(c, "invalid JSON body")
		return
	}
	employeeID, err := uuid.Parse(strings.TrimSpace(payload.EmployeeID))
	if err != nil {
		BadRequest(c, "employee_id is required")
		return
	}

	now := h.clock()
	expiresAt, msg := payload.normalize(now)
	if msg != "" {
		BadRequest(c, msg)
		return
	}

	ctx := c.Request.Context()
	if _, err := h.employees.Get(ctx, employeeID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			BadRequest(c, "employee not found")
			return
		}
		InternalError(c)
		return
	}

	plain, hash, prefix, last4, err := crypto.GenerateToken()
	if err != nil {
		InternalError(c)
		return
	}
	t := &store.Token{
		EmployeeID:   employeeID,
		TokenHash:    hash,
		TokenPrefix:  prefix,
		TokenLast4:   last4,
		QuotaType:    payload.QuotaType,
		QuotaPeriod:  payload.QuotaPeriod,
		QuotaTotal:   payload.QuotaTotal,
		RateLimitRPM: payload.RateLimitRPM,
		IPAllowlist:  payload.IPAllowlist,
		Status:       1,
		ExpiresAt:    expiresAt,
	}
	if err := h.tokens.Create(ctx, t); err != nil {
		writeStoreError(c, err, "employee not found", "token conflict", "employee not found")
		return
	}
	OK(c, http.StatusCreated, gin.H{"token": plain, "token_id": t.ID.String()})
}

// Update handles PUT /admin/api/tokens/:id. It never changes the secret.
func (h *TokenHandler) Update(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var payload tokenPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		BadRequest(c, "invalid JSON body")
		return
	}
	now := h.clock()
	expiresAt, msg := payload.normalize(now)
	if msg != "" {
		BadRequest(c, msg)
		return
	}

	ctx := c.Request.Context()
	t, err := h.tokens.Get(ctx, id)
	if err != nil {
		writeStoreError(c, err, "token not found", "token conflict", "employee not found")
		return
	}
	t.QuotaType = payload.QuotaType
	t.QuotaPeriod = payload.QuotaPeriod
	t.QuotaTotal = payload.QuotaTotal
	t.RateLimitRPM = payload.RateLimitRPM
	t.IPAllowlist = payload.IPAllowlist
	t.ExpiresAt = expiresAt
	if err := h.tokens.Update(ctx, t); err != nil {
		writeStoreError(c, err, "token not found", "token conflict", "employee not found")
		return
	}
	OK(c, http.StatusOK, toTokenDTO(t))
}

// Revoke handles POST /admin/api/tokens/:id/revoke.
func (h *TokenHandler) Revoke(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	t, err := h.tokens.Get(ctx, id)
	if err != nil {
		writeStoreError(c, err, "token not found", "token conflict", "employee not found")
		return
	}
	t.Status = 0
	if err := h.tokens.Update(ctx, t); err != nil {
		writeStoreError(c, err, "token not found", "token conflict", "employee not found")
		return
	}
	OK(c, http.StatusOK, toTokenDTO(t))
}

// Rotate handles POST /admin/api/tokens/:id/rotate. The old token is revoked
// and a new plaintext token is returned exactly once.
func (h *TokenHandler) Rotate(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	old, err := h.tokens.Get(ctx, id)
	if err != nil {
		writeStoreError(c, err, "token not found", "token conflict", "employee not found")
		return
	}
	now := h.clock()
	if old.ExpiresAt != nil && !old.ExpiresAt.After(now) {
		BadRequest(c, "cannot rotate an expired token; set a new expiration first")
		return
	}

	plain, hash, prefix, last4, err := crypto.GenerateToken()
	if err != nil {
		InternalError(c)
		return
	}
	fresh := &store.Token{
		EmployeeID:   old.EmployeeID,
		TokenHash:    hash,
		TokenPrefix:  prefix,
		TokenLast4:   last4,
		QuotaType:    old.QuotaType,
		QuotaPeriod:  old.QuotaPeriod,
		QuotaTotal:   old.QuotaTotal,
		QuotaUsed:    0,
		RateLimitRPM: old.RateLimitRPM,
		IPAllowlist:  append([]string(nil), old.IPAllowlist...),
		Status:       1,
		ExpiresAt:    old.ExpiresAt,
	}
	if err := h.tokens.Create(ctx, fresh); err != nil {
		writeStoreError(c, err, "employee not found", "token conflict", "employee not found")
		return
	}

	old.Status = 0
	if err := h.tokens.Update(ctx, old); err != nil {
		// Leave no active token behind if the revocation failed.
		fresh.Status = 0
		_ = h.tokens.Update(ctx, fresh)
		InternalError(c)
		return
	}
	OK(c, http.StatusOK, gin.H{"token": plain, "token_id": fresh.ID.String()})
}
