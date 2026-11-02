package adminapi

import (
	"net/http"
	"testing"
)

func TestRouteProtocolMustMatchProvider(t *testing.T) {
	env := newAdminEnv(t)
	env.createProvider(t, "anthropic", "anthropic")
	body := `{"provider_id":"` + env.providerID + `","protocol":"openai_compatible","pattern":"gpt-*","match_type":"prefix"}`
	rr := env.do("super_admin", http.MethodPost, "/admin/api/model-routes", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("got %d %s", rr.Code, rr.Body.String())
	}
}

func TestRouteCreateListDelete(t *testing.T) {
	env := newAdminEnv(t)
	id := env.createProvider(t, "openai", "openai_compatible")
	body := `{"provider_id":"` + id + `","protocol":"openai_compatible","pattern":"gpt-4*","match_type":"glob","upstream_model":"gpt-4o","priority":10}`
	rr := env.do("super_admin", http.MethodPost, "/admin/api/model-routes", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rr.Code, rr.Body.String())
	}
	var created routeDTO
	decodeData(t, rr, &created)
	if created.ProviderID != id || created.Priority != 10 || created.UpstreamModel == nil || *created.UpstreamModel != "gpt-4o" {
		t.Fatalf("created %+v", created)
	}
	if !created.Enabled {
		t.Fatal("route should default to enabled")
	}

	list := env.do("auditor", http.MethodGet, "/admin/api/model-routes", "")
	var page struct {
		Items []routeDTO `json:"items"`
	}
	decodeData(t, list, &page)
	if len(page.Items) != 1 || page.Items[0].ID != created.ID {
		t.Fatalf("list %+v", page)
	}

	if rr := env.do("super_admin", http.MethodDelete, "/admin/api/model-routes/"+created.ID, ""); rr.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rr.Code, rr.Body.String())
	}
	list = env.do("auditor", http.MethodGet, "/admin/api/model-routes", "")
	decodeData(t, list, &page)
	if len(page.Items) != 0 {
		t.Fatalf("route survived deletion: %+v", page)
	}
}

func TestRouteValidation(t *testing.T) {
	env := newAdminEnv(t)
	id := env.createProvider(t, "openai", "openai_compatible")
	for _, tc := range []struct {
		name string
		body string
	}{
		{"unknown provider", `{"provider_id":"00000000-0000-0000-0000-000000000000","protocol":"openai_compatible","pattern":"gpt-*"}`},
		{"bad provider id", `{"provider_id":"nope","protocol":"openai_compatible","pattern":"gpt-*"}`},
		{"bad protocol", `{"provider_id":"` + id + `","protocol":"anthropic","pattern":"gpt-*"}`},
		{"bad match type", `{"provider_id":"` + id + `","protocol":"openai_compatible","pattern":"gpt-*","match_type":"regex"}`},
		{"empty pattern", `{"provider_id":"` + id + `","protocol":"openai_compatible","pattern":""}`},
		{"bad glob", `{"provider_id":"` + id + `","protocol":"openai_compatible","pattern":"[","match_type":"glob"}`},
		{"negative priority", `{"provider_id":"` + id + `","protocol":"openai_compatible","pattern":"gpt-*","priority":-1}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := env.do("super_admin", http.MethodPost, "/admin/api/model-routes", tc.body)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("got %d %s", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestAuditorCannotMutateRoutes(t *testing.T) {
	env := newAdminEnv(t)
	id := env.createProvider(t, "openai", "openai_compatible")
	body := `{"provider_id":"` + id + `","protocol":"openai_compatible","pattern":"gpt-*"}`
	if rr := env.do("auditor", http.MethodPost, "/admin/api/model-routes", body); rr.Code != http.StatusForbidden {
		t.Fatalf("create got %d", rr.Code)
	}
	if rr := env.do("auditor", http.MethodGet, "/admin/api/model-routes", ""); rr.Code != http.StatusOK {
		t.Fatalf("list got %d", rr.Code)
	}
}
