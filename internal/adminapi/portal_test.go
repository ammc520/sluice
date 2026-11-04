package adminapi

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/company/api-key/internal/store"
)

func (e *adminEnv) seedTokenRequest(t *testing.T, reason string) *store.TokenRequest {
	t.Helper()
	return e.seedTokenRequestKind(t, reason, "quota")
}

func (e *adminEnv) seedTokenRequestKind(t *testing.T, reason, kind string) *store.TokenRequest {
	t.Helper()
	id, err := uuid.Parse(e.employeeID)
	if err != nil {
		t.Fatalf("parse employee id: %v", err)
	}
	total := int64(300_000)
	req := &store.TokenRequest{
		EmployeeID:     id,
		Kind:           kind,
		Reason:         reason,
		RequestedTotal: &total,
		ExpiresAt:      time.Now().Add(14 * 24 * time.Hour),
	}
	if err := e.store.TokenRequests.Create(context.Background(), req); err != nil {
		t.Fatalf("seed token request: %v", err)
	}
	return req
}

func TestTokenRequestListAndApprovalRBAC(t *testing.T) {
	env := newAdminEnv(t)
	req := env.seedTokenRequest(t, "need a token for the internal analytics job")

	// Auditors can read but never decide.
	rr := env.do("auditor", http.MethodGet, "/admin/api/token-requests", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("list as auditor: %d %s", rr.Code, rr.Body.String())
	}
	var page struct {
		Items []adminTokenRequestDTO `json:"items"`
		Total int                    `json:"total"`
	}
	decodeData(t, rr, &page)
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].EmployeeNo != "E001" {
		t.Fatalf("list page: %+v", page)
	}

	rr = env.do("auditor", http.MethodPost, "/admin/api/token-requests/"+req.ID.String()+"/approve",
		`{"quota_total":100000}`)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("auditor approve: %d %s", rr.Code, rr.Body.String())
	}
	rr = env.do("auditor", http.MethodPost, "/admin/api/token-requests/"+req.ID.String()+"/deny",
		`{"note":"no"}`)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("auditor deny: %d %s", rr.Code, rr.Body.String())
	}

	// Validation happens before the request is touched.
	rr = env.do("operator", http.MethodPost, "/admin/api/token-requests/"+req.ID.String()+"/approve",
		`{"quota_total":0}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("zero quota: %d %s", rr.Code, rr.Body.String())
	}
	rr = env.do("operator", http.MethodPost, "/admin/api/token-requests/"+req.ID.String()+"/approve",
		`{"quota_total":6000000}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("over policy max: %d %s", rr.Code, rr.Body.String())
	}
	rr = env.do("operator", http.MethodPost, "/admin/api/token-requests/"+req.ID.String()+"/approve",
		`{"quota_period":"yearly"}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("bad quota period: %d %s", rr.Code, rr.Body.String())
	}

	// Approving a quota request raises the employee allowance immediately and
	// does not issue a key, so the request ends as 'granted'.
	rr = env.do("operator", http.MethodPost, "/admin/api/token-requests/"+req.ID.String()+"/approve",
		`{"quota_period":"monthly","quota_total":250000,"note":"ok"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("approve: %d %s", rr.Code, rr.Body.String())
	}
	var approved adminTokenRequestDTO
	decodeData(t, rr, &approved)
	if approved.Kind != "quota" || approved.Status != "granted" ||
		approved.GrantedTotal == nil || *approved.GrantedTotal != 250_000 {
		t.Fatalf("approved dto: %+v", approved)
	}
	if approved.TokenID != nil {
		t.Fatalf("quota approval must not create a key: %+v", approved.TokenID)
	}
	if approved.ReviewNote != "ok" || approved.ReviewedAt == nil {
		t.Fatalf("review metadata: %+v", approved)
	}
	var quotaTotal, quotaUsed int64
	var quotaPeriod string
	if err := env.pool.QueryRow(context.Background(),
		`SELECT quota_total, quota_used, quota_period FROM employee_quotas WHERE employee_id=$1`,
		req.EmployeeID).Scan(&quotaTotal, &quotaUsed, &quotaPeriod); err != nil {
		t.Fatalf("employee quota: %v", err)
	}
	if quotaTotal != 250_000 || quotaUsed != 0 || quotaPeriod != "monthly" {
		t.Fatalf("employee quota total=%d used=%d period=%s", quotaTotal, quotaUsed, quotaPeriod)
	}

	rr = env.do("operator", http.MethodPost, "/admin/api/token-requests/"+req.ID.String()+"/approve",
		`{"quota_total":100000}`)
	if rr.Code != http.StatusConflict {
		t.Fatalf("double approve: %d %s", rr.Code, rr.Body.String())
	}
	rr = env.do("operator", http.MethodPost, "/admin/api/token-requests/"+req.ID.String()+"/deny",
		`{"note":"too late"}`)
	if rr.Code != http.StatusConflict {
		t.Fatalf("deny approved: %d %s", rr.Code, rr.Body.String())
	}

	// Free the partial unique index before seeding a second active request.
	if _, err := env.pool.Exec(context.Background(),
		`UPDATE token_requests SET expires_at = now() - interval '1 minute' WHERE id=$1`, req.ID); err != nil {
		t.Fatalf("expire first request: %v", err)
	}

	// Denial requires a reason and only applies to pending rows.
	second := env.seedTokenRequest(t, "second request for denial testing")
	rr = env.do("operator", http.MethodPost, "/admin/api/token-requests/"+second.ID.String()+"/deny",
		`{"note":"   "}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("empty deny note: %d %s", rr.Code, rr.Body.String())
	}
	rr = env.do("operator", http.MethodPost, "/admin/api/token-requests/"+second.ID.String()+"/deny",
		`{"note":"quota policy exceeded"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("deny: %d %s", rr.Code, rr.Body.String())
	}
	var denied adminTokenRequestDTO
	decodeData(t, rr, &denied)
	if denied.Status != "denied" || denied.ReviewNote != "quota policy exceeded" {
		t.Fatalf("denied dto: %+v", denied)
	}

	// Unknown request ids are 404, not a leaked state.
	rr = env.do("operator", http.MethodPost, "/admin/api/token-requests/"+uuid.NewString()+"/approve",
		`{"quota_total":100000}`)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown request: %d %s", rr.Code, rr.Body.String())
	}
}

// Legacy rows created before the employee-quota migration keep the claim flow:
// approval grants a key rather than changing the employee allowance.
func TestLegacyTokenRequestApproval(t *testing.T) {
	env := newAdminEnv(t)
	req := env.seedTokenRequestKind(t, "legacy token request", "token")

	rr := env.do("operator", http.MethodPost, "/admin/api/token-requests/"+req.ID.String()+"/approve",
		`{"quota_type":"tokens","quota_period":"monthly","quota_total":250000,"rate_limit_rpm":30,"note":"ok"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("approve: %d %s", rr.Code, rr.Body.String())
	}
	var approved adminTokenRequestDTO
	decodeData(t, rr, &approved)
	if approved.Kind != "token" || approved.Status != "approved" ||
		approved.GrantedTotal == nil || *approved.GrantedTotal != 250_000 {
		t.Fatalf("approved dto: %+v", approved)
	}
	if approved.GrantedRPM == nil || *approved.GrantedRPM != 30 {
		t.Fatalf("granted rpm: %+v", approved.GrantedRPM)
	}
	// An omitted expires_at falls back to the portal policy (180 days), never
	// an unbounded token.
	if approved.GrantedExpiresAt == nil {
		t.Fatal("granted token expiry should default from portal policy")
	}
	if days := time.Until(*approved.GrantedExpiresAt).Hours() / 24; days < 179 || days > 181 {
		t.Fatalf("granted token expiry = %.1f days, want ~180", days)
	}
	// Default claim window is 72 hours.
	if until := time.Until(approved.ExpiresAt); until < 71*time.Hour || until > 73*time.Hour {
		t.Fatalf("claim deadline=%v, want ~72h", until)
	}
}

func TestTokenRequestListFilters(t *testing.T) {
	env := newAdminEnv(t)
	req := env.seedTokenRequest(t, "filtering request")
	rr := env.do("operator", http.MethodPost, "/admin/api/token-requests/"+req.ID.String()+"/approve",
		`{"quota_total":100000}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("approve: %d %s", rr.Code, rr.Body.String())
	}

	rr = env.do("operator", http.MethodGet, "/admin/api/token-requests?status=granted", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("filter list: %d %s", rr.Code, rr.Body.String())
	}
	var page struct {
		Total int `json:"total"`
	}
	decodeData(t, rr, &page)
	if page.Total != 1 {
		t.Fatalf("approved filter total=%d", page.Total)
	}

	rr = env.do("operator", http.MethodGet, "/admin/api/token-requests?status=bogus", "")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("invalid status filter: %d", rr.Code)
	}
	rr = env.do("operator", http.MethodGet, "/admin/api/token-requests?employee_id=not-a-uuid", "")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("invalid employee filter: %d", rr.Code)
	}
}

func TestPortalAccountProvisioning(t *testing.T) {
	env := newAdminEnv(t)
	accountPath := "/admin/api/employees/" + env.employeeID + "/portal-account"

	rr := env.do("operator", http.MethodGet, accountPath, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("get missing account: %d %s", rr.Code, rr.Body.String())
	}
	var status struct {
		Exists bool `json:"exists"`
	}
	decodeData(t, rr, &status)
	if status.Exists {
		t.Fatalf("account unexpectedly exists")
	}

	// Auditors cannot provision or reset credentials.
	if rr := env.do("auditor", http.MethodPost, accountPath, ""); rr.Code != http.StatusForbidden {
		t.Fatalf("auditor create: %d", rr.Code)
	}
	if rr := env.do("auditor", http.MethodPost, accountPath+"/reset-password", ""); rr.Code != http.StatusForbidden {
		t.Fatalf("auditor reset: %d", rr.Code)
	}
	if rr := env.do("auditor", http.MethodDelete, accountPath, ""); rr.Code != http.StatusForbidden {
		t.Fatalf("auditor delete: %d", rr.Code)
	}

	rr = env.do("operator", http.MethodPost, accountPath, "")
	if rr.Code != http.StatusCreated {
		t.Fatalf("create account: %d %s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("initial password response Cache-Control=%q", got)
	}
	var created struct {
		EmployeeID      string `json:"employee_id"`
		InitialPassword string `json:"initial_password"`
	}
	decodeData(t, rr, &created)
	if created.EmployeeID != env.employeeID || len(created.InitialPassword) != 16 {
		t.Fatalf("created account: %+v", created)
	}

	// The one-time password is not echoed by any read endpoint.
	rr = env.do("operator", http.MethodGet, accountPath, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("get account: %d %s", rr.Code, rr.Body.String())
	}
	var got struct {
		Exists             bool `json:"exists"`
		MustChangePassword bool `json:"must_change_password"`
	}
	decodeData(t, rr, &got)
	if !got.Exists || !got.MustChangePassword {
		t.Fatalf("account state: %+v", got)
	}
	if rr.Body.String() == created.InitialPassword {
		t.Fatalf("account read echoed the password")
	}

	rr = env.do("operator", http.MethodPost, accountPath, "")
	if rr.Code != http.StatusConflict {
		t.Fatalf("duplicate account: %d %s", rr.Code, rr.Body.String())
	}

	rr = env.do("operator", http.MethodPost, accountPath+"/reset-password", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("reset password: %d %s", rr.Code, rr.Body.String())
	}
	var reset struct {
		InitialPassword string `json:"initial_password"`
	}
	decodeData(t, rr, &reset)
	if len(reset.InitialPassword) != 16 || reset.InitialPassword == created.InitialPassword {
		t.Fatalf("reset did not issue a fresh password")
	}

	rr = env.do("operator", http.MethodDelete, accountPath, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("delete account: %d %s", rr.Code, rr.Body.String())
	}
	rr = env.do("operator", http.MethodGet, accountPath, "")
	decodeData(t, rr, &status)
	if status.Exists {
		t.Fatalf("account still exists after delete")
	}
	if rr := env.do("operator", http.MethodDelete, accountPath, ""); rr.Code != http.StatusNotFound {
		t.Fatalf("delete missing account: %d", rr.Code)
	}
}

func TestPortalSettingsValidation(t *testing.T) {
	env := newAdminEnv(t)

	rr := env.do("super_admin", http.MethodGet, "/admin/api/settings/portal", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("get portal settings: %d %s", rr.Code, rr.Body.String())
	}
	var settings struct {
		Enabled bool `json:"enabled"`
	}
	decodeData(t, rr, &settings)
	if settings.Enabled {
		t.Fatalf("portal must start disabled")
	}

	if rr := env.do("operator", http.MethodPut, "/admin/api/settings/portal", `{"enabled":true}`); rr.Code != http.StatusForbidden {
		t.Fatalf("operator update settings: %d", rr.Code)
	}

	// Normalization fills defaults, then validation catches bad values.
	rr = env.do("super_admin", http.MethodPut, "/admin/api/settings/portal",
		`{"enabled":true,"default_quota":{"type":"tokens","period":"monthly","total":1000,"rpm":0,"expires_days":30}}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("normalized update: %d %s", rr.Code, rr.Body.String())
	}

	rr = env.do("super_admin", http.MethodPut, "/admin/api/settings/portal",
		`{"enabled":true,"default_quota":{"type":"bogus","period":"monthly","total":1000,"rpm":10,"expires_days":30}}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("invalid quota type: %d %s", rr.Code, rr.Body.String())
	}
	rr = env.do("super_admin", http.MethodPut, "/admin/api/settings/portal",
		`{"enabled":true,"default_quota":{"type":"tokens","period":"monthly","total":1000,"rpm":10,"expires_days":30},"max_request_total":2000000000,"request_ttl_days":14,"claim_ttl_hours":72,"usage_max_days":90}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("out-of-range max_request_total: %d %s", rr.Code, rr.Body.String())
	}

	rr = env.do("super_admin", http.MethodPut, "/admin/api/settings/portal",
		`{"enabled":true,"default_quota":{"type":"tokens","period":"monthly","total":2000000,"rpm":60,"expires_days":180},"max_request_total":5000000,"request_ttl_days":14,"claim_ttl_hours":72,"usage_max_days":90}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("valid update: %d %s", rr.Code, rr.Body.String())
	}

	rr = env.do("super_admin", http.MethodGet, "/admin/api/settings/portal", "")
	var full struct {
		Enabled      bool  `json:"enabled"`
		MaxRequest   int64 `json:"max_request_total"`
		DefaultQuota struct {
			Total int64 `json:"total"`
		} `json:"default_quota"`
	}
	decodeData(t, rr, &full)
	if !full.Enabled || full.MaxRequest != 5_000_000 || full.DefaultQuota.Total != 2_000_000 {
		t.Fatalf("updated settings: %+v", full)
	}
}
