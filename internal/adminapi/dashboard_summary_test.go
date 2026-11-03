package adminapi

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/company/api-key/internal/store"
)

func summaryURL(from, to time.Time, extra url.Values) string {
	values := url.Values{}
	values.Set("from", from.Format(time.RFC3339))
	values.Set("to", to.Format(time.RFC3339))
	for key, list := range extra {
		for _, value := range list {
			values.Add(key, value)
		}
	}
	return "/admin/api/dashboard/summary?" + values.Encode()
}

func createEmployeeForTest(t *testing.T, env *adminEnv, no, name string, departmentID int64) string {
	t.Helper()
	body := fmt.Sprintf(`{"employee_no":%q,"name":%q,"email":%q,"department_id":%d,"status":1}`,
		no, name, strings.ToLower(no)+"@example.com", departmentID)
	rr := env.do("super_admin", http.MethodPost, "/admin/api/employees", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create employee: %d %s", rr.Code, rr.Body.String())
	}
	var dto employeeDTO
	decodeData(t, rr, &dto)
	return dto.ID
}

func seedUserAudit(t *testing.T, env *adminEnv, at time.Time, employeeID, providerID string, mutate func(*store.AuditRecord)) {
	t.Helper()
	seedAuditRecord(t, env, at, func(r *store.AuditRecord) {
		if employeeID != "" {
			id := uuid.MustParse(employeeID)
			r.EmployeeID = &id
			if r.DepartmentID == nil {
				var departmentID *int64
				if err := env.pool.QueryRow(context.Background(),
					`SELECT department_id FROM employees WHERE id=$1`, id).Scan(&departmentID); err == nil {
					r.DepartmentID = departmentID
				}
			}
		}
		if providerID != "" {
			id := uuid.MustParse(providerID)
			r.ProviderID = &id
		}
		if mutate != nil {
			mutate(r)
		}
	})
}

func TestDashboardSummaryAggregates(t *testing.T) {
	env := newAdminEnv(t)
	ctx := context.Background()

	var otherDepartmentID int64
	if err := env.pool.QueryRow(ctx,
		`INSERT INTO departments (name) VALUES ('Sales') RETURNING id`).Scan(&otherDepartmentID); err != nil {
		t.Fatal(err)
	}
	bobID := createEmployeeForTest(t, env, "E002", "Bob", otherDepartmentID)

	providerA := env.createProvider(t, "primary", "openai_compatible")
	providerB := env.createProvider(t, "backup", "openai_compatible")

	currentFrom := cstTime(2026, time.November, 2, 0)
	currentTo := cstTime(2026, time.November, 3, 0)

	seedUserAudit(t, env, cstTime(2026, time.November, 2, 10), env.employeeID, providerA, func(r *store.AuditRecord) {
		r.EventTime = cstTime(2026, time.November, 2, 10).Add(15 * time.Minute).UTC()
		r.TotalTokens, r.PromptTokens, r.CompletionTokens = 100, 60, 40
		r.LatencyMS = 100
	})
	seedUserAudit(t, env, cstTime(2026, time.November, 2, 10), env.employeeID, providerA, func(r *store.AuditRecord) {
		r.EventTime = cstTime(2026, time.November, 2, 10).Add(45 * time.Minute).UTC()
		r.TotalTokens, r.PromptTokens, r.CompletionTokens = 50, 30, 20
		r.LatencyMS = 300
		r.StatusCode = http.StatusInternalServerError
		r.DLPAction = "block"
		r.DLPHits = []byte(`[{"rule_id":"r1","rule_name":"aws_access_key","action":"block"}]`)
	})
	seedUserAudit(t, env, cstTime(2026, time.November, 2, 11), env.employeeID, providerA, func(r *store.AuditRecord) {
		r.EventTime = cstTime(2026, time.November, 2, 11).Add(5 * time.Minute).UTC()
		r.TotalTokens, r.PromptTokens, r.CompletionTokens = 30, 20, 10
		r.LatencyMS = 200
		r.StatusCode = 499
		r.ClientDisconnected = true
	})
	seedUserAudit(t, env, cstTime(2026, time.November, 2, 12), bobID, providerB, func(r *store.AuditRecord) {
		r.EventTime = cstTime(2026, time.November, 2, 12).UTC()
		r.Model = "claude-x"
		r.TotalTokens, r.PromptTokens, r.CompletionTokens = 30, 20, 10
		r.LatencyMS = 400
		r.DLPAction = "mask"
		r.DLPHits = []byte(`[{"rule_id":"r2","rule_name":"email_address","action":"mask"}]`)
	})

	seedUserAudit(t, env, cstTime(2026, time.November, 1, 10), env.employeeID, providerA, func(r *store.AuditRecord) {
		r.EventTime = cstTime(2026, time.November, 1, 10).UTC()
		r.TotalTokens, r.PromptTokens, r.CompletionTokens = 60, 40, 20
	})
	seedUserAudit(t, env, cstTime(2026, time.November, 1, 11), bobID, providerB, func(r *store.AuditRecord) {
		r.EventTime = cstTime(2026, time.November, 1, 11).UTC()
		r.TotalTokens, r.PromptTokens, r.CompletionTokens = 20, 10, 10
		r.StatusCode = http.StatusBadRequest
	})

	created := mustCreateToken(t, env, defaultTokenBody(env))
	tokenID := uuid.MustParse(created.TokenID)
	if _, err := env.pool.Exec(ctx,
		`UPDATE tokens SET quota_total=$2, quota_used=$3, period_start=$4, quota_period='monthly' WHERE id=$1`,
		tokenID, 1000, 900, time.Now()); err != nil {
		t.Fatal(err)
	}

	rr := env.do("auditor", http.MethodGet, summaryURL(currentFrom, currentTo, nil), "")
	if rr.Code != http.StatusOK {
		t.Fatalf("summary: %d %s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("X-Dashboard-Cache"); got != "miss" {
		t.Fatalf("cache header %q", got)
	}
	var summary summaryDTO
	decodeData(t, rr, &summary)

	if summary.Range.Interval != "hour" || len(summary.Trend.Buckets) != 24 {
		t.Fatalf("range/trend %+v buckets=%d", summary.Range, len(summary.Trend.Buckets))
	}
	if summary.KPIs.Requests.Value != 4 || summary.KPIs.Requests.Previous != 2 {
		t.Fatalf("requests %+v", summary.KPIs.Requests)
	}
	if summary.KPIs.Requests.DeltaPct == nil || *summary.KPIs.Requests.DeltaPct != 100 {
		t.Fatalf("request delta %+v", summary.KPIs.Requests.DeltaPct)
	}
	if summary.KPIs.Tokens.Value != 210 || summary.KPIs.Tokens.Previous != 80 {
		t.Fatalf("tokens %+v", summary.KPIs.Tokens)
	}
	if summary.KPIs.ActiveUsers.Value != 2 || summary.KPIs.ErrorRate.Value != 0.25 {
		t.Fatalf("users/error rate %+v %+v", summary.KPIs.ActiveUsers, summary.KPIs.ErrorRate)
	}
	if summary.KPIs.ErrorRate.Previous != 0.5 || summary.KPIs.ErrorRate.DeltaPP == nil || *summary.KPIs.ErrorRate.DeltaPP != -25 {
		t.Fatalf("error rate delta %+v", summary.KPIs.ErrorRate)
	}
	if summary.KPIs.ClientDisconnects.Value != 1 {
		t.Fatalf("disconnects %+v", summary.KPIs.ClientDisconnects)
	}
	if summary.DLP.Requests != 2 || summary.DLP.Hits != 2 {
		t.Fatalf("dlp %+v", summary.DLP)
	}
	actions := map[string]int64{}
	for _, item := range summary.DLP.ByAction {
		actions[item.Action] = item.Requests
	}
	if actions["block"] != 1 || actions["mask"] != 1 || actions["audit"] != 0 {
		t.Fatalf("dlp actions %+v", summary.DLP.ByAction)
	}
	if len(summary.DLP.ByRule) != 2 {
		t.Fatalf("dlp rules %+v", summary.DLP.ByRule)
	}

	if summary.Trend.Current[10].Requests != 2 || summary.Trend.Current[10].Tokens != 150 {
		t.Fatalf("current bucket 10 %+v", summary.Trend.Current[10])
	}
	if summary.Trend.Current[11].Requests != 1 || summary.Trend.Current[12].Requests != 1 {
		t.Fatalf("current later buckets %+v", summary.Trend.Current)
	}
	if summary.Trend.Previous[10].Requests != 1 || summary.Trend.Previous[11].Requests != 1 {
		t.Fatalf("previous buckets %+v", summary.Trend.Previous)
	}
	if summary.Trend.Current[10].P95LatencyMS != nil {
		t.Fatalf("low-sample p95 should be null: %+v", summary.Trend.Current[10].P95LatencyMS)
	}

	if len(summary.Breakdown.Providers) != 2 {
		t.Fatalf("providers %+v", summary.Breakdown.Providers)
	}
	if summary.Breakdown.Providers[0].Name != "primary" || summary.Breakdown.Providers[0].Requests != 3 {
		t.Fatalf("provider breakdown %+v", summary.Breakdown.Providers)
	}
	if len(summary.Breakdown.Models) != 2 || summary.Breakdown.Models[0].Model != "gpt-4o" || summary.Breakdown.Models[0].Requests != 3 {
		t.Fatalf("model breakdown %+v", summary.Breakdown.Models)
	}

	if len(summary.TopUsers) != 2 || summary.TopUsers[0].Name != "Alice" || summary.TopUsers[0].Requests != 3 {
		t.Fatalf("top users %+v", summary.TopUsers)
	}
	if summary.TopUsers[0].Quota == nil || summary.TopUsers[0].Quota.Pct != 0.9 {
		t.Fatalf("quota %+v", summary.TopUsers[0].Quota)
	}
	if len(summary.QuotaPressure) != 1 || summary.QuotaPressure[0].Name != "Alice" {
		t.Fatalf("quota pressure %+v", summary.QuotaPressure)
	}

	if len(summary.RecentIncidents) != 2 {
		t.Fatalf("incidents %+v", summary.RecentIncidents)
	}
	if summary.RecentIncidents[0].EmployeeName != "Bob" || summary.RecentIncidents[0].Action != "mask" {
		t.Fatalf("latest incident %+v", summary.RecentIncidents[0])
	}
	if len(summary.RecentIncidents[0].Rules) != 1 || summary.RecentIncidents[0].Rules[0] != "email_address" {
		t.Fatalf("incident rules %+v", summary.RecentIncidents[0].Rules)
	}

	raw := rr.Body.String()
	for _, forbidden := range []string{"prompt_enc", "response_enc", "sk-company-", "sk-real-secret"} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("summary leaked %q: %s", forbidden, raw)
		}
	}

	cached := env.do("auditor", http.MethodGet, summaryURL(currentFrom, currentTo, nil), "")
	if got := cached.Header().Get("X-Dashboard-Cache"); got != "hit" {
		t.Fatalf("second request cache header %q", got)
	}

	filtered := env.do("auditor", http.MethodGet,
		summaryURL(currentFrom, currentTo, url.Values{"department_id": {fmt.Sprint(env.departmentID)}}), "")
	if filtered.Code != http.StatusOK {
		t.Fatalf("department filter: %d %s", filtered.Code, filtered.Body.String())
	}
	var deptSummary summaryDTO
	decodeData(t, filtered, &deptSummary)
	if deptSummary.KPIs.Requests.Value != 3 || deptSummary.KPIs.ActiveUsers.Value != 1 {
		t.Fatalf("department filter %+v", deptSummary.KPIs)
	}

	providerFiltered := env.do("auditor", http.MethodGet,
		summaryURL(currentFrom, currentTo, url.Values{"provider_id": {providerA}}), "")
	if providerFiltered.Code != http.StatusOK {
		t.Fatalf("provider filter: %d %s", providerFiltered.Code, providerFiltered.Body.String())
	}
	var providerSummary summaryDTO
	decodeData(t, providerFiltered, &providerSummary)
	if providerSummary.KPIs.Requests.Value != 3 {
		t.Fatalf("provider filter %+v", providerSummary.KPIs)
	}
}

func TestDashboardSummaryRangesAndValidation(t *testing.T) {
	env := newAdminEnv(t)
	now := time.Now()
	seedUserAudit(t, env, now.Add(-time.Hour), env.employeeID, "", nil)

	for _, tc := range []struct {
		rangeValue string
		interval   string
		buckets    int
	}{
		{"24h", "hour", 24},
		{"7d", "day", 7},
		{"30d", "day", 30},
	} {
		t.Run(tc.rangeValue, func(t *testing.T) {
			rr := env.do("auditor", http.MethodGet,
				"/admin/api/dashboard/summary?range="+tc.rangeValue, "")
			if rr.Code != http.StatusOK {
				t.Fatalf("got %d: %s", rr.Code, rr.Body.String())
			}
			var summary summaryDTO
			decodeData(t, rr, &summary)
			if summary.Range.Interval != tc.interval || len(summary.Trend.Buckets) != tc.buckets {
				t.Fatalf("range %s interval=%s buckets=%d", tc.rangeValue, summary.Range.Interval, len(summary.Trend.Buckets))
			}
		})
	}

	today := env.do("auditor", http.MethodGet, "/admin/api/dashboard/summary?range=today", "")
	if today.Code != http.StatusOK {
		t.Fatalf("today: %d %s", today.Code, today.Body.String())
	}
	var todaySummary summaryDTO
	decodeData(t, today, &todaySummary)
	if todaySummary.Range.Interval != "hour" || len(todaySummary.Trend.Buckets) < 1 || len(todaySummary.Trend.Buckets) > 24 {
		t.Fatalf("today %+v", todaySummary.Range)
	}

	from := now.Add(-48 * time.Hour).Format(time.RFC3339)
	to := now.Format(time.RFC3339)
	for _, path := range []string{
		"/admin/api/dashboard/summary?range=7d&from=" + from,
		"/admin/api/dashboard/summary?range=90d",
		"/admin/api/dashboard/summary?range=7d&interval=week",
		"/admin/api/dashboard/summary?department_id=0",
		"/admin/api/dashboard/summary?provider_id=nope",
		"/admin/api/dashboard/summary?from=" + from,
		"/admin/api/dashboard/summary?from=" + to + "&to=" + to,
		"/admin/api/dashboard/summary?from=" + now.Add(-91*24*time.Hour).Format(time.RFC3339) + "&to=" + to,
	} {
		rr := env.do("auditor", http.MethodGet, path, "")
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("%s got %d: %s", path, rr.Code, rr.Body.String())
		}
	}
}

func TestDepartmentsList(t *testing.T) {
	env := newAdminEnv(t)
	rr := env.do("auditor", http.MethodGet, "/admin/api/departments", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("departments: %d %s", rr.Code, rr.Body.String())
	}
	var result struct {
		Items []departmentDTO `json:"items"`
	}
	decodeData(t, rr, &result)
	if len(result.Items) != 1 || result.Items[0].Name != "Engineering" {
		t.Fatalf("departments %+v", result.Items)
	}
}

func TestAuditDepartmentFilter(t *testing.T) {
	env := newAdminEnv(t)
	seedUserAudit(t, env, time.Now().Add(-time.Hour), env.employeeID, "", nil)
	rr := env.do("auditor", http.MethodGet,
		fmt.Sprintf("/admin/api/audit?department_id=%d", env.departmentID), "")
	if rr.Code != http.StatusOK {
		t.Fatalf("audit department filter: %d %s", rr.Code, rr.Body.String())
	}
	var result struct {
		Items []auditLogDTO `json:"items"`
	}
	decodeData(t, rr, &result)
	if len(result.Items) != 1 {
		t.Fatalf("items %+v", result.Items)
	}
	bad := env.do("auditor", http.MethodGet, "/admin/api/audit?department_id=nope", "")
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("bad department got %d", bad.Code)
	}
}
