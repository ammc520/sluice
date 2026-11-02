package adminapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/company/api-key/internal/dlp"
)

func TestDLPTestEndpointReturnsMaskedText(t *testing.T) {
	env := newAdminEnv(t)
	if err := dlp.EnsureBuiltinRules(context.Background(), env.store.DLPRules); err != nil {
		t.Fatal(err)
	}

	rr := env.do("operator", http.MethodPost, "/admin/api/dlp-rules/test", `{"text":"call 13800138000"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("got %d %s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "13800138000") || !strings.Contains(rr.Body.String(), "***") {
		t.Fatalf("response %s", rr.Body.String())
	}

	var data struct {
		Action     string `json:"action"`
		MaskedText string `json:"masked_text"`
		Hits       []struct {
			RuleName string `json:"rule_name"`
			Action   string `json:"action"`
			Start    int    `json:"start"`
			End      int    `json:"end"`
		} `json:"hits"`
	}
	decodeData(t, rr, &data)
	if data.Action != "mask" || data.MaskedText != "call ***" {
		t.Fatalf("data %+v", data)
	}
	if len(data.Hits) == 0 || data.Hits[0].RuleName != dlp.BuiltinPhoneName {
		t.Fatalf("hits %+v", data.Hits)
	}
	if data.Hits[0].Start != 5 || data.Hits[0].End != 16 {
		t.Fatalf("offsets %+v", data.Hits[0])
	}
}

func TestDLPTestEndpointUsesTemporaryRules(t *testing.T) {
	env := newAdminEnv(t)
	if err := dlp.EnsureBuiltinRules(context.Background(), env.store.DLPRules); err != nil {
		t.Fatal(err)
	}
	before, err := env.store.DLPRules.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	body := `{"text":"Project Falcon is confidential","rules":[{"name":"project","match_type":"keyword","pattern":"project falcon","action":"block"}]}`
	rr := env.do("operator", http.MethodPost, "/admin/api/dlp-rules/test", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("got %d %s", rr.Code, rr.Body.String())
	}
	var response struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	var data struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(response.Data, &data); err != nil {
		t.Fatal(err)
	}
	if data.Action != "block" {
		t.Fatalf("action %q body %s", data.Action, rr.Body.String())
	}

	after, err := env.store.DLPRules.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("temporary rule was persisted: %d -> %d", len(before), len(after))
	}
}

func TestDLPTestEndpointRejectsInvalidTemporaryRule(t *testing.T) {
	env := newAdminEnv(t)
	body := `{"text":"x","rules":[{"name":"bad","match_type":"regex","pattern":"(","action":"mask"}]}`
	if rr := env.do("operator", http.MethodPost, "/admin/api/dlp-rules/test", body); rr.Code != http.StatusBadRequest {
		t.Fatalf("got %d %s", rr.Code, rr.Body.String())
	}
}

func TestDLPTestEndpointRoleEnforcement(t *testing.T) {
	env := newAdminEnv(t)
	if rr := env.do("auditor", http.MethodPost, "/admin/api/dlp-rules/test", `{"text":"x"}`); rr.Code != http.StatusForbidden {
		t.Fatalf("auditor got %d", rr.Code)
	}
	if rr := env.do("super_admin", http.MethodPost, "/admin/api/dlp-rules/test", `{"text":"x"}`); rr.Code != http.StatusOK {
		t.Fatalf("super admin got %d %s", rr.Code, rr.Body.String())
	}
}
