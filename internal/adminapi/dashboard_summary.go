package adminapi

import (
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/company/sluice/internal/store"
)

const (
	summaryCacheTTL     = 15 * time.Second
	summaryCacheSize    = 64
	dashboardMaxRange   = 90 * 24 * time.Hour
	dashboardHourFormat = "2006-01-02 15:04"
)

type summaryParams struct {
	from         time.Time
	to           time.Time
	previousFrom time.Time
	previousTo   time.Time
	interval     string
	departmentID *int64
	providerID   *uuid.UUID
	cacheKey     string
}

type summaryCacheEntry struct {
	expiresAt time.Time
	value     summaryDTO
}

type summaryCache struct {
	mu    sync.Mutex
	items map[string]summaryCacheEntry
}

func newSummaryCache() *summaryCache {
	return &summaryCache{items: map[string]summaryCacheEntry{}}
}

func (c *summaryCache) get(key string, now time.Time) (summaryDTO, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.items[key]
	if !ok {
		return summaryDTO{}, false
	}
	if !now.Before(entry.expiresAt) {
		delete(c.items, key)
		return summaryDTO{}, false
	}
	return entry.value, true
}

func (c *summaryCache) set(key string, value summaryDTO, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.items) >= summaryCacheSize {
		for k, entry := range c.items {
			if !now.Before(entry.expiresAt) {
				delete(c.items, k)
			}
		}
		if len(c.items) >= summaryCacheSize {
			c.items = map[string]summaryCacheEntry{}
		}
	}
	c.items[key] = summaryCacheEntry{expiresAt: now.Add(summaryCacheTTL), value: value}
}

type summaryDTO struct {
	GeneratedAt     time.Time           `json:"generated_at"`
	Range           summaryRangeDTO     `json:"range"`
	KPIs            summaryKPIDTO       `json:"kpis"`
	Trend           summaryTrendDTO     `json:"trend"`
	Breakdown       summaryBreakdownDTO `json:"breakdown"`
	TopUsers        []summaryUserDTO    `json:"top_users"`
	DLP             summaryDLPDTO       `json:"dlp"`
	QuotaPressure   []quotaPressureDTO  `json:"quota_pressure"`
	RecentIncidents []incidentDTO       `json:"recent_incidents"`
}

type summaryRangeDTO struct {
	From         time.Time `json:"from"`
	To           time.Time `json:"to"`
	PreviousFrom time.Time `json:"previous_from"`
	PreviousTo   time.Time `json:"previous_to"`
	Interval     string    `json:"interval"`
	Timezone     string    `json:"timezone"`
}

type summaryKPIDTO struct {
	Requests          kpiMetricDTO `json:"requests"`
	Tokens            kpiMetricDTO `json:"tokens"`
	PromptTokens      kpiMetricDTO `json:"prompt_tokens"`
	CompletionTokens  kpiMetricDTO `json:"completion_tokens"`
	ActiveUsers       kpiMetricDTO `json:"active_users"`
	ErrorRate         kpiMetricDTO `json:"error_rate"`
	DLPRequests       kpiMetricDTO `json:"dlp_requests"`
	P95LatencyMS      kpiMetricDTO `json:"p95_latency_ms"`
	ClientDisconnects kpiMetricDTO `json:"client_disconnects"`
}

type kpiMetricDTO struct {
	Value    float64  `json:"value"`
	Previous float64  `json:"previous"`
	DeltaPct *float64 `json:"delta_pct,omitempty"`
	DeltaPP  *float64 `json:"delta_pp,omitempty"`
}

type summaryTrendDTO struct {
	Interval string                 `json:"interval"`
	Buckets  []string               `json:"buckets"`
	Current  []summaryTrendPointDTO `json:"current"`
	Previous []summaryTrendPointDTO `json:"previous"`
}

type summaryTrendPointDTO struct {
	Requests         int64  `json:"requests"`
	Errors           int64  `json:"errors"`
	Tokens           int64  `json:"tokens"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	DLPRequests      int64  `json:"dlp_requests"`
	P95LatencyMS     *int64 `json:"p95_latency_ms"`
}

type summaryBreakdownDTO struct {
	Providers []providerStatDTO `json:"providers"`
	Models    []modelStatDTO    `json:"models"`
}

type providerStatDTO struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Requests     int64  `json:"requests"`
	Tokens       int64  `json:"tokens"`
	Errors       int64  `json:"errors"`
	P95LatencyMS int64  `json:"p95_latency_ms"`
}

type modelStatDTO struct {
	Model    string               `json:"model"`
	Requests int64                `json:"requests"`
	Tokens   int64                `json:"tokens"`
	Errors   int64                `json:"errors"`
	Trend    []modelTrendPointDTO `json:"trend"`
}

type modelTrendPointDTO struct {
	Bucket   string `json:"bucket"`
	Requests int64  `json:"requests"`
	Tokens   int64  `json:"tokens"`
	Errors   int64  `json:"errors"`
}

type summaryUserDTO struct {
	EmployeeID  string         `json:"employee_id"`
	EmployeeNo  string         `json:"employee_no"`
	Name        string         `json:"name"`
	Department  string         `json:"department"`
	Requests    int64          `json:"requests"`
	Tokens      int64          `json:"tokens"`
	Errors      int64          `json:"errors"`
	ErrorRate   float64        `json:"error_rate"`
	DLPRequests int64          `json:"dlp_requests"`
	Quota       *quotaBriefDTO `json:"quota"`
}

type quotaBriefDTO struct {
	Type   string  `json:"type"`
	Period string  `json:"period"`
	Used   int64   `json:"used"`
	Total  int64   `json:"total"`
	Pct    float64 `json:"pct"`
}

type summaryDLPDTO struct {
	Requests int64                 `json:"requests"`
	Hits     int64                 `json:"hits"`
	ByAction []dlpActionSummaryDTO `json:"by_action"`
	ByRule   []dlpRuleSummaryDTO   `json:"by_rule"`
}

type dlpActionSummaryDTO struct {
	Action   string `json:"action"`
	Requests int64  `json:"requests"`
	Hits     int64  `json:"hits"`
}

type dlpRuleSummaryDTO struct {
	RuleName string `json:"rule_name"`
	Action   string `json:"action"`
	Hits     int64  `json:"hits"`
}

type quotaPressureDTO struct {
	EmployeeID  string     `json:"employee_id"`
	Name        string     `json:"name"`
	Department  string     `json:"department"`
	QuotaType   string     `json:"quota_type"`
	QuotaPeriod string     `json:"quota_period"`
	Used        int64      `json:"used"`
	Total       int64      `json:"total"`
	Pct         float64    `json:"pct"`
	PeriodStart *time.Time `json:"period_start"`
	ExpiresAt   *time.Time `json:"expires_at"`
}

type incidentDTO struct {
	ID           int64     `json:"id"`
	EventTime    time.Time `json:"event_time"`
	EmployeeID   string    `json:"employee_id"`
	EmployeeName string    `json:"employee_name"`
	Action       string    `json:"action"`
	Rules        []string  `json:"rules"`
	Model        string    `json:"model"`
	RequestID    string    `json:"request_id"`
}

// Summary handles GET /admin/api/dashboard/summary. It returns a consistent
// set of aggregates for the requested range, cached for 15 seconds.
func (h *DashboardHandler) Summary(c *gin.Context) {
	if h.dashboard == nil {
		InternalError(c)
		return
	}
	now := h.now()
	hasCustomRange := strings.TrimSpace(c.Query("from")) != "" || strings.TrimSpace(c.Query("to")) != ""
	if !hasCustomRange {
		now = now.Truncate(summaryCacheTTL)
	}
	params, ok := parseSummaryParams(c, now)
	if !ok {
		return
	}

	if cached, hit := h.cache.get(params.cacheKey, now); hit {
		c.Header("X-Dashboard-Cache", "hit")
		OK(c, http.StatusOK, cached)
		return
	}

	current := store.DashboardFilter{
		From:         params.from,
		To:           params.to,
		DepartmentID: params.departmentID,
		ProviderID:   params.providerID,
	}
	previous := store.DashboardFilter{
		From:         params.previousFrom,
		To:           params.previousTo,
		DepartmentID: params.departmentID,
		ProviderID:   params.providerID,
	}
	data, err := h.dashboard.Summary(c.Request.Context(), current, previous, params.interval, now)
	if err != nil {
		InternalError(c)
		return
	}
	dto := buildSummaryDTO(data, params, now)
	h.cache.set(params.cacheKey, dto, now)
	c.Header("X-Dashboard-Cache", "miss")
	OK(c, http.StatusOK, dto)
}

func parseSummaryParams(c *gin.Context, now time.Time) (summaryParams, bool) {
	var p summaryParams

	rawFrom := strings.TrimSpace(c.Query("from"))
	rawTo := strings.TrimSpace(c.Query("to"))
	rawRange := strings.TrimSpace(c.Query("range"))
	if rawRange != "" && (rawFrom != "" || rawTo != "") {
		BadRequest(c, "use either range or from/to")
		return p, false
	}
	if rawRange == "" && (rawFrom != "" || rawTo != "") && (rawFrom == "" || rawTo == "") {
		BadRequest(c, "from and to must be provided together")
		return p, false
	}

	switch {
	case rawFrom != "":
		from, err := parseQueryTime(rawFrom, false)
		if err != nil {
			BadRequest(c, "from and to must be RFC3339 timestamps or dates")
			return p, false
		}
		to, err := parseQueryTime(rawTo, true)
		if err != nil {
			BadRequest(c, "from and to must be RFC3339 timestamps or dates")
			return p, false
		}
		if !from.Before(to) {
			BadRequest(c, "from must be before to")
			return p, false
		}
		duration := to.Sub(from)
		if duration > dashboardMaxRange {
			BadRequest(c, "range must not exceed 90 days")
			return p, false
		}
		p.from, p.to = from, to
		p.previousFrom, p.previousTo = from.Add(-duration), from
		if duration <= 48*time.Hour {
			p.interval = "hour"
		} else {
			p.interval = "day"
		}
	default:
		if rawRange == "" {
			rawRange = "24h"
		}
		from, to, previousFrom, previousTo, interval, ok := rangeWindow(rawRange, now)
		if !ok {
			BadRequest(c, "range must be today, yesterday, 24h, 7d, 30d, or 90d")
			return p, false
		}
		p.from, p.to = from, to
		p.previousFrom, p.previousTo = previousFrom, previousTo
		p.interval = interval
	}

	if rawInterval := strings.TrimSpace(c.Query("interval")); rawInterval != "" {
		if rawInterval != "hour" && rawInterval != "day" {
			BadRequest(c, "interval must be hour or day")
			return p, false
		}
		p.interval = rawInterval
	}

	if p.to.Sub(p.from) > dashboardMaxRange {
		BadRequest(c, "range must not exceed 90 days")
		return p, false
	}

	if rawDept := strings.TrimSpace(c.Query("department_id")); rawDept != "" {
		id, err := strconv.ParseInt(rawDept, 10, 64)
		if err != nil || id <= 0 {
			BadRequest(c, "invalid department_id")
			return p, false
		}
		p.departmentID = &id
	}
	providerID, ok := queryUUID(c, "provider_id")
	if !ok {
		return p, false
	}
	p.providerID = providerID

	dept := ""
	if p.departmentID != nil {
		dept = strconv.FormatInt(*p.departmentID, 10)
	}
	provider := ""
	if p.providerID != nil {
		provider = p.providerID.String()
	}
	p.cacheKey = fmt.Sprintf("%s|%s|%s|%s|%s",
		p.from.UTC().Format(time.RFC3339Nano),
		p.to.UTC().Format(time.RFC3339Nano),
		p.interval, dept, provider)
	return p, true
}

func rangeWindow(raw string, now time.Time) (from, to, previousFrom, previousTo time.Time, interval string, ok bool) {
	loc := shanghaiLocation()
	local := now.In(loc)
	startOfDay := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)

	switch raw {
	case "today":
		from = startOfDay
		to = now
		previousFrom = from.AddDate(0, 0, -1)
		previousTo = now.AddDate(0, 0, -1)
		interval = "hour"
	case "yesterday":
		to = startOfDay
		from = startOfDay.AddDate(0, 0, -1)
		previousFrom = from.AddDate(0, 0, -1)
		previousTo = to.AddDate(0, 0, -1)
		interval = "hour"
	case "24h":
		to = now
		from = now.Truncate(time.Hour).Add(-23 * time.Hour)
		previousFrom = from.Add(-24 * time.Hour)
		previousTo = to.Add(-24 * time.Hour)
		interval = "hour"
	case "7d":
		from = startOfDay.AddDate(0, 0, -6)
		to = now
		previousFrom = from.AddDate(0, 0, -7)
		previousTo = now.AddDate(0, 0, -7)
		interval = "day"
	case "30d":
		from = startOfDay.AddDate(0, 0, -29)
		to = now
		previousFrom = from.AddDate(0, 0, -30)
		previousTo = now.AddDate(0, 0, -30)
		interval = "day"
	case "90d":
		from = startOfDay.AddDate(0, 0, -89)
		to = now
		previousFrom = from.AddDate(0, 0, -90)
		previousTo = now.AddDate(0, 0, -90)
		interval = "day"
	default:
		return time.Time{}, time.Time{}, time.Time{}, time.Time{}, "", false
	}
	return from, to, previousFrom, previousTo, interval, true
}

func buildSummaryDTO(data store.DashboardData, p summaryParams, now time.Time) summaryDTO {
	cur, prev := data.KPI, data.PreviousKPI
	currentTrend := fillTrend(data.Trend, p.from, p.to, p.interval)
	previousTrend := fillTrend(data.PreviousTrend, p.previousFrom, p.previousTo, p.interval)

	buckets := make([]string, 0, len(currentTrend))
	currentPoints := make([]summaryTrendPointDTO, 0, len(currentTrend))
	previousPoints := make([]summaryTrendPointDTO, 0, len(currentTrend))
	for i, point := range currentTrend {
		buckets = append(buckets, point.Bucket.Format(time.RFC3339))
		currentPoints = append(currentPoints, toSummaryTrendPoint(point))
		if i < len(previousTrend) {
			previousPoints = append(previousPoints, toSummaryTrendPoint(previousTrend[i]))
		} else {
			previousPoints = append(previousPoints, summaryTrendPointDTO{})
		}
	}

	byAction := make([]dlpActionSummaryDTO, 0, 3)
	actionMap := map[string]store.DashboardDLPActionStat{}
	for _, item := range data.DLPByAction {
		actionMap[item.Action] = item
	}
	for _, action := range []string{"block", "mask", "audit"} {
		item := actionMap[action]
		byAction = append(byAction, dlpActionSummaryDTO{
			Action:   action,
			Requests: item.Requests,
			Hits:     item.Hits,
		})
	}

	providers := make([]providerStatDTO, 0, len(data.Providers))
	for _, item := range data.Providers {
		providers = append(providers, providerStatDTO{
			ID:           item.ID,
			Name:         item.Name,
			Requests:     item.Requests,
			Tokens:       item.Tokens,
			Errors:       item.Errors,
			P95LatencyMS: item.P95LatencyMS,
		})
	}
	models := make([]modelStatDTO, 0, len(data.Models))
	for _, item := range data.Models {
		points := make([]modelTrendPointDTO, 0, len(item.Trend))
		for _, point := range item.Trend {
			points = append(points, modelTrendPointDTO{
				Bucket:   point.Bucket.Format(time.RFC3339),
				Requests: point.Requests,
				Tokens:   point.Tokens,
				Errors:   point.Errors,
			})
		}
		models = append(models, modelStatDTO{
			Model:    item.Model,
			Requests: item.Requests,
			Tokens:   item.Tokens,
			Errors:   item.Errors,
			Trend:    points,
		})
	}

	users := make([]summaryUserDTO, 0, len(data.Users))
	for _, item := range data.Users {
		user := summaryUserDTO{
			EmployeeID:  item.EmployeeID.String(),
			EmployeeNo:  item.EmployeeNo,
			Name:        item.Name,
			Department:  item.Department,
			Requests:    item.Requests,
			Tokens:      item.Tokens,
			Errors:      item.Errors,
			ErrorRate:   ratio(item.Errors, item.Requests),
			DLPRequests: item.DLPRequests,
		}
		if quota, ok := data.UserQuotas[item.EmployeeID]; ok {
			user.Quota = &quotaBriefDTO{
				Type:   quota.QuotaType,
				Period: quota.QuotaPeriod,
				Used:   quota.Used,
				Total:  quota.Total,
				Pct:    quotaPercent(quota),
			}
		}
		users = append(users, user)
	}

	rules := make([]dlpRuleSummaryDTO, 0, len(data.DLPByRule))
	for _, item := range data.DLPByRule {
		rules = append(rules, dlpRuleSummaryDTO{
			RuleName: item.RuleName,
			Action:   item.Action,
			Hits:     item.Hits,
		})
	}

	pressure := make([]quotaPressureDTO, 0, len(data.QuotaPressure))
	for _, item := range data.QuotaPressure {
		pressure = append(pressure, quotaPressureDTO{
			EmployeeID:  item.EmployeeID.String(),
			Name:        item.EmployeeName,
			Department:  item.Department,
			QuotaType:   item.QuotaType,
			QuotaPeriod: item.QuotaPeriod,
			Used:        item.Used,
			Total:       item.Total,
			Pct:         quotaPercent(item),
			PeriodStart: item.PeriodStart,
			ExpiresAt:   item.ExpiresAt,
		})
	}

	incidents := make([]incidentDTO, 0, len(data.Incidents))
	for _, item := range data.Incidents {
		employeeID := ""
		if item.EmployeeID != nil {
			employeeID = item.EmployeeID.String()
		}
		incidents = append(incidents, incidentDTO{
			ID:           item.ID,
			EventTime:    item.EventTime,
			EmployeeID:   employeeID,
			EmployeeName: item.EmployeeName,
			Action:       item.Action,
			Rules:        item.Rules,
			Model:        item.Model,
			RequestID:    item.RequestID,
		})
	}

	return summaryDTO{
		GeneratedAt: now,
		Range: summaryRangeDTO{
			From:         p.from,
			To:           p.to,
			PreviousFrom: p.previousFrom,
			PreviousTo:   p.previousTo,
			Interval:     p.interval,
			Timezone:     "Asia/Shanghai",
		},
		KPIs: summaryKPIDTO{
			Requests:          countMetric(cur.Requests, prev.Requests),
			Tokens:            countMetric(cur.Tokens, prev.Tokens),
			PromptTokens:      countMetric(cur.PromptTokens, prev.PromptTokens),
			CompletionTokens:  countMetric(cur.CompletionTokens, prev.CompletionTokens),
			ActiveUsers:       countMetric(cur.ActiveUsers, prev.ActiveUsers),
			ErrorRate:         rateMetric(cur.Errors, cur.Requests, prev.Errors, prev.Requests),
			DLPRequests:       countMetric(cur.DLPRequests, prev.DLPRequests),
			P95LatencyMS:      countMetric(cur.P95LatencyMS, prev.P95LatencyMS),
			ClientDisconnects: countMetric(cur.ClientDisconnects, prev.ClientDisconnects),
		},
		Trend: summaryTrendDTO{
			Interval: p.interval,
			Buckets:  buckets,
			Current:  currentPoints,
			Previous: previousPoints,
		},
		Breakdown: summaryBreakdownDTO{
			Providers: providers,
			Models:    models,
		},
		TopUsers: users,
		DLP: summaryDLPDTO{
			Requests: cur.DLPRequests,
			Hits:     cur.DLPHits,
			ByAction: byAction,
			ByRule:   rules,
		},
		QuotaPressure:   pressure,
		RecentIncidents: incidents,
	}
}

func fillTrend(points []store.DashboardTrendPoint, from, to time.Time, interval string) []store.DashboardTrendPoint {
	start := truncateDashboardBucket(from, interval)
	// The query range is [from,to), so the bucket containing to must not be
	// emitted when to falls exactly on a boundary.
	end := truncateDashboardBucket(to.Add(-time.Nanosecond), interval)
	byBucket := make(map[int64]store.DashboardTrendPoint, len(points))
	for _, point := range points {
		byBucket[point.Bucket.UnixNano()] = point
	}
	out := []store.DashboardTrendPoint{}
	for bucket := start; !bucket.After(end); bucket = nextDashboardBucket(bucket, interval) {
		point, ok := byBucket[bucket.UnixNano()]
		if !ok {
			point = store.DashboardTrendPoint{Bucket: bucket}
		}
		out = append(out, point)
	}
	return out
}

func truncateDashboardBucket(t time.Time, interval string) time.Time {
	loc := shanghaiLocation()
	local := t.In(loc)
	if interval == "day" {
		return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	}
	return local.Truncate(time.Hour)
}

func nextDashboardBucket(t time.Time, interval string) time.Time {
	if interval == "day" {
		return t.AddDate(0, 0, 1)
	}
	return t.Add(time.Hour)
}

func toSummaryTrendPoint(point store.DashboardTrendPoint) summaryTrendPointDTO {
	return summaryTrendPointDTO{
		Requests:         point.Requests,
		Errors:           point.Errors,
		Tokens:           point.Tokens,
		PromptTokens:     point.PromptTokens,
		CompletionTokens: point.CompletionTokens,
		DLPRequests:      point.DLPRequests,
		P95LatencyMS:     point.P95LatencyMS,
	}
}

func countMetric(current, previous int64) kpiMetricDTO {
	return kpiMetricDTO{
		Value:    float64(current),
		Previous: float64(previous),
		DeltaPct: deltaPct(previous, current),
	}
}

func rateMetric(currentErrors, currentRequests, previousErrors, previousRequests int64) kpiMetricDTO {
	return kpiMetricDTO{
		Value:    ratio(currentErrors, currentRequests),
		Previous: ratio(previousErrors, previousRequests),
		DeltaPP:  deltaPP(previousErrors, previousRequests, currentErrors, currentRequests),
	}
}

func ratio(numerator, denominator int64) float64 {
	if denominator <= 0 {
		return 0
	}
	return round4(float64(numerator) / float64(denominator))
}

func deltaPct(previous, current int64) *float64 {
	if previous == 0 {
		if current == 0 {
			value := 0.0
			return &value
		}
		return nil
	}
	value := round1((float64(current-previous) / float64(previous)) * 100)
	return &value
}

func deltaPP(previousErrors, previousRequests, currentErrors, currentRequests int64) *float64 {
	if previousRequests <= 0 || currentRequests <= 0 {
		return nil
	}
	value := round2((ratio(currentErrors, currentRequests) - ratio(previousErrors, previousRequests)) * 100)
	return &value
}

func quotaPercent(q store.DashboardQuota) float64 {
	if q.Total <= 0 {
		return 0
	}
	return round4(math.Min(1, float64(q.Used)/float64(q.Total)))
}

func round1(value float64) float64 { return math.Round(value*10) / 10 }
func round2(value float64) float64 { return math.Round(value*100) / 100 }
func round4(value float64) float64 { return math.Round(value*10000) / 10000 }
