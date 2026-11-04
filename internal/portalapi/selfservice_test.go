package portalapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/company/sluice/internal/portalcfg"
)

type createdKey struct {
	Token string `json:"token"`
	Item  struct {
		ID           string `json:"id"`
		Name         string `json:"name"`
		TokenPrefix  string `json:"token_prefix"`
		TokenLast4   string `json:"token_last4"`
		QuotaTotal   *int64 `json:"quota_total"`
		RateLimitRPM int    `json:"rate_limit_rpm"`
		Status       int16  `json:"status"`
	} `json:"item"`
}

func decodeCreatedKey(t *testing.T, rec *httptest.ResponseRecorder) createdKey {
	t.Helper()
	out := decode(t, rec)
	var key createdKey
	if err := json.Unmarshal(out.Data, &key); err != nil {
		t.Fatalf("decode key: %v", err)
	}
	return key
}

func TestSelfServiceKeyLifecycle(t *testing.T) {
	e := newEnv(t, true)
	emp, _ := e.seedEmployee(t)
	sess := e.login(t, emp.EmployeeNo, initialPassword)

	rec := e.do(http.MethodGet, "/portal/api/me/quota", nil, sess, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("quota status=%d body=%s", rec.Code, rec.Body.String())
	}
	var quota struct {
		QuotaTotal         int64      `json:"quota_total"`
		QuotaUsed          int64      `json:"quota_used"`
		QuotaRemaining     int64      `json:"quota_remaining"`
		ResetsAt           *time.Time `json:"resets_at"`
		ActiveTokens       int        `json:"active_tokens"`
		MaxActiveTokens    int        `json:"max_active_tokens"`
		SelfServiceEnabled bool       `json:"self_service_enabled"`
	}
	if err := json.Unmarshal(decode(t, rec).Data, &quota); err != nil {
		t.Fatalf("decode quota: %v", err)
	}
	if quota.QuotaTotal != 1_000_000 || quota.QuotaUsed != 0 || quota.QuotaRemaining != 1_000_000 {
		t.Fatalf("quota = %+v", quota)
	}
	if quota.ResetsAt == nil || quota.ActiveTokens != 0 || quota.MaxActiveTokens != 5 || !quota.SelfServiceEnabled {
		t.Fatalf("quota policy = %+v", quota)
	}

	rec = e.do(http.MethodPost, "/portal/api/me/tokens", map[string]any{
		"name":           "CI",
		"quota_total":    500,
		"rate_limit_rpm": 30,
	}, sess, true)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("create Cache-Control=%q", got)
	}
	created := decodeCreatedKey(t, rec)
	if !strings.HasPrefix(created.Token, "sk-company-") {
		t.Fatalf("plaintext=%q", created.Token)
	}
	if created.Item.Name != "CI" || created.Item.QuotaTotal == nil || *created.Item.QuotaTotal != 500 ||
		created.Item.RateLimitRPM != 30 || created.Item.Status != 1 {
		t.Fatalf("created item = %+v", created.Item)
	}

	// The plaintext is never returned by the list endpoint.
	rec = e.do(http.MethodGet, "/portal/api/me/tokens", nil, sess, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status=%d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), created.Token) {
		t.Fatalf("list leaked the plaintext")
	}
	if !strings.Contains(rec.Body.String(), `"name":"CI"`) {
		t.Fatalf("list missing key name: %s", rec.Body.String())
	}

	rec = e.do(http.MethodPost, "/portal/api/me/tokens/"+created.Item.ID+"/rotate", nil, sess, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("rotate status=%d body=%s", rec.Code, rec.Body.String())
	}
	rotated := decodeCreatedKey(t, rec)
	if rotated.Token == "" || rotated.Token == created.Token {
		t.Fatalf("rotate did not issue a fresh key")
	}
	if rotated.Item.Name != "CI" || rotated.Item.ID == created.Item.ID {
		t.Fatalf("rotated item = %+v", rotated.Item)
	}
	oldID := uuid.MustParse(created.Item.ID)
	old, err := e.s.Tokens.Get(context.Background(), oldID)
	if err != nil {
		t.Fatalf("get old key: %v", err)
	}
	if old.Status != 0 {
		t.Fatalf("old key status=%d, want revoked", old.Status)
	}

	// A second employee cannot rotate or revoke the key.
	other, _ := e.seedEmployee(t)
	otherSess := e.login(t, other.EmployeeNo, initialPassword)
	rec = e.do(http.MethodPost, "/portal/api/me/tokens/"+rotated.Item.ID+"/rotate", nil, otherSess, true)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("foreign rotate status=%d", rec.Code)
	}
	rec = e.do(http.MethodPost, "/portal/api/me/tokens/"+rotated.Item.ID+"/revoke", nil, otherSess, true)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("foreign revoke status=%d", rec.Code)
	}

	rec = e.do(http.MethodPost, "/portal/api/me/tokens/"+rotated.Item.ID+"/revoke", nil, sess, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("revoke status=%d body=%s", rec.Code, rec.Body.String())
	}
	freshID := uuid.MustParse(rotated.Item.ID)
	fresh, err := e.s.Tokens.Get(context.Background(), freshID)
	if err != nil {
		t.Fatalf("get rotated key: %v", err)
	}
	if fresh.Status != 0 {
		t.Fatalf("revoked key status=%d", fresh.Status)
	}
}

func TestSelfServiceKeyValidationAndLimits(t *testing.T) {
	e := newEnvWith(t, true, func(s *portalcfg.Settings) { s.MaxActiveTokens = 1 })
	emp, _ := e.seedEmployee(t)
	sess := e.login(t, emp.EmployeeNo, initialPassword)

	for name, payload := range map[string]any{
		"short name":   map[string]any{"name": "a"},
		"long name":    map[string]any{"name": strings.Repeat("x", 41)},
		"negative cap": map[string]any{"name": "ok", "quota_total": -1},
		"bad rate":     map[string]any{"name": "ok", "rate_limit_rpm": 0},
	} {
		rec := e.do(http.MethodPost, "/portal/api/me/tokens", payload, sess, true)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s status=%d body=%s", name, rec.Code, rec.Body.String())
		}
	}

	rec := e.do(http.MethodPost, "/portal/api/me/tokens", map[string]any{"name": "first"}, sess, true)
	if rec.Code != http.StatusCreated {
		t.Fatalf("first key status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = e.do(http.MethodPost, "/portal/api/me/tokens", map[string]any{"name": "second"}, sess, true)
	if rec.Code != http.StatusConflict || errorCode(t, rec) != "token_limit_reached" {
		t.Fatalf("limit status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestSelfServiceSwitchesAreEnforced(t *testing.T) {
	e := newEnvWith(t, true, func(s *portalcfg.Settings) {
		s.SelfServiceEnabled = false
		s.AllowSelfRotate = false
		s.AllowSelfRevoke = false
	})
	emp, _ := e.seedEmployee(t)
	tok := e.seedToken(t, emp.ID, "old")
	sess := e.login(t, emp.EmployeeNo, initialPassword)

	rec := e.do(http.MethodPost, "/portal/api/me/tokens", map[string]any{"name": "blocked"}, sess, true)
	if rec.Code != http.StatusForbidden || errorCode(t, rec) != "self_service_disabled" {
		t.Fatalf("create status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = e.do(http.MethodPost, "/portal/api/me/tokens/"+tok.ID.String()+"/rotate", nil, sess, true)
	if rec.Code != http.StatusForbidden || errorCode(t, rec) != "self_rotate_disabled" {
		t.Fatalf("rotate status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = e.do(http.MethodPost, "/portal/api/me/tokens/"+tok.ID.String()+"/revoke", nil, sess, true)
	if rec.Code != http.StatusForbidden || errorCode(t, rec) != "self_revoke_disabled" {
		t.Fatalf("revoke status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestQuotaRequestAutoApproval(t *testing.T) {
	e := newEnvWith(t, true, func(s *portalcfg.Settings) { s.AutoApproveQuota = true })
	emp, _ := e.seedEmployee(t)
	sess := e.login(t, emp.EmployeeNo, initialPassword)

	rec := e.do(http.MethodPost, "/portal/api/me/token-requests", map[string]any{
		"reason":          "the analytics job needs a larger monthly allowance",
		"requested_total": int64(2_000_000),
	}, sess, true)
	if rec.Code != http.StatusCreated {
		t.Fatalf("request status=%d body=%s", rec.Code, rec.Body.String())
	}
	var req struct {
		ID             string `json:"id"`
		Kind           string `json:"kind"`
		Status         string `json:"status"`
		RequestedTotal *int64 `json:"requested_total"`
	}
	if err := json.Unmarshal(decode(t, rec).Data, &req); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if req.Kind != "quota" || req.Status != "granted" ||
		req.RequestedTotal == nil || *req.RequestedTotal != 2_000_000 {
		t.Fatalf("request = %+v", req)
	}

	quota, err := e.s.EmployeeQuotas.Get(context.Background(), emp.ID)
	if err != nil {
		t.Fatalf("get quota: %v", err)
	}
	if quota.QuotaTotal != 2_000_000 {
		t.Fatalf("quota total=%d, want 2000000", quota.QuotaTotal)
	}
}

func TestSelfServiceRotateRejectsRevokedKey(t *testing.T) {
	e := newEnv(t, true)
	emp, _ := e.seedEmployee(t)
	sess := e.login(t, emp.EmployeeNo, initialPassword)

	rec := e.do(http.MethodPost, "/portal/api/me/tokens", map[string]any{"name": "old"}, sess, true)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", rec.Code, rec.Body.String())
	}
	created := decodeCreatedKey(t, rec)

	rec = e.do(http.MethodPost, "/portal/api/me/tokens/"+created.Item.ID+"/revoke", nil, sess, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("revoke status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = e.do(http.MethodPost, "/portal/api/me/tokens/"+created.Item.ID+"/rotate", nil, sess, true)
	if rec.Code != http.StatusConflict || errorCode(t, rec) != "token_revoked" {
		t.Fatalf("rotate revoked status=%d body=%s", rec.Code, rec.Body.String())
	}

	// The rejected rotation must not mint a replacement.
	rec = e.do(http.MethodGet, "/portal/api/me/tokens", nil, sess, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status=%d", rec.Code)
	}
	var list struct {
		Items []struct {
			Status int16 `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(decode(t, rec).Data, &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list.Items) != 1 || list.Items[0].Status != 0 {
		t.Fatalf("items = %+v, want the single revoked key", list.Items)
	}
}
