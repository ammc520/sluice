package adminapi

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/company/sluice/internal/adminauth"
	"github.com/company/sluice/internal/auth"
	"github.com/company/sluice/internal/crypto"
	"github.com/company/sluice/internal/store"
)

const (
	defaultAuditLimit     = 50
	maxAuditLimit         = 200
	maxRevealReasonLength = 500
)

// AuditHandler serves audit search, detail, and privileged reveal endpoints.
type AuditHandler struct {
	audit          store.AuditStore
	admins         store.AdminStore
	cipher         *crypto.Cipher
	now            func() time.Time
	trustedProxies []string
}

// NewAuditHandler creates the audit handler. trustedProxies controls which
// proxies may supply the client IP recorded in the administrator audit log.
func NewAuditHandler(audit store.AuditStore, admins store.AdminStore, cipher *crypto.Cipher, clock func() time.Time, trustedProxies []string) *AuditHandler {
	if clock == nil {
		clock = time.Now
	}
	return &AuditHandler{
		audit:          audit,
		admins:         admins,
		cipher:         cipher,
		now:            clock,
		trustedProxies: append([]string(nil), trustedProxies...),
	}
}

type auditDLPHitDTO struct {
	RuleID   string `json:"rule_id"`
	RuleName string `json:"rule_name"`
	Action   string `json:"action"`
}

type auditLogDTO struct {
	ID                 int64            `json:"id"`
	EventTime          time.Time        `json:"event_time"`
	RequestID          string           `json:"request_id"`
	TokenID            *string          `json:"token_id"`
	EmployeeID         *string          `json:"employee_id"`
	DepartmentID       *int64           `json:"department_id"`
	ClientIP           string           `json:"client_ip"`
	Method             string           `json:"method"`
	Path               string           `json:"path"`
	Model              string           `json:"model"`
	ProviderID         *string          `json:"provider_id"`
	StatusCode         int              `json:"status_code"`
	ErrorCode          string           `json:"error_code"`
	LatencyMS          int              `json:"latency_ms"`
	UpstreamLatencyMS  int              `json:"upstream_latency_ms"`
	FirstByteMS        int              `json:"first_byte_ms"`
	PromptTokens       int              `json:"prompt_tokens"`
	CompletionTokens   int              `json:"completion_tokens"`
	TotalTokens        int              `json:"total_tokens"`
	UsageEstimated     bool             `json:"usage_estimated"`
	HasPrompt          bool             `json:"has_prompt"`
	HasResponse        bool             `json:"has_response"`
	PromptTruncated    bool             `json:"prompt_truncated"`
	ResponseTruncated  bool             `json:"response_truncated"`
	DLPAction          string           `json:"dlp_action"`
	DLPHits            []auditDLPHitDTO `json:"dlp_hits"`
	ClientDisconnected bool             `json:"client_disconnected"`
	UpstreamRequestID  string           `json:"upstream_request_id"`
	CreatedAt          time.Time        `json:"created_at"`
}

func uuidString(id *uuid.UUID) *string {
	if id == nil {
		return nil
	}
	value := id.String()
	return &value
}

func toAuditLogDTO(a *store.AuditRecord) auditLogDTO {
	return auditLogDTO{
		ID:                 a.ID,
		EventTime:          a.EventTime,
		RequestID:          a.RequestID,
		TokenID:            uuidString(a.TokenID),
		EmployeeID:         uuidString(a.EmployeeID),
		DepartmentID:       a.DepartmentID,
		ClientIP:           a.ClientIP,
		Method:             a.Method,
		Path:               a.Path,
		Model:              a.Model,
		ProviderID:         uuidString(a.ProviderID),
		StatusCode:         a.StatusCode,
		ErrorCode:          a.ErrorCode,
		LatencyMS:          a.LatencyMS,
		UpstreamLatencyMS:  a.UpstreamLatencyMS,
		FirstByteMS:        a.FirstByteMS,
		PromptTokens:       a.PromptTokens,
		CompletionTokens:   a.CompletionTokens,
		TotalTokens:        a.TotalTokens,
		UsageEstimated:     a.UsageEstimated,
		HasPrompt:          len(a.PromptEnc) > 0,
		HasResponse:        len(a.ResponseEnc) > 0,
		PromptTruncated:    a.PromptTruncated,
		ResponseTruncated:  a.ResponseTruncated,
		DLPAction:          a.DLPAction,
		DLPHits:            parseAuditHits(a.DLPHits),
		ClientDisconnected: a.ClientDisconnected,
		UpstreamRequestID:  a.UpstreamRequestID,
		CreatedAt:          a.CreatedAt,
	}
}

// parseAuditHits exposes only rule identity and action. Match offsets and the
// encrypted prompt/response never leave the server through this route.
func parseAuditHits(raw []byte) []auditDLPHitDTO {
	out := []auditDLPHitDTO{}
	if len(raw) == 0 {
		return out
	}
	var items []struct {
		RuleID   string `json:"rule_id"`
		RuleName string `json:"rule_name"`
		Action   string `json:"action"`
	}
	if err := json.Unmarshal(raw, &items); err != nil {
		return out
	}
	for _, item := range items {
		out = append(out, auditDLPHitDTO{
			RuleID:   item.RuleID,
			RuleName: item.RuleName,
			Action:   item.Action,
		})
	}
	return out
}

// List handles GET /admin/api/audit.
func (h *AuditHandler) List(c *gin.Context) {
	from, to, ok := queryTimeRange(c, h.now())
	if !ok {
		return
	}
	limit, ok := auditLimit(c)
	if !ok {
		return
	}
	filter := store.AuditFilter{
		From:  &from,
		To:    &to,
		Limit: limit,
	}
	employeeID, ok := queryUUID(c, "employee_id")
	if !ok {
		return
	}
	filter.EmployeeID = employeeID

	if raw := strings.TrimSpace(c.Query("department_id")); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			BadRequest(c, "invalid department_id")
			return
		}
		filter.DepartmentID = &id
	}

	if raw := strings.TrimSpace(c.Query("status_code")); raw != "" {
		code, err := strconv.Atoi(raw)
		if err != nil || code < 100 || code > 599 {
			BadRequest(c, "status_code must be between 100 and 599")
			return
		}
		filter.StatusCode = &code
	}
	if action := strings.TrimSpace(c.Query("dlp_action")); action != "" {
		switch action {
		case "none", "block", "mask", "audit":
			filter.DLPAction = action
		default:
			BadRequest(c, "dlp_action must be none, block, mask, or audit")
			return
		}
	}
	if model := strings.TrimSpace(c.Query("model")); model != "" {
		if len(model) > 128 {
			BadRequest(c, "model is too long")
			return
		}
		filter.Model = model
	}
	if cursor := strings.TrimSpace(c.Query("cursor")); cursor != "" {
		if _, _, err := decodeAuditCursor(cursor); err != nil {
			BadRequest(c, "invalid cursor")
			return
		}
		filter.Cursor = cursor
	}

	items, err := h.audit.List(c.Request.Context(), filter)
	if err != nil {
		InternalError(c)
		return
	}
	out := make([]auditLogDTO, 0, len(items))
	for _, item := range items {
		out = append(out, toAuditLogDTO(item))
	}
	nextCursor := ""
	if len(items) == limit {
		last := items[len(items)-1]
		nextCursor = encodeAuditCursor(last.EventTime, last.ID)
	}
	OK(c, http.StatusOK, gin.H{"items": out, "next_cursor": nextCursor})
}

// Detail handles GET /admin/api/audit/:id. It returns metadata only.
func (h *AuditHandler) Detail(c *gin.Context) {
	id, ok := auditID(c)
	if !ok {
		return
	}
	record, err := h.audit.Get(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			Fail(c, http.StatusNotFound, "not_found", "audit record not found")
			return
		}
		InternalError(c)
		return
	}
	c.Header("Cache-Control", "no-store")
	OK(c, http.StatusOK, toAuditLogDTO(record))
}

type revealPayload struct {
	Reason string `json:"reason"`
}

// Reveal handles POST /admin/api/audit/:id/reveal. The plaintext response is
// returned only after the administrator audit record has been persisted.
func (h *AuditHandler) Reveal(c *gin.Context) {
	user := adminauth.CurrentUser(c)
	if user == nil {
		Fail(c, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}
	if user.Role != "super_admin" {
		Fail(c, http.StatusForbidden, "forbidden", "insufficient role")
		return
	}
	id, ok := auditID(c)
	if !ok {
		return
	}
	var payload revealPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		BadRequest(c, "invalid JSON body")
		return
	}
	reason := strings.TrimSpace(payload.Reason)
	if utf8.RuneCountInString(reason) < 5 {
		BadRequest(c, "reason must be at least 5 characters")
		return
	}
	if utf8.RuneCountInString(reason) > maxRevealReasonLength {
		BadRequest(c, "reason is too long")
		return
	}

	record, err := h.audit.Get(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			Fail(c, http.StatusNotFound, "not_found", "audit record not found")
			return
		}
		InternalError(c)
		return
	}
	prompt, err := h.decryptField(record.PromptEnc, record.RequestID+":prompt")
	if err != nil {
		InternalError(c)
		return
	}
	response, err := h.decryptField(record.ResponseEnc, record.RequestID+":response")
	if err != nil {
		InternalError(c)
		return
	}

	detail, err := json.Marshal(map[string]any{
		"reason":     reason,
		"fields":     []string{"prompt", "response"},
		"request_id": record.RequestID,
	})
	if err != nil {
		InternalError(c)
		return
	}
	if err := h.admins.InsertAdminAudit(c.Request.Context(), &store.AdminAuditLog{
		AdminID:    user.ID,
		Action:     "reveal_audit",
		TargetType: "audit_log",
		TargetID:   strconv.FormatInt(record.ID, 10),
		Detail:     detail,
		ClientIP:   auth.ClientIP(c.Request, h.trustedProxies),
	}); err != nil {
		// Fail closed: never disclose plaintext without an audit trail.
		InternalError(c)
		return
	}

	c.Header("Cache-Control", "no-store, max-age=0")
	c.Header("Pragma", "no-cache")
	OK(c, http.StatusOK, gin.H{"prompt": prompt, "response": response})
}

func (h *AuditHandler) decryptField(encrypted []byte, aad string) (string, error) {
	if len(encrypted) == 0 {
		return "", nil
	}
	if h.cipher == nil {
		return "", errors.New("audit: cipher is not configured")
	}
	plain, err := h.cipher.Open(encrypted, aad)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func auditID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		BadRequest(c, "invalid id")
		return 0, false
	}
	return id, true
}

func auditLimit(c *gin.Context) (int, bool) {
	raw := strings.TrimSpace(c.Query("limit"))
	if raw == "" {
		return defaultAuditLimit, true
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 1 {
		BadRequest(c, "limit must be a positive integer")
		return 0, false
	}
	if limit > maxAuditLimit {
		limit = maxAuditLimit
	}
	return limit, true
}

// queryTimeRange parses from/to. An omitted to defaults to now and an omitted
// from defaults to the 24 hours before to. A date-only to is treated as the
// end of that day in Asia/Shanghai.
func queryTimeRange(c *gin.Context, now time.Time) (time.Time, time.Time, bool) {
	rawFrom := strings.TrimSpace(c.Query("from"))
	rawTo := strings.TrimSpace(c.Query("to"))

	to := now
	if rawTo != "" {
		parsed, err := parseQueryTime(rawTo, true)
		if err != nil {
			BadRequest(c, "from and to must be RFC3339 timestamps")
			return time.Time{}, time.Time{}, false
		}
		to = parsed
	}
	from := to.Add(-24 * time.Hour)
	if rawFrom != "" {
		parsed, err := parseQueryTime(rawFrom, false)
		if err != nil {
			BadRequest(c, "from and to must be RFC3339 timestamps")
			return time.Time{}, time.Time{}, false
		}
		from = parsed
	}
	if !from.Before(to) {
		BadRequest(c, "from must be before to")
		return time.Time{}, time.Time{}, false
	}
	return from, to, true
}

func parseQueryTime(raw string, endOfDay bool) (time.Time, error) {
	if len(raw) == len("2006-01-02") {
		day, err := time.ParseInLocation("2006-01-02", raw, shanghaiLocation())
		if err == nil {
			if endOfDay {
				return day.AddDate(0, 0, 1), nil
			}
			return day, nil
		}
	}
	if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
		return parsed, nil
	}
	for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02 15:04:05"} {
		if parsed, err := time.ParseInLocation(layout, raw, shanghaiLocation()); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, errors.New("unsupported time format")
}

func encodeAuditCursor(at time.Time, id int64) string {
	raw := at.UTC().Format(time.RFC3339Nano) + "|" + strconv.FormatInt(id, 10)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeAuditCursor(cursor string) (time.Time, int64, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, 0, errors.New("invalid cursor")
	}
	parts := strings.SplitN(string(raw), "|", 2)
	if len(parts) != 2 {
		return time.Time{}, 0, errors.New("invalid cursor")
	}
	at, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, 0, errors.New("invalid cursor")
	}
	id, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return time.Time{}, 0, errors.New("invalid cursor")
	}
	return at, id, nil
}
