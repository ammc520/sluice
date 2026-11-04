package portalapi

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/company/sluice/internal/portalauth"
	"github.com/company/sluice/internal/portalcfg"
	"github.com/company/sluice/internal/store"
)

const (
	defaultUsageDays  = 30
	maxUsagePageSize  = 100
	defaultUsageLimit = 50
)

type usageHandler struct {
	audit store.AuditStore
	cfg   *portalcfg.Provider
	clock func() time.Time
}

type usageItemDTO struct {
	ID                 int64     `json:"id"`
	EventTime          time.Time `json:"event_time"`
	Model              string    `json:"model"`
	Path               string    `json:"path"`
	StatusCode         int       `json:"status_code"`
	ErrorCode          string    `json:"error_code,omitempty"`
	LatencyMS          int       `json:"latency_ms"`
	PromptTokens       int       `json:"prompt_tokens"`
	CompletionTokens   int       `json:"completion_tokens"`
	TotalTokens        int       `json:"total_tokens"`
	UsageEstimated     bool      `json:"usage_estimated"`
	DLPAction          string    `json:"dlp_action"`
	ClientDisconnected bool      `json:"client_disconnected"`
}

// Summary handles GET /portal/api/me/summary.
func (h *usageHandler) Summary(c *gin.Context) {
	employee := portalauth.CurrentEmployee(c)
	if employee == nil {
		Fail(c, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}
	from, to, ok := h.rangeFromQuery(c)
	if !ok {
		return
	}

	summary, err := h.audit.EmployeeSummary(c.Request.Context(), employee.ID, from, to)
	if err != nil {
		InternalError(c)
		return
	}
	trend, err := h.audit.EmployeeTrend(c.Request.Context(), employee.ID, from, to)
	if err != nil {
		InternalError(c)
		return
	}
	models, err := h.audit.EmployeeModelUsage(c.Request.Context(), employee.ID, from, to)
	if err != nil {
		InternalError(c)
		return
	}

	points := make([]gin.H, 0, len(trend))
	for _, p := range trend {
		points = append(points, gin.H{
			"day":      p.Day.Format("2006-01-02"),
			"requests": p.Requests,
			"tokens":   p.Tokens,
			"errors":   p.Errors,
		})
	}
	modelPoints := make([]gin.H, 0, len(models))
	for _, m := range models {
		days := make([]gin.H, 0, len(m.Days))
		for _, d := range m.Days {
			days = append(days, gin.H{
				"day":      d.Day.Format("2006-01-02"),
				"requests": d.Requests,
				"tokens":   d.Tokens,
			})
		}
		modelPoints = append(modelPoints, gin.H{
			"model":    m.Model,
			"requests": m.Requests,
			"tokens":   m.Tokens,
			"errors":   m.Errors,
			"trend":    days,
		})
	}
	OK(c, http.StatusOK, gin.H{
		"from":    from,
		"to":      to,
		"summary": summary,
		"trend":   points,
		"models":  modelPoints,
	})
}

// List handles GET /portal/api/me/usage. Only request metadata is returned;
// prompt and response payloads are never decrypted here.
func (h *usageHandler) List(c *gin.Context) {
	employee := portalauth.CurrentEmployee(c)
	if employee == nil {
		Fail(c, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}
	from, to, ok := h.rangeFromQuery(c)
	if !ok {
		return
	}

	limit := defaultUsageLimit
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v <= 0 {
			BadRequest(c, "limit must be a positive integer")
			return
		}
		if v > maxUsagePageSize {
			v = maxUsagePageSize
		}
		limit = v
	}

	filter := store.AuditFilter{
		From:       &from,
		To:         &to,
		EmployeeID: &employee.ID,
		Limit:      limit,
	}
	if cursor := strings.TrimSpace(c.Query("cursor")); cursor != "" {
		if _, _, err := decodeUsageCursor(cursor); err != nil {
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
	out := make([]usageItemDTO, 0, len(items))
	for _, item := range items {
		out = append(out, usageItemDTO{
			ID:                 item.ID,
			EventTime:          item.EventTime,
			Model:              item.Model,
			Path:               item.Path,
			StatusCode:         item.StatusCode,
			ErrorCode:          item.ErrorCode,
			LatencyMS:          item.LatencyMS,
			PromptTokens:       item.PromptTokens,
			CompletionTokens:   item.CompletionTokens,
			TotalTokens:        item.TotalTokens,
			UsageEstimated:     item.UsageEstimated,
			DLPAction:          item.DLPAction,
			ClientDisconnected: item.ClientDisconnected,
		})
	}

	nextCursor := ""
	if len(items) == limit {
		last := items[len(items)-1]
		nextCursor = encodeUsageCursor(last.EventTime, last.ID)
	}
	OK(c, http.StatusOK, gin.H{
		"items":       out,
		"next_cursor": nextCursor,
		"from":        from,
		"to":          to,
	})
}

// rangeFromQuery parses and clamps from/to. Date-only "to" is inclusive of
// the whole day.
func (h *usageHandler) rangeFromQuery(c *gin.Context) (time.Time, time.Time, bool) {
	now := h.clock()
	settings := h.cfg.Get(c.Request.Context())
	maxDays := settings.UsageMaxDays
	if maxDays <= 0 {
		maxDays = 90
	}

	to := now
	from := now.AddDate(0, 0, -defaultUsageDays)

	if raw := strings.TrimSpace(c.Query("to")); raw != "" {
		parsed, err := parsePortalTime(raw)
		if err != nil {
			BadRequest(c, "invalid to timestamp")
			return time.Time{}, time.Time{}, false
		}
		if len(raw) == len("2006-01-02") {
			parsed = parsed.Add(24 * time.Hour)
		}
		to = parsed
	}
	if raw := strings.TrimSpace(c.Query("from")); raw != "" {
		parsed, err := parsePortalTime(raw)
		if err != nil {
			BadRequest(c, "invalid from timestamp")
			return time.Time{}, time.Time{}, false
		}
		from = parsed
	}
	if to.After(now) {
		to = now
	}
	if !from.Before(to) {
		BadRequest(c, "from must be before to")
		return time.Time{}, time.Time{}, false
	}
	if maxRange := time.Duration(maxDays) * 24 * time.Hour; to.Sub(from) > maxRange {
		from = to.Add(-maxRange)
	}
	return from, to, true
}

var portalShanghai = func() *time.Location {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return time.FixedZone("CST", 8*3600)
	}
	return loc
}()

func parsePortalTime(raw string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t, nil
	}
	for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, raw, portalShanghai); err == nil {
			return t, nil
		}
	}
	return time.Time{}, errors.New("unsupported time format")
}

func encodeUsageCursor(at time.Time, id int64) string {
	raw := at.UTC().Format(time.RFC3339Nano) + "|" + strconv.FormatInt(id, 10)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeUsageCursor(cursor string) (time.Time, int64, error) {
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
