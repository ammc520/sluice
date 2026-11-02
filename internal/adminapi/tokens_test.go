package adminapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/company/api-key/internal/crypto"
	"github.com/company/api-key/internal/store"
)

type tokenCreateResponse struct {
	Token   string `json:"token"`
	TokenID string `json:"token_id"`
}

func defaultTokenBody(env *adminEnv) string {
	return fmt.Sprintf(`{"employee_id":"%s","quota_type":"tokens","quota_period":"monthly","quota_total":100000,"rate_limit_rpm":60}`, env.employeeID)
}

func mustCreateToken(t *testing.T, env *adminEnv, body string) tokenCreateResponse {
	t.Helper()
	rr := env.do("super_admin", http.MethodPost, "/admin/api/tokens", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create token: %d %s", rr.Code, rr.Body.String())
	}
	var resp tokenCreateResponse
	decodeData(t, rr, &resp)
	if resp.Token == "" || resp.TokenID == "" {
		t.Fatalf("missing token response: %s", rr.Body.String())
	}
	return resp
}

func TestCreateTokenReturnsPlaintextOnce(t *testing.T) {
	env := newAdminEnv(t)
	created := mustCreateToken(t, env, defaultTokenBody(env))
	if !strings.HasPrefix(created.Token, "sk-company-") {
		t.Fatalf("bad token %q", created.Token)
	}

	list := env.do("super_admin", http.MethodGet, "/admin/api/tokens", "")
	if list.Code != http.StatusOK {
		t.Fatalf("list: %d %s", list.Code, list.Body.String())
	}
	if strings.Contains(list.Body.String(), created.Token) {
		t.Fatal("list leaked the plaintext token")
	}
	if strings.Contains(list.Body.String(), crypto.HashToken(created.Token)) {
		t.Fatal("list leaked the token hash")
	}
	var page struct {
		Items []tokenDTO `json:"items"`
		Total int        `json:"total"`
	}
	decodeData(t, list, &page)
	if page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("page %+v", page)
	}
	got := page.Items[0]
	if got.TokenLast4 != created.Token[len(created.Token)-4:] || got.TokenPrefix == "" {
		t.Fatalf("list item %+v", got)
	}
}

func TestTokenValidation(t *testing.T) {
	env := newAdminEnv(t)
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	for _, tc := range []struct {
		name string
		body string
	}{
		{"bad employee", `{"employee_id":"00000000-0000-0000-0000-000000000000","rate_limit_rpm":60}`},
		{"bad quota type", `{"employee_id":"` + env.employeeID + `","quota_type":"credits","rate_limit_rpm":60}`},
		{"bad period", `{"employee_id":"` + env.employeeID + `","quota_period":"weekly","rate_limit_rpm":60}`},
		{"negative quota", `{"employee_id":"` + env.employeeID + `","quota_total":-1,"rate_limit_rpm":60}`},
		{"bad rpm", `{"employee_id":"` + env.employeeID + `","rate_limit_rpm":200000}`},
		{"bad ip", `{"employee_id":"` + env.employeeID + `","rate_limit_rpm":60,"ip_allowlist":["not-an-ip"]}`},
		{"past expiry", `{"employee_id":"` + env.employeeID + `","rate_limit_rpm":60,"expires_at":"` + past + `"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := env.do("super_admin", http.MethodPost, "/admin/api/tokens", tc.body)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("got %d %s", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestUpdateRevokeAndRotateToken(t *testing.T) {
	env := newAdminEnv(t)
	first := mustCreateToken(t, env, defaultTokenBody(env))

	expiry := time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)
	update := fmt.Sprintf(`{"quota_type":"requests","quota_period":"daily","quota_total":500,"rate_limit_rpm":30,"expires_at":%q,"ip_allowlist":["192.0.2.0/24"]}`, expiry)
	rr := env.do("super_admin", http.MethodPut, "/admin/api/tokens/"+first.TokenID, update)
	if rr.Code != http.StatusOK {
		t.Fatalf("update: %d %s", rr.Code, rr.Body.String())
	}
	var updated tokenDTO
	decodeData(t, rr, &updated)
	if updated.QuotaType != "requests" || updated.QuotaPeriod != "daily" || updated.RateLimitRPM != 30 || updated.ExpiresAt == nil {
		t.Fatalf("updated %+v", updated)
	}

	if rr := env.do("super_admin", http.MethodPost, "/admin/api/tokens/"+first.TokenID+"/revoke", ""); rr.Code != http.StatusOK {
		t.Fatalf("revoke: %d %s", rr.Code, rr.Body.String())
	}
	ctx := context.Background()
	old, err := env.store.Tokens.FindByHash(ctx, crypto.HashToken(first.Token))
	if err != nil || old.Status != 0 {
		t.Fatalf("old token after revoke: %+v err=%v", old, err)
	}

	second := mustCreateToken(t, env, defaultTokenBody(env))
	rr = env.do("super_admin", http.MethodPost, "/admin/api/tokens/"+second.TokenID+"/rotate", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("rotate: %d %s", rr.Code, rr.Body.String())
	}
	var rotated tokenCreateResponse
	decodeData(t, rr, &rotated)
	if rotated.Token == "" || rotated.Token == second.Token {
		t.Fatalf("rotated token %q", rotated.Token)
	}

	old, err = env.store.Tokens.FindByHash(ctx, crypto.HashToken(second.Token))
	if err != nil || old.Status != 0 {
		t.Fatalf("pre-rotation token: %+v err=%v", old, err)
	}
	fresh, err := env.store.Tokens.FindByHash(ctx, crypto.HashToken(rotated.Token))
	if err != nil || fresh.Status != 1 || fresh.ID.String() != rotated.TokenID {
		t.Fatalf("new token: %+v err=%v", fresh, err)
	}
	if fresh.QuotaUsed != 0 || fresh.QuotaType != "tokens" || fresh.RateLimitRPM != 60 {
		t.Fatalf("rotation did not copy settings: %+v", fresh)
	}
}

func TestTokenListFilters(t *testing.T) {
	env := newAdminEnv(t)
	mustCreateToken(t, env, defaultTokenBody(env))
	other := &store.Token{
		EmployeeID:   uuid.MustParse(env.employeeID),
		TokenHash:    crypto.HashToken("sk-company-other"),
		TokenPrefix:  "sk-company-oth",
		TokenLast4:   "ther",
		QuotaType:    "requests",
		QuotaPeriod:  "total",
		RateLimitRPM: 5,
		Status:       0,
	}
	if err := env.store.Tokens.Create(context.Background(), other); err != nil {
		t.Fatal(err)
	}

	rr := env.do("auditor", http.MethodGet, "/admin/api/tokens?status=0&employee_id="+env.employeeID, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rr.Code, rr.Body.String())
	}
	var page struct {
		Items []tokenDTO `json:"items"`
		Total int        `json:"total"`
	}
	decodeData(t, rr, &page)
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].Status != 0 {
		t.Fatalf("page %+v", page)
	}
}

func TestAuditorCannotMutateTokens(t *testing.T) {
	env := newAdminEnv(t)
	if rr := env.do("auditor", http.MethodPost, "/admin/api/tokens", defaultTokenBody(env)); rr.Code != http.StatusForbidden {
		t.Fatalf("create got %d", rr.Code)
	}
	created := mustCreateToken(t, env, defaultTokenBody(env))
	if rr := env.do("auditor", http.MethodPost, "/admin/api/tokens/"+created.TokenID+"/revoke", ""); rr.Code != http.StatusForbidden {
		t.Fatalf("revoke got %d", rr.Code)
	}
	if rr := env.do("auditor", http.MethodGet, "/admin/api/tokens", ""); rr.Code != http.StatusOK {
		t.Fatalf("list got %d", rr.Code)
	}
}
