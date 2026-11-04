package portalapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/company/sluice/internal/crypto"
	"github.com/company/sluice/internal/portalapi"
	"github.com/company/sluice/internal/portalauth"
	"github.com/company/sluice/internal/portalcfg"
	"github.com/company/sluice/internal/ratelimit"
	"github.com/company/sluice/internal/store"
	"github.com/company/sluice/internal/store/postgres"
	"github.com/company/sluice/internal/testutil"
)

const initialPassword = "initial-password-123"

type envelope struct {
	Data  json.RawMessage `json:"data"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type session struct {
	cookie string
	csrf   string
}

type env struct {
	s      *postgres.Store
	router *gin.Engine
	clock  func() time.Time
}

func newEnv(t *testing.T, enabled bool) *env {
	t.Helper()
	return newEnvWith(t, enabled, nil)
}

func newEnvWith(t *testing.T, enabled bool, mutate func(*portalcfg.Settings)) *env {
	t.Helper()
	gin.SetMode(gin.TestMode)
	s := postgres.New(testutil.OpenTestDB(t))

	if enabled {
		settings := portalcfg.Defaults()
		settings.Enabled = true
		if mutate != nil {
			mutate(&settings)
		}
		raw, err := json.Marshal(settings)
		if err != nil {
			t.Fatalf("marshal settings: %v", err)
		}
		if err := s.Settings.Set(context.Background(), portalcfg.SettingsKey, raw); err != nil {
			t.Fatalf("write portal settings: %v", err)
		}
	}

	clock := time.Now
	provider := portalcfg.NewProvider(s.Settings, time.Minute, clock, nil)
	svc := portalauth.New(s.Accounts, s.Employees, ratelimit.NewMemory(), clock, time.Hour)

	router := gin.New()
	portalapi.Register(router.Group("/portal/api"), portalapi.Dependencies{
		Auth:         svc,
		Tokens:       s.Tokens,
		Requests:     s.TokenRequests,
		Quotas:       s.EmployeeQuotas,
		Audit:        s.Audit,
		AuditLogs:    s.EmployeeAudits,
		Config:       provider,
		Clock:        clock,
		CookieSecure: false,
	})
	return &env{s: s, router: router, clock: clock}
}

func (e *env) seedEmployee(t *testing.T) (*store.Employee, *store.EmployeeAccount) {
	t.Helper()
	emp := &store.Employee{
		ID:         uuid.New(),
		EmployeeNo: "E" + uuid.NewString()[:8],
		Name:       "Employee",
		Status:     1,
	}
	if err := e.s.Employees.Create(context.Background(), emp); err != nil {
		t.Fatalf("create employee: %v", err)
	}
	hash, err := crypto.HashPassword(initialPassword)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	account := &store.EmployeeAccount{
		EmployeeID:         emp.ID,
		PasswordHash:       hash,
		MustChangePassword: false,
	}
	if err := e.s.Accounts.CreateAccount(context.Background(), account); err != nil {
		t.Fatalf("create account: %v", err)
	}
	return emp, account
}

func (e *env) do(method, path string, body any, sess *session, withCSRF bool) *httptest.ResponseRecorder {
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	if sess != nil {
		req.Header.Set("Cookie", sess.cookie)
		if withCSRF {
			req.Header.Set(portalauth.CSRFHeader, sess.csrf)
		}
	}
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

func (e *env) login(t *testing.T, employeeNo, password string) *session {
	t.Helper()
	rec := e.do(http.MethodPost, "/portal/api/auth/login", map[string]string{
		"employee_no": employeeNo,
		"password":    password,
	}, nil, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", rec.Code, rec.Body.String())
	}
	var cookieValues, csrf string
	for _, cookie := range rec.Result().Cookies() {
		switch cookie.Name {
		case portalauth.SessionCookie:
			cookieValues = cookie.Name + "=" + cookie.Value
			if !cookie.HttpOnly {
				t.Fatalf("session cookie is not HttpOnly")
			}
		case portalauth.CSRFCookie:
			cookieValues += "; " + cookie.Name + "=" + cookie.Value
			csrf = cookie.Value
			if cookie.HttpOnly {
				t.Fatalf("CSRF cookie must be readable by the SPA")
			}
		}
		if cookie.Path != portalauth.CookiePath {
			t.Fatalf("cookie %s path=%q, want %q", cookie.Name, cookie.Path, portalauth.CookiePath)
		}
	}
	if cookieValues == "" || csrf == "" {
		t.Fatalf("login did not set both cookies")
	}
	return &session{cookie: cookieValues, csrf: csrf}
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) envelope {
	t.Helper()
	var out envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response %q: %v", rec.Body.String(), err)
	}
	return out
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	out := decode(t, rec)
	if out.Error == nil {
		return ""
	}
	return out.Error.Code
}

func (e *env) seedReviewer(t *testing.T) uuid.UUID {
	t.Helper()
	hash, err := crypto.HashPassword("reviewer-password-123")
	if err != nil {
		t.Fatalf("hash reviewer password: %v", err)
	}
	reviewer := &store.AdminUser{
		Username:     "reviewer-" + uuid.NewString()[:8],
		PasswordHash: hash,
		Role:         "operator",
		Status:       1,
	}
	if err := e.s.Admins.Create(context.Background(), reviewer); err != nil {
		t.Fatalf("create reviewer: %v", err)
	}
	return reviewer.ID
}

func (e *env) seedToken(t *testing.T, employeeID uuid.UUID, marker string) *store.Token {
	t.Helper()
	tok := &store.Token{
		ID:           uuid.New(),
		EmployeeID:   employeeID,
		TokenHash:    crypto.HashToken(marker + uuid.NewString()),
		TokenPrefix:  "sk-company-" + marker,
		TokenLast4:   "abcd",
		QuotaType:    "tokens",
		QuotaPeriod:  "monthly",
		QuotaTotal:   ptr(int64(1000)),
		RateLimitRPM: 60,
		IPAllowlist:  []string{},
		Status:       1,
	}
	if err := e.s.Tokens.Create(context.Background(), tok); err != nil {
		t.Fatalf("seed token: %v", err)
	}
	return tok
}

func (e *env) seedUsage(t *testing.T, employeeID uuid.UUID, marker string) {
	t.Helper()
	err := e.s.Audit.InsertBatch(context.Background(), []*store.AuditRecord{{
		EventTime:  time.Now().Add(-time.Hour),
		RequestID:  marker + "-" + uuid.NewString(),
		EmployeeID: &employeeID,
		Method:     "POST",
		Path:       "/v1/chat/completions",
		Model:      marker + "-model",
		StatusCode: 200,
		LatencyMS:  42,
		PromptEnc:  []byte("ciphertext-should-never-leak"),
		DLPAction:  "none",
		DLPHits:    []byte("[]"),
	}})
	if err != nil {
		t.Fatalf("seed audit: %v", err)
	}
}

func ptr[T any](v T) *T { return &v }

func TestPortalDisabledHidesEveryRoute(t *testing.T) {
	e := newEnv(t, false)
	rec := e.do(http.MethodPost, "/portal/api/auth/login", map[string]string{
		"employee_no": "E1", "password": "x",
	}, nil, false)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("disabled portal login status=%d, want 404", rec.Code)
	}
	if code := errorCode(t, rec); code != "not_found" {
		t.Fatalf("error code=%q", code)
	}

	rec = e.do(http.MethodGet, "/portal/api/me/summary", nil, nil, false)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("disabled portal data status=%d, want 404", rec.Code)
	}
}

func TestUnsupportedMethodRequiresAuthentication(t *testing.T) {
	e := newEnv(t, true)
	for _, path := range []string{
		"/portal/api/auth/me",
		"/portal/api/me/summary",
		"/portal/api/me/tokens",
		"/portal/api/me/token-requests",
	} {
		rec := e.do(http.MethodGet, path, nil, nil, false)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s status=%d, want 401", path, rec.Code)
		}
	}
}

func TestPasswordChangeGateAndCSRF(t *testing.T) {
	e := newEnv(t, true)
	emp, account := e.seedEmployee(t)
	if err := e.s.Accounts.ReplacePassword(context.Background(), emp.ID, account.PasswordHash, true); err != nil {
		t.Fatalf("set must_change_password: %v", err)
	}

	sess := e.login(t, emp.EmployeeNo, initialPassword)

	rec := e.do(http.MethodGet, "/portal/api/auth/me", nil, sess, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("me status=%d", rec.Code)
	}
	var me struct {
		MustChangePassword bool `json:"must_change_password"`
	}
	if err := json.Unmarshal(decode(t, rec).Data, &me); err != nil || !me.MustChangePassword {
		t.Fatalf("me must_change_password: %+v err=%v", me, err)
	}

	// Data endpoints stay closed until the password is replaced.
	rec = e.do(http.MethodGet, "/portal/api/me/summary", nil, sess, false)
	if rec.Code != http.StatusForbidden || errorCode(t, rec) != "password_change_required" {
		t.Fatalf("gate status=%d code=%q", rec.Code, errorCode(t, rec))
	}

	// Unsafe methods without the double-submit header are rejected.
	rec = e.do(http.MethodPost, "/portal/api/auth/change-password", map[string]string{
		"current_password": initialPassword,
		"new_password":     "brand-new-password-456",
	}, sess, false)
	if rec.Code != http.StatusForbidden || errorCode(t, rec) != "csrf_failed" {
		t.Fatalf("csrf status=%d code=%q", rec.Code, errorCode(t, rec))
	}

	rec = e.do(http.MethodPost, "/portal/api/auth/change-password", map[string]string{
		"current_password": initialPassword,
		"new_password":     "short",
	}, sess, true)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("weak password status=%d", rec.Code)
	}

	rec = e.do(http.MethodPost, "/portal/api/auth/change-password", map[string]string{
		"current_password": initialPassword,
		"new_password":     "brand-new-password-456",
	}, sess, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("change password status=%d body=%s", rec.Code, rec.Body.String())
	}

	// Every session is revoked, including the one that changed the password.
	rec = e.do(http.MethodGet, "/portal/api/auth/me", nil, sess, false)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("old session status=%d, want 401", rec.Code)
	}

	sess = e.login(t, emp.EmployeeNo, "brand-new-password-456")
	rec = e.do(http.MethodGet, "/portal/api/auth/me", nil, sess, false)
	var me2 struct {
		MustChangePassword bool `json:"must_change_password"`
	}
	if err := json.Unmarshal(decode(t, rec).Data, &me2); err != nil || me2.MustChangePassword {
		t.Fatalf("must_change_password not cleared: %+v err=%v", me2, err)
	}
	rec = e.do(http.MethodGet, "/portal/api/me/summary", nil, sess, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("summary after change status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestSummaryIncludesModelBreakdown(t *testing.T) {
	e := newEnv(t, true)
	emp, _ := e.seedEmployee(t)
	sess := e.login(t, emp.EmployeeNo, initialPassword)
	e.seedUsage(t, emp.ID, "alpha")
	e.seedUsage(t, emp.ID, "beta")

	rec := e.do(http.MethodGet, "/portal/api/me/summary", nil, sess, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("summary status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Models []struct {
			Model    string `json:"model"`
			Requests int64  `json:"requests"`
			Trend    []struct {
				Day string `json:"day"`
			} `json:"trend"`
		} `json:"models"`
	}
	if err := json.Unmarshal(decode(t, rec).Data, &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Models) != 2 {
		t.Fatalf("models=%d, want 2: %s", len(body.Models), rec.Body.String())
	}
	seen := map[string]bool{}
	for _, m := range body.Models {
		seen[m.Model] = true
		if m.Requests != 1 || len(m.Trend) != 1 {
			t.Fatalf("model breakdown wrong: %+v", m)
		}
	}
	if !seen["alpha-model"] || !seen["beta-model"] {
		t.Fatalf("model names missing: %+v", body.Models)
	}
}

func TestTokenRequestValidationAndIsolation(t *testing.T) {
	e := newEnv(t, true)
	alice, _ := e.seedEmployee(t)
	bob, _ := e.seedEmployee(t)
	aliceSess := e.login(t, alice.EmployeeNo, initialPassword)
	bobSess := e.login(t, bob.EmployeeNo, initialPassword)

	// Attach a token and usage to Bob; Alice must never see either.
	bobToken := e.seedToken(t, bob.ID, "bob")
	e.seedUsage(t, bob.ID, "bob")
	aliceToken := e.seedToken(t, alice.ID, "alice")

	rec := e.do(http.MethodGet, "/portal/api/me/tokens", nil, aliceSess, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("tokens status=%d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, aliceToken.TokenPrefix) {
		t.Fatalf("alice's own token missing: %s", body)
	}
	if strings.Contains(body, bobToken.TokenPrefix) || strings.Contains(body, bobToken.TokenHash) {
		t.Fatalf("alice can see bob's token: %s", body)
	}
	if strings.Contains(body, "hash") {
		t.Fatalf("token hash exposed in portal response: %s", body)
	}

	rec = e.do(http.MethodGet, "/portal/api/me/usage", nil, aliceSess, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("usage status=%d body=%s", rec.Code, rec.Body.String())
	}
	body = rec.Body.String()
	if strings.Contains(body, "bob-model") {
		t.Fatalf("alice sees bob's usage: %s", body)
	}
	if strings.Contains(body, "ciphertext") || strings.Contains(body, "prompt_enc") {
		t.Fatalf("usage leaked captured payloads: %s", body)
	}

	// Bob's request is invisible to Alice.
	bobReq := &store.TokenRequest{
		EmployeeID: bob.ID,
		Reason:     "bob needs a token for his tooling",
		ExpiresAt:  time.Now().Add(24 * time.Hour),
	}
	if err := e.s.TokenRequests.Create(context.Background(), bobReq); err != nil {
		t.Fatalf("create bob request: %v", err)
	}
	if rec := e.do(http.MethodGet, "/portal/api/me/token-requests", nil, bobSess, false); !strings.Contains(rec.Body.String(), bobReq.ID.String()) {
		t.Fatalf("bob cannot see his own request: %s", rec.Body.String())
	}
	if rec := e.do(http.MethodPost, "/portal/api/me/token-requests/"+bobReq.ID.String()+"/cancel", nil, aliceSess, true); rec.Code != http.StatusNotFound {
		t.Fatalf("alice cancelling bob's request status=%d, want 404", rec.Code)
	}
	rec = e.do(http.MethodGet, "/portal/api/me/token-requests", nil, aliceSess, false)
	if strings.Contains(rec.Body.String(), bobReq.ID.String()) {
		t.Fatalf("alice can list bob's request: %s", rec.Body.String())
	}

	// Validation: reason length and requested total.
	rec = e.do(http.MethodPost, "/portal/api/me/token-requests", map[string]any{
		"reason": "short",
	}, aliceSess, true)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("short reason status=%d", rec.Code)
	}
	rec = e.do(http.MethodPost, "/portal/api/me/token-requests", map[string]any{
		"reason":          "need a token for the analytics job",
		"requested_total": int64(0),
	}, aliceSess, true)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("zero total status=%d", rec.Code)
	}

	rec = e.do(http.MethodPost, "/portal/api/me/token-requests", map[string]any{
		"reason":          "need a larger allowance for the analytics job",
		"requested_total": int64(2_000_000),
	}, aliceSess, true)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(decode(t, rec).Data, &created); err != nil || created.Status != "pending" {
		t.Fatalf("created request: %+v err=%v", created, err)
	}

	// Only one live request per employee.
	rec = e.do(http.MethodPost, "/portal/api/me/token-requests", map[string]any{
		"reason":          "a second request while the first is pending",
		"requested_total": int64(3_000_000),
	}, aliceSess, true)
	if rec.Code != http.StatusConflict || errorCode(t, rec) != "active_request_exists" {
		t.Fatalf("duplicate request status=%d code=%q", rec.Code, errorCode(t, rec))
	}

	// CSRF is required for state-changing portal endpoints.
	rec = e.do(http.MethodPost, "/portal/api/me/token-requests", map[string]any{
		"reason": "no csrf header should be rejected",
	}, aliceSess, false)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("missing csrf status=%d", rec.Code)
	}

	// Cancel then cancel again.
	rec = e.do(http.MethodPost, "/portal/api/me/token-requests/"+created.ID+"/cancel", nil, aliceSess, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("cancel status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = e.do(http.MethodPost, "/portal/api/me/token-requests/"+created.ID+"/cancel", nil, aliceSess, true)
	if rec.Code != http.StatusConflict {
		t.Fatalf("double cancel status=%d", rec.Code)
	}
}

func TestClaimReturnsPlaintextExactlyOnce(t *testing.T) {
	e := newEnv(t, true)
	alice, _ := e.seedEmployee(t)
	bob, _ := e.seedEmployee(t)
	aliceSess := e.login(t, alice.EmployeeNo, initialPassword)
	bobSess := e.login(t, bob.EmployeeNo, initialPassword)

	req := &store.TokenRequest{
		EmployeeID: alice.ID,
		Kind:       "token",
		Reason:     "alice needs a production token",
		ExpiresAt:  time.Now().Add(24 * time.Hour),
	}
	if err := e.s.TokenRequests.Create(context.Background(), req); err != nil {
		t.Fatalf("create request: %v", err)
	}
	if _, err := e.s.TokenRequests.Approve(context.Background(), req.ID, e.seedReviewer(t), store.TokenRequestGrant{
		QuotaType:     "tokens",
		QuotaPeriod:   "monthly",
		QuotaTotal:    ptr(int64(500_000)),
		RateLimitRPM:  60,
		Note:          "approved",
		ClaimDeadline: time.Now().Add(72 * time.Hour),
	}, time.Now()); err != nil {
		t.Fatalf("approve: %v", err)
	}

	// Another employee cannot claim it, and the response does not reveal it exists.
	if rec := e.do(http.MethodPost, "/portal/api/me/token-requests/"+req.ID.String()+"/claim", nil, bobSess, true); rec.Code != http.StatusNotFound {
		t.Fatalf("foreign claim status=%d, want 404", rec.Code)
	}

	rec := e.do(http.MethodPost, "/portal/api/me/token-requests/"+req.ID.String()+"/claim", nil, aliceSess, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("claim status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("claim Cache-Control=%q, want no-store", got)
	}
	var claimed struct {
		Token     string `json:"token"`
		TokenID   string `json:"token_id"`
		TokenHash string `json:"token_hash"`
		Request   struct {
			Status string `json:"status"`
		} `json:"request"`
	}
	if err := json.Unmarshal(decode(t, rec).Data, &claimed); err != nil {
		t.Fatalf("decode claim: %v", err)
	}
	if !strings.HasPrefix(claimed.Token, "sk-company-") {
		t.Fatalf("claim did not return a company token")
	}
	if claimed.TokenHash != "" {
		t.Fatalf("claim response exposed the token hash")
	}
	if claimed.Request.Status != "claimed" {
		t.Fatalf("claim request status=%q", claimed.Request.Status)
	}

	// The plaintext is not stored; only its SHA-256 hash is.
	stored, err := e.s.Tokens.FindByHash(context.Background(), crypto.HashToken(claimed.Token))
	if err != nil {
		t.Fatalf("claimed token not stored by hash: %v", err)
	}
	if stored.ID.String() != claimed.TokenID {
		t.Fatalf("stored token id=%s, response id=%s", stored.ID, claimed.TokenID)
	}
	if _, err := e.s.Tokens.FindByHash(context.Background(), claimed.Token); err == nil {
		t.Fatalf("plaintext token was stored as a hash")
	}

	// A second claim must not mint a second token.
	rec = e.do(http.MethodPost, "/portal/api/me/token-requests/"+req.ID.String()+"/claim", nil, aliceSess, true)
	if rec.Code != http.StatusConflict || errorCode(t, rec) != "not_claimable" {
		t.Fatalf("second claim status=%d code=%q", rec.Code, errorCode(t, rec))
	}

	got, err := e.s.TokenRequests.Get(context.Background(), req.ID)
	if err != nil || got.TokenID == nil || got.Status != "claimed" {
		t.Fatalf("request after claim: %+v err=%v", got, err)
	}
	if rec := e.do(http.MethodPost, "/portal/api/me/token-requests/"+req.ID.String()+"/claim", nil, bobSess, true); rec.Code != http.StatusNotFound {
		t.Fatalf("foreign re-claim status=%d, want 404", rec.Code)
	}
}

func TestExpiredClaimWindow(t *testing.T) {
	e := newEnv(t, true)
	emp, _ := e.seedEmployee(t)
	sess := e.login(t, emp.EmployeeNo, initialPassword)

	req := &store.TokenRequest{
		EmployeeID: emp.ID,
		Kind:       "token",
		Reason:     "expired window test request",
		ExpiresAt:  time.Now().Add(24 * time.Hour),
	}
	if err := e.s.TokenRequests.Create(context.Background(), req); err != nil {
		t.Fatalf("create request: %v", err)
	}
	if _, err := e.s.TokenRequests.Approve(context.Background(), req.ID, e.seedReviewer(t), store.TokenRequestGrant{
		QuotaType: "tokens", QuotaPeriod: "monthly", RateLimitRPM: 60,
		ClaimDeadline: time.Now().Add(-time.Minute),
	}, time.Now()); err != nil {
		t.Fatalf("approve: %v", err)
	}

	rec := e.do(http.MethodPost, "/portal/api/me/token-requests/"+req.ID.String()+"/claim", nil, sess, true)
	if rec.Code != http.StatusConflict || errorCode(t, rec) != "claim_expired" {
		t.Fatalf("expired claim status=%d code=%q", rec.Code, errorCode(t, rec))
	}
}

func TestLoginSetsNoStore(t *testing.T) {
	e := newEnv(t, true)
	emp, _ := e.seedEmployee(t)
	rec := e.do(http.MethodPost, "/portal/api/auth/login", map[string]string{
		"employee_no": emp.EmployeeNo,
		"password":    initialPassword,
	}, nil, false)
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("login Cache-Control=%q", got)
	}

	rec = e.do(http.MethodPost, "/portal/api/auth/login", map[string]string{
		"employee_no": emp.EmployeeNo,
		"password":    "wrong-password",
	}, nil, false)
	if rec.Code != http.StatusUnauthorized || errorCode(t, rec) != "invalid_credentials" {
		t.Fatalf("bad login status=%d code=%q", rec.Code, errorCode(t, rec))
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("failed login Cache-Control=%q", got)
	}
}

func TestListClampsUsageRange(t *testing.T) {
	e := newEnv(t, true)
	emp, _ := e.seedEmployee(t)
	sess := e.login(t, emp.EmployeeNo, initialPassword)

	rec := e.do(http.MethodGet, "/portal/api/me/usage?from=not-a-date", nil, sess, false)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad from status=%d", rec.Code)
	}
	rec = e.do(http.MethodGet, "/portal/api/me/usage?from=2020-01-01&to=2020-02-01", nil, sess, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("clamped range status=%d body=%s", rec.Code, rec.Body.String())
	}
}
