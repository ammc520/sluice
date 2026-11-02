package adminapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestProviderKeyNeverReturned(t *testing.T) {
	env := newAdminEnv(t)
	body := `{"name":"openai","protocol":"openai_compatible","base_url":"https://api.openai.com","api_key":"sk-real-secret","is_default":true}`
	rr := env.do("super_admin", http.MethodPost, "/admin/api/providers", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("got %d %s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "sk-real-secret") {
		t.Fatal("create response leaked the provider key")
	}

	list := env.do("super_admin", http.MethodGet, "/admin/api/providers", "")
	if list.Code != http.StatusOK {
		t.Fatalf("list: %d %s", list.Code, list.Body.String())
	}
	if strings.Contains(list.Body.String(), "sk-real-secret") {
		t.Fatal("list leaked the provider key")
	}
	if !strings.Contains(list.Body.String(), `"api_key_last4":"cret"`) {
		t.Fatal(list.Body.String())
	}
}

func TestProviderUpdateKeepsKeyWhenBlank(t *testing.T) {
	env := newAdminEnv(t)
	id := env.createProvider(t, "openai", "openai_compatible")

	update := `{"name":"openai-renamed","protocol":"openai_compatible","base_url":"https://example.com","api_key":"","enabled":false,"timeout_seconds":60}`
	rr := env.do("super_admin", http.MethodPut, "/admin/api/providers/"+id, update)
	if rr.Code != http.StatusOK {
		t.Fatalf("update: %d %s", rr.Code, rr.Body.String())
	}
	var dto providerDTO
	decodeData(t, rr, &dto)
	if dto.APIKeyLast4 != "cret" || dto.Enabled {
		t.Fatalf("dto %+v", dto)
	}

	raw, err := env.store.Providers.Get(context.Background(), uuid.MustParse(id))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := env.cipher.Open(raw.APIKeyEnc, providerAAD(raw.ID))
	if err != nil || string(plain) != "sk-real-secret" {
		t.Fatalf("stored key %q err=%v", plain, err)
	}
}

func TestProviderBaseURLChangeRequiresNewKey(t *testing.T) {
	env := newAdminEnv(t)
	id := env.createProvider(t, "openai", "openai_compatible")

	body := `{"name":"openai","protocol":"openai_compatible","base_url":"https://attacker.example.com","api_key":""}`
	rr := env.do("super_admin", http.MethodPut, "/admin/api/providers/"+id, body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("got %d %s", rr.Code, rr.Body.String())
	}

	body = `{"name":"openai","protocol":"openai_compatible","base_url":"https://attacker.example.com","api_key":"new-secret"}`
	rr = env.do("super_admin", http.MethodPut, "/admin/api/providers/"+id, body)
	if rr.Code != http.StatusOK {
		t.Fatalf("re-entered key: %d %s", rr.Code, rr.Body.String())
	}
}

func TestProviderValidation(t *testing.T) {
	env := newAdminEnv(t)
	for _, tc := range []struct {
		name string
		body string
	}{
		{"missing name", `{"name":"","protocol":"openai_compatible","base_url":"https://example.com","api_key":"k"}`},
		{"bad protocol", `{"name":"x","protocol":"gemini","base_url":"https://example.com","api_key":"k"}`},
		{"bad scheme", `{"name":"x","protocol":"openai_compatible","base_url":"ftp://example.com","api_key":"k"}`},
		{"credentials in url", `{"name":"x","protocol":"openai_compatible","base_url":"https://user:pass@example.com","api_key":"k"}`},
		{"missing key", `{"name":"x","protocol":"openai_compatible","base_url":"https://example.com"}`},
		{"bad timeout", `{"name":"x","protocol":"openai_compatible","base_url":"https://example.com","api_key":"k","timeout_seconds":0}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := env.do("super_admin", http.MethodPost, "/admin/api/providers", tc.body)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("got %d %s", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestProviderDeleteBlockedWhileRouted(t *testing.T) {
	env := newAdminEnv(t)
	id := env.createProvider(t, "openai", "openai_compatible")
	body := `{"provider_id":"` + id + `","protocol":"openai_compatible","pattern":"gpt-*","match_type":"prefix"}`
	if rr := env.do("super_admin", http.MethodPost, "/admin/api/model-routes", body); rr.Code != http.StatusCreated {
		t.Fatalf("create route: %d %s", rr.Code, rr.Body.String())
	}

	rr := env.do("super_admin", http.MethodDelete, "/admin/api/providers/"+id, "")
	if rr.Code != http.StatusConflict {
		t.Fatalf("delete got %d %s", rr.Code, rr.Body.String())
	}
}

func TestProviderConnectivityTest(t *testing.T) {
	var gotPath, gotAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		if r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer sk-real-secret" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	env := newAdminEnv(t)
	id := env.createProviderWithBase(t, "local", "openai_compatible", upstream.URL)
	rr := env.do("super_admin", http.MethodPost, "/admin/api/providers/"+id+"/test", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("test: %d %s", rr.Code, rr.Body.String())
	}
	var result struct {
		Reachable  bool `json:"reachable"`
		StatusCode int  `json:"status_code"`
	}
	decodeData(t, rr, &result)
	if !result.Reachable || result.StatusCode != 200 {
		t.Fatalf("result %+v path=%s auth=%s", result, gotPath, gotAuth)
	}

	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer failing.Close()
	id = env.createProviderWithBase(t, "failing", "anthropic", failing.URL)
	rr = env.do("super_admin", http.MethodPost, "/admin/api/providers/"+id+"/test", "")
	decodeData(t, rr, &result)
	if result.Reachable || result.StatusCode != 500 {
		t.Fatalf("failing result %+v", result)
	}
}

func TestAuditorCannotMutateProviders(t *testing.T) {
	env := newAdminEnv(t)
	body := `{"name":"x","protocol":"openai_compatible","base_url":"https://example.com","api_key":"k"}`
	if rr := env.do("auditor", http.MethodPost, "/admin/api/providers", body); rr.Code != http.StatusForbidden {
		t.Fatalf("create got %d", rr.Code)
	}
	if rr := env.do("auditor", http.MethodGet, "/admin/api/providers", ""); rr.Code != http.StatusOK {
		t.Fatalf("list got %d", rr.Code)
	}
}
