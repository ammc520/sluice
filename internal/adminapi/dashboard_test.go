package adminapi

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/company/api-key/internal/store"
)

func cstTime(year int, month time.Month, day, hour int) time.Time {
	return time.Date(year, month, day, hour, 0, 0, 0, shanghaiLocation())
}

func dashboardURL(rawPath string, from, to time.Time) string {
	parsed, err := url.Parse(rawPath)
	if err != nil {
		panic(err)
	}
	q := parsed.Query()
	q.Set("from", from.Format(time.RFC3339))
	q.Set("to", to.Format(time.RFC3339))
	parsed.RawQuery = q.Encode()
	return parsed.String()
}

func TestDashboardAggregates(t *testing.T) {
	env := newAdminEnv(t)
	dayOne := cstTime(2026, time.November, 1, 10)
	dayTwoMorning := cstTime(2026, time.November, 2, 9)
	dayTwoNoon := cstTime(2026, time.November, 2, 12)

	seedAuditRecord(t, env, dayOne, func(r *store.AuditRecord) {
		r.TotalTokens = 100
		r.PromptTokens = 60
		r.CompletionTokens = 40
	})
	seedAuditRecord(t, env, dayTwoMorning, func(r *store.AuditRecord) {
		r.TotalTokens = 50
		r.PromptTokens = 30
		r.CompletionTokens = 20
		r.StatusCode = http.StatusInternalServerError
		r.DLPAction = "block"
		r.DLPHits = []byte(`[{"rule_id":"r1","rule_name":"aws_access_key","action":"block"}]`)
	})
	seedAuditRecord(t, env, dayTwoNoon, func(r *store.AuditRecord) {
		r.TotalTokens = 30
		r.PromptTokens = 20
		r.CompletionTokens = 10
		r.DLPAction = "block"
		r.DLPHits = []byte(`[
			{"rule_id":"r1","rule_name":"aws_access_key","action":"block"},
			{"rule_id":"r2","rule_name":"email_address","action":"mask"}
		]`)
	})

	from := cstTime(2026, time.November, 1, 0)
	to := cstTime(2026, time.November, 3, 0)

	rr := env.do("auditor", http.MethodGet,
		dashboardURL("/admin/api/dashboard/overview", from, to), "")
	if rr.Code != http.StatusOK {
		t.Fatalf("overview got %d: %s", rr.Code, rr.Body.String())
	}
	var overview dashboardOverviewDTO
	decodeData(t, rr, &overview)
	if overview.TotalCalls != 3 || overview.ActiveUsers != 1 || overview.TotalTokens != 180 ||
		overview.DLPHits != 2 || overview.ErrorCalls != 1 || overview.ErrorRate != 0.3333 {
		t.Fatalf("overview %+v", overview)
	}
	if !overview.From.Equal(from) || !overview.To.Equal(to) {
		t.Fatalf("range %s - %s", overview.From, overview.To)
	}

	rr = env.do("auditor", http.MethodGet,
		dashboardURL("/admin/api/dashboard/trend?interval=day", from, to), "")
	if rr.Code != http.StatusOK {
		t.Fatalf("trend got %d: %s", rr.Code, rr.Body.String())
	}
	var trend struct {
		Interval string          `json:"interval"`
		Items    []trendPointDTO `json:"items"`
	}
	decodeData(t, rr, &trend)
	if trend.Interval != "day" || len(trend.Items) != 2 {
		t.Fatalf("trend %+v", trend)
	}
	if trend.Items[0].Day != "2026-11-01" || trend.Items[0].Calls != 1 || trend.Items[0].Tokens != 100 {
		t.Fatalf("day one %+v", trend.Items[0])
	}
	if trend.Items[1].Day != "2026-11-02" || trend.Items[1].Calls != 2 || trend.Items[1].Tokens != 80 {
		t.Fatalf("day two %+v", trend.Items[1])
	}

	rr = env.do("auditor", http.MethodGet,
		dashboardURL("/admin/api/dashboard/top-users?limit=10", from, to), "")
	if rr.Code != http.StatusOK {
		t.Fatalf("top users got %d: %s", rr.Code, rr.Body.String())
	}
	var top struct {
		Items []topUserDTO `json:"items"`
	}
	decodeData(t, rr, &top)
	if len(top.Items) != 1 || top.Items[0].EmployeeID != env.employeeID ||
		top.Items[0].Name != "Alice" || top.Items[0].Calls != 3 || top.Items[0].Tokens != 180 {
		t.Fatalf("top users %+v", top.Items)
	}

	rr = env.do("auditor", http.MethodGet,
		dashboardURL("/admin/api/dashboard/dlp", from, to), "")
	if rr.Code != http.StatusOK {
		t.Fatalf("dlp got %d: %s", rr.Code, rr.Body.String())
	}
	var dlp struct {
		Items []dlpStatDTO `json:"items"`
	}
	decodeData(t, rr, &dlp)
	stats := map[string]int64{}
	for _, item := range dlp.Items {
		stats[item.RuleName+"/"+item.Action] = item.Hits
	}
	if stats["aws_access_key/block"] != 2 || stats["email_address/mask"] != 1 {
		t.Fatalf("dlp stats %+v", dlp.Items)
	}
}

func TestDashboardDefaultsAndValidation(t *testing.T) {
	env := newAdminEnv(t)
	now := time.Now()
	seedAuditRecord(t, env, now.Add(-time.Hour), func(r *store.AuditRecord) {
		r.TotalTokens = 42
	})
	seedAuditRecord(t, env, now.Add(-30*time.Hour), nil)

	rr := env.do("auditor", http.MethodGet, "/admin/api/dashboard/overview", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("default overview got %d: %s", rr.Code, rr.Body.String())
	}
	var overview dashboardOverviewDTO
	decodeData(t, rr, &overview)
	if overview.TotalCalls != 1 || overview.TotalTokens != 42 {
		t.Fatalf("default range %+v", overview)
	}

	for _, path := range []string{
		"/admin/api/dashboard/trend?interval=hour",
		"/admin/api/dashboard/top-users?limit=0",
		"/admin/api/dashboard/top-users?limit=-1",
		"/admin/api/dashboard/top-users?limit=abc",
		"/admin/api/dashboard/overview?from=garbage",
		"/admin/api/dashboard/dlp?from=2026-11-02T10:00:00Z&to=2026-11-02T10:00:00Z",
	} {
		rr := env.do("auditor", http.MethodGet, path, "")
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("%s got %d: %s", path, rr.Code, rr.Body.String())
		}
	}
}
