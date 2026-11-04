package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"

	"github.com/company/sluice/internal/store"
)

const dashboardQuotaLimit = 10
const dashboardQuotaPressureThreshold = 0.8

type dashboardWhere struct {
	args   []any
	clause string
}

func newDashboardWhere(alias string, f store.DashboardFilter) *dashboardWhere {
	w := &dashboardWhere{
		args:   []any{f.From, f.To},
		clause: fmt.Sprintf("%s.event_time >= $1 AND %s.event_time < $2", alias, alias),
	}
	if f.DepartmentID != nil {
		w.args = append(w.args, *f.DepartmentID)
		w.clause += fmt.Sprintf(" AND %s.department_id = $%d", alias, len(w.args))
	}
	if f.ProviderID != nil {
		w.args = append(w.args, *f.ProviderID)
		w.clause += fmt.Sprintf(" AND %s.provider_id = $%d", alias, len(w.args))
	}
	return w
}

// Summary runs the dashboard aggregates concurrently. Each query uses the
// existing audit time index and bounded LIMITs.
func (s *DashboardStore) Summary(ctx context.Context, current, previous store.DashboardFilter, interval string, now time.Time) (store.DashboardData, error) {
	var (
		kpi        store.DashboardKPI
		prevKPI    store.DashboardKPI
		trend      []store.DashboardTrendPoint
		prevTrend  []store.DashboardTrendPoint
		providers  []store.DashboardProviderStat
		models     []store.DashboardModelStat
		modelTrend []modelTrendSeries
		users      []store.DashboardUserStat
		dlpActions []store.DashboardDLPActionStat
		dlpRules   []store.DashboardDLPRuleStat
		incidents  []store.DashboardIncident
		quotas     []store.DashboardQuota
	)

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(6)

	g.Go(func() (err error) {
		kpi, err = s.dashboardKPI(gctx, current)
		return err
	})
	g.Go(func() (err error) {
		prevKPI, err = s.dashboardKPI(gctx, previous)
		return err
	})
	g.Go(func() (err error) {
		trend, err = s.dashboardTrend(gctx, current, interval)
		return err
	})
	g.Go(func() (err error) {
		prevTrend, err = s.dashboardTrend(gctx, previous, interval)
		return err
	})
	g.Go(func() (err error) {
		providers, err = s.dashboardProviders(gctx, current)
		return err
	})
	g.Go(func() (err error) {
		models, err = s.dashboardModels(gctx, current)
		return err
	})
	g.Go(func() (err error) {
		modelTrend, err = s.dashboardModelTrends(gctx, current, interval)
		return err
	})
	g.Go(func() (err error) {
		users, err = s.dashboardUsers(gctx, current)
		return err
	})
	g.Go(func() (err error) {
		dlpActions, err = s.dashboardDLPActions(gctx, current)
		return err
	})
	g.Go(func() (err error) {
		dlpRules, err = s.dashboardDLPRules(gctx, current)
		return err
	})
	g.Go(func() (err error) {
		incidents, err = s.dashboardIncidents(gctx, current)
		return err
	})
	g.Go(func() (err error) {
		quotas, err = s.dashboardQuotas(gctx, now, current.DepartmentID)
		return err
	})

	if err := g.Wait(); err != nil {
		return store.DashboardData{}, err
	}

	pressure, userQuotas := summarizeQuotas(quotas)
	trendByModel := make(map[string][]store.DashboardModelTrendPoint, len(modelTrend))
	for _, series := range modelTrend {
		trendByModel[series.model] = series.points
	}
	for i := range models {
		models[i].Trend = trendByModel[models[i].Model]
	}
	return store.DashboardData{
		KPI:           kpi,
		PreviousKPI:   prevKPI,
		Trend:         trend,
		PreviousTrend: prevTrend,
		Providers:     providers,
		Models:        models,
		Users:         users,
		DLPByAction:   dlpActions,
		DLPByRule:     dlpRules,
		Incidents:     incidents,
		QuotaPressure: pressure,
		UserQuotas:    userQuotas,
	}, nil
}

func (s *DashboardStore) dashboardKPI(ctx context.Context, f store.DashboardFilter) (store.DashboardKPI, error) {
	w := newDashboardWhere("a", f)
	var k store.DashboardKPI
	err := s.pool.QueryRow(ctx, `
		SELECT count(*),
			count(DISTINCT a.employee_id),
			coalesce(sum(a.total_tokens),0),
			coalesce(sum(a.prompt_tokens),0),
			coalesce(sum(a.completion_tokens),0),
			count(*) FILTER (WHERE a.status_code >= 400 AND a.status_code <> 499),
			count(*) FILTER (WHERE a.status_code = 499 OR a.client_disconnected),
			count(*) FILTER (WHERE a.dlp_action <> 'none'),
			coalesce(sum(jsonb_array_length(a.dlp_hits)),0),
			coalesce((percentile_cont(0.95) WITHIN GROUP (ORDER BY a.latency_ms)
				FILTER (WHERE a.latency_ms > 0))::bigint,0)
		FROM audit_logs a
		WHERE `+w.clause, w.args...).
		Scan(&k.Requests, &k.ActiveUsers, &k.Tokens, &k.PromptTokens, &k.CompletionTokens,
			&k.Errors, &k.ClientDisconnects, &k.DLPRequests, &k.DLPHits, &k.P95LatencyMS)
	return k, err
}

func (s *DashboardStore) dashboardTrend(ctx context.Context, f store.DashboardFilter, interval string) ([]store.DashboardTrendPoint, error) {
	unit := "hour"
	pgFormat := "YYYY-MM-DD HH24:00"
	layout := "2006-01-02 15:04"
	if interval == "day" {
		unit = "day"
		pgFormat = "YYYY-MM-DD"
		layout = "2006-01-02"
	}
	w := newDashboardWhere("a", f)
	q := fmt.Sprintf(`
		SELECT to_char(date_trunc('%s', a.event_time AT TIME ZONE 'Asia/Shanghai'), '%s') AS bucket,
			count(*),
			count(*) FILTER (WHERE a.status_code >= 400 AND a.status_code <> 499),
			coalesce(sum(a.total_tokens),0),
			coalesce(sum(a.prompt_tokens),0),
			coalesce(sum(a.completion_tokens),0),
			count(*) FILTER (WHERE a.dlp_action <> 'none'),
			(percentile_cont(0.95) WITHIN GROUP (ORDER BY a.latency_ms)
				FILTER (WHERE a.latency_ms > 0))::bigint
		FROM audit_logs a
		WHERE %s
		GROUP BY bucket
		ORDER BY bucket`, unit, pgFormat, w.clause)
	rows, err := s.pool.Query(ctx, q, w.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []store.DashboardTrendPoint{}
	for rows.Next() {
		var raw string
		var p store.DashboardTrendPoint
		var p95 *int64
		if err := rows.Scan(&raw, &p.Requests, &p.Errors, &p.Tokens,
			&p.PromptTokens, &p.CompletionTokens, &p.DLPRequests, &p95); err != nil {
			return nil, err
		}
		p.Bucket, err = time.ParseInLocation(layout, raw, shanghai)
		if err != nil {
			return nil, fmt.Errorf("parse dashboard bucket %q: %w", raw, err)
		}
		if p.Requests >= 20 {
			p.P95LatencyMS = p95
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *DashboardStore) dashboardProviders(ctx context.Context, f store.DashboardFilter) ([]store.DashboardProviderStat, error) {
	w := newDashboardWhere("a", f)
	rows, err := s.pool.Query(ctx, `
		SELECT coalesce(a.provider_id::text,''), coalesce(p.name,''),
			count(*), coalesce(sum(a.total_tokens),0),
			count(*) FILTER (WHERE a.status_code >= 400 AND a.status_code <> 499),
			coalesce((percentile_cont(0.95) WITHIN GROUP (ORDER BY a.latency_ms)
				FILTER (WHERE a.latency_ms > 0))::bigint,0)
		FROM audit_logs a
		LEFT JOIN upstream_providers p ON p.id = a.provider_id
		WHERE `+w.clause+`
		GROUP BY a.provider_id, p.name
		ORDER BY count(*) DESC, coalesce(p.name,'') ASC
		LIMIT 10`, w.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []store.DashboardProviderStat{}
	for rows.Next() {
		var p store.DashboardProviderStat
		if err := rows.Scan(&p.ID, &p.Name, &p.Requests, &p.Tokens, &p.Errors, &p.P95LatencyMS); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *DashboardStore) dashboardModels(ctx context.Context, f store.DashboardFilter) ([]store.DashboardModelStat, error) {
	w := newDashboardWhere("a", f)
	rows, err := s.pool.Query(ctx, `
		SELECT coalesce(nullif(a.model,''),'未知'), count(*), coalesce(sum(a.total_tokens),0),
			count(*) FILTER (WHERE a.status_code >= 400 AND a.status_code <> 499)
		FROM audit_logs a
		WHERE `+w.clause+`
		GROUP BY 1
		ORDER BY count(*) DESC, 1 ASC
		LIMIT 10`, w.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []store.DashboardModelStat{}
	for rows.Next() {
		var m store.DashboardModelStat
		if err := rows.Scan(&m.Model, &m.Requests, &m.Tokens, &m.Errors); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

type modelTrendSeries struct {
	model  string
	points []store.DashboardModelTrendPoint
}

// dashboardModelTrends returns per-bucket usage grouped by requested model so
// the console can render a sparkline for each model in the breakdown.
func (s *DashboardStore) dashboardModelTrends(ctx context.Context, f store.DashboardFilter, interval string) ([]modelTrendSeries, error) {
	unit := "hour"
	pgFormat := "YYYY-MM-DD HH24:00"
	layout := "2006-01-02 15:04"
	if interval == "day" {
		unit = "day"
		pgFormat = "YYYY-MM-DD"
		layout = "2006-01-02"
	}
	w := newDashboardWhere("a", f)
	q := fmt.Sprintf(`
		SELECT coalesce(nullif(a.model,''),'未知') AS model,
			to_char(date_trunc('%s', a.event_time AT TIME ZONE 'Asia/Shanghai'), '%s') AS bucket,
			count(*),
			coalesce(sum(a.total_tokens),0),
			count(*) FILTER (WHERE a.status_code >= 400 AND a.status_code <> 499)
		FROM audit_logs a
		WHERE %s
		GROUP BY 1, 2
		ORDER BY 1, 2`, unit, pgFormat, w.clause)
	rows, err := s.pool.Query(ctx, q, w.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []modelTrendSeries{}
	for rows.Next() {
		var model, raw string
		var point store.DashboardModelTrendPoint
		if err := rows.Scan(&model, &raw, &point.Requests, &point.Tokens, &point.Errors); err != nil {
			return nil, err
		}
		point.Bucket, err = time.ParseInLocation(layout, raw, shanghai)
		if err != nil {
			return nil, fmt.Errorf("parse model bucket %q: %w", raw, err)
		}
		if len(out) == 0 || out[len(out)-1].model != model {
			out = append(out, modelTrendSeries{model: model})
		}
		out[len(out)-1].points = append(out[len(out)-1].points, point)
	}
	return out, rows.Err()
}

func (s *DashboardStore) dashboardUsers(ctx context.Context, f store.DashboardFilter) ([]store.DashboardUserStat, error) {
	w := newDashboardWhere("a", f)
	rows, err := s.pool.Query(ctx, `
		SELECT a.employee_id, coalesce(e.employee_no,''), coalesce(e.name,''), coalesce(d.name,''),
			count(*), coalesce(sum(a.total_tokens),0),
			count(*) FILTER (WHERE a.status_code >= 400 AND a.status_code <> 499),
			count(*) FILTER (WHERE a.dlp_action <> 'none')
		FROM audit_logs a
		LEFT JOIN employees e ON e.id = a.employee_id
		LEFT JOIN departments d ON d.id = e.department_id
		WHERE `+w.clause+` AND a.employee_id IS NOT NULL
		GROUP BY a.employee_id, e.employee_no, e.name, d.name
		ORDER BY sum(a.total_tokens) DESC, count(*) DESC
		LIMIT 10`, w.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []store.DashboardUserStat{}
	for rows.Next() {
		var u store.DashboardUserStat
		if err := rows.Scan(&u.EmployeeID, &u.EmployeeNo, &u.Name, &u.Department,
			&u.Requests, &u.Tokens, &u.Errors, &u.DLPRequests); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *DashboardStore) dashboardDLPActions(ctx context.Context, f store.DashboardFilter) ([]store.DashboardDLPActionStat, error) {
	w := newDashboardWhere("a", f)
	rows, err := s.pool.Query(ctx, `
		SELECT a.dlp_action, count(*), coalesce(sum(jsonb_array_length(a.dlp_hits)),0)
		FROM audit_logs a
		WHERE `+w.clause+` AND a.dlp_action <> 'none'
		GROUP BY a.dlp_action
		ORDER BY count(*) DESC, a.dlp_action ASC`, w.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []store.DashboardDLPActionStat{}
	for rows.Next() {
		var a store.DashboardDLPActionStat
		if err := rows.Scan(&a.Action, &a.Requests, &a.Hits); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *DashboardStore) dashboardDLPRules(ctx context.Context, f store.DashboardFilter) ([]store.DashboardDLPRuleStat, error) {
	w := newDashboardWhere("a", f)
	rows, err := s.pool.Query(ctx, `
		SELECT coalesce(elem->>'rule_name','未知'), coalesce(elem->>'action',''),
			count(*)
		FROM audit_logs a
		CROSS JOIN LATERAL jsonb_array_elements(a.dlp_hits) elem
		WHERE `+w.clause+`
		GROUP BY 1, 2
		ORDER BY count(*) DESC, 1 ASC
		LIMIT 10`, w.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []store.DashboardDLPRuleStat{}
	for rows.Next() {
		var r store.DashboardDLPRuleStat
		if err := rows.Scan(&r.RuleName, &r.Action, &r.Hits); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *DashboardStore) dashboardIncidents(ctx context.Context, f store.DashboardFilter) ([]store.DashboardIncident, error) {
	w := newDashboardWhere("a", f)
	rows, err := s.pool.Query(ctx, `
		SELECT a.id, a.event_time, a.employee_id, coalesce(e.name,''),
			a.dlp_action, a.dlp_hits, coalesce(a.model,''), a.request_id
		FROM audit_logs a
		LEFT JOIN employees e ON e.id = a.employee_id
		WHERE `+w.clause+` AND a.dlp_action <> 'none'
		ORDER BY a.event_time DESC, a.id DESC
		LIMIT 20`, w.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []store.DashboardIncident{}
	for rows.Next() {
		var (
			incident store.DashboardIncident
			hits     []byte
		)
		if err := rows.Scan(&incident.ID, &incident.EventTime, &incident.EmployeeID,
			&incident.EmployeeName, &incident.Action, &hits, &incident.Model,
			&incident.RequestID); err != nil {
			return nil, err
		}
		incident.Rules = incidentRuleNames(hits)
		out = append(out, incident)
	}
	return out, rows.Err()
}

func incidentRuleNames(raw []byte) []string {
	var hits []struct {
		RuleName string `json:"rule_name"`
	}
	if err := json.Unmarshal(raw, &hits); err != nil {
		return []string{}
	}
	seen := map[string]struct{}{}
	out := []string{}
	for _, hit := range hits {
		name := hit.RuleName
		if name == "" {
			name = "未知"
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

func (s *DashboardStore) dashboardQuotas(ctx context.Context, now time.Time, departmentID *int64) ([]store.DashboardQuota, error) {
	args := []any{}
	q := `
		SELECT q.employee_id, coalesce(e.name,''), coalesce(d.name,''),
			q.quota_type, q.quota_period, q.quota_used, q.quota_total, q.period_start
		FROM employee_quotas q
		JOIN employees e ON e.id = q.employee_id
		LEFT JOIN departments d ON d.id = e.department_id
		WHERE e.status = 1 AND q.quota_total > 0`
	if departmentID != nil {
		args = append(args, *departmentID)
		q += fmt.Sprintf(" AND e.department_id = $%d", len(args))
	}
	q += " ORDER BY q.quota_used::numeric / NULLIF(q.quota_total,0) DESC, q.quota_used DESC"

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []store.DashboardQuota{}
	for rows.Next() {
		var q store.DashboardQuota
		var start time.Time
		if err := rows.Scan(&q.EmployeeID, &q.EmployeeName, &q.Department,
			&q.QuotaType, &q.QuotaPeriod, &q.Used, &q.Total, &start); err != nil {
			return nil, err
		}
		q.PeriodStart = &start
		applyQuotaReset(&q, now)
		out = append(out, q)
	}
	return out, rows.Err()
}

// applyQuotaReset mirrors ReserveQuota: daily/monthly counters roll over at
// the Shanghai period boundary, while total quotas never reset.
func applyQuotaReset(q *store.DashboardQuota, now time.Time) {
	if q.QuotaPeriod != "daily" && q.QuotaPeriod != "monthly" {
		return
	}
	start := periodStart(q.QuotaPeriod, now)
	if q.PeriodStart == nil || q.PeriodStart.Before(start) {
		q.Used = 0
	}
}

func summarizeQuotas(quotas []store.DashboardQuota) ([]store.DashboardQuota, map[uuid.UUID]store.DashboardQuota) {
	pressure := []store.DashboardQuota{}
	userQuotas := map[uuid.UUID]store.DashboardQuota{}
	for _, q := range quotas {
		if q.Total <= 0 {
			continue
		}
		pct := float64(q.Used) / float64(q.Total)
		if pct >= dashboardQuotaPressureThreshold {
			pressure = append(pressure, q)
		}
		current, ok := userQuotas[q.EmployeeID]
		if !ok || quotaPct(q) > quotaPct(current) {
			userQuotas[q.EmployeeID] = q
		}
	}
	sort.SliceStable(pressure, func(i, j int) bool {
		pi, pj := quotaPct(pressure[i]), quotaPct(pressure[j])
		if pi == pj {
			return pressure[i].Used > pressure[j].Used
		}
		return pi > pj
	})
	if len(pressure) > dashboardQuotaLimit {
		pressure = pressure[:dashboardQuotaLimit]
	}
	return pressure, userQuotas
}

func quotaPct(q store.DashboardQuota) float64 {
	if q.Total <= 0 {
		return 0
	}
	return math.Min(1, float64(q.Used)/float64(q.Total))
}
