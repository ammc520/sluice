package adminapi

import (
	"context"
	"net/http"
	"testing"

	"github.com/company/sluice/internal/store"
)

func TestDLPRuleCRUD(t *testing.T) {
	env := newAdminEnv(t)
	body := `{"name":"phone","description":"mobile numbers","category":"pii","match_type":"regex","pattern":"1[3-9][0-9]{9}","action":"mask","priority":5}`
	rr := env.do("super_admin", http.MethodPost, "/admin/api/dlp-rules", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rr.Code, rr.Body.String())
	}
	var created dlpRuleDTO
	decodeData(t, rr, &created)
	if created.Name != "phone" || created.Action != "mask" || created.Replacement != "***" || created.Builtin {
		t.Fatalf("created %+v", created)
	}

	list := env.do("auditor", http.MethodGet, "/admin/api/dlp-rules", "")
	var page struct {
		Items []dlpRuleDTO `json:"items"`
	}
	decodeData(t, list, &page)
	if len(page.Items) != 1 || page.Items[0].ID != created.ID {
		t.Fatalf("list %+v", page)
	}

	update := `{"name":"phone","description":"","category":"pii","match_type":"regex","pattern":"1[3-9][0-9]{9}","action":"block","enabled":false}`
	rr = env.do("super_admin", http.MethodPut, "/admin/api/dlp-rules/"+created.ID, update)
	if rr.Code != http.StatusOK {
		t.Fatalf("update: %d %s", rr.Code, rr.Body.String())
	}
	var updated dlpRuleDTO
	decodeData(t, rr, &updated)
	if updated.Action != "block" || updated.Enabled {
		t.Fatalf("updated %+v", updated)
	}

	if rr := env.do("super_admin", http.MethodDelete, "/admin/api/dlp-rules/"+created.ID, ""); rr.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rr.Code, rr.Body.String())
	}
	list = env.do("auditor", http.MethodGet, "/admin/api/dlp-rules", "")
	decodeData(t, list, &page)
	if len(page.Items) != 0 {
		t.Fatalf("rule survived deletion: %+v", page)
	}
}

func TestDLPRuleValidation(t *testing.T) {
	env := newAdminEnv(t)
	for _, tc := range []struct {
		name string
		body string
	}{
		{"missing name", `{"match_type":"regex","pattern":"a","action":"mask"}`},
		{"bad match type", `{"name":"x","match_type":"contains","pattern":"a","action":"mask"}`},
		{"empty keyword", `{"name":"x","match_type":"keyword","pattern":"","action":"audit"}`},
		{"bad regex", `{"name":"x","match_type":"regex","pattern":"(","action":"mask"}`},
		{"bad action", `{"name":"x","match_type":"regex","pattern":"a","action":"delete"}`},
		{"negative priority", `{"name":"x","match_type":"regex","pattern":"a","action":"mask","priority":-2}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := env.do("super_admin", http.MethodPost, "/admin/api/dlp-rules", tc.body)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("got %d %s", rr.Code, rr.Body.String())
			}
		})
	}

	valid := `{"name":"dup","match_type":"keyword","pattern":"Project Falcon","action":"audit"}`
	if rr := env.do("super_admin", http.MethodPost, "/admin/api/dlp-rules", valid); rr.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rr.Code, rr.Body.String())
	}
	rr := env.do("super_admin", http.MethodPost, "/admin/api/dlp-rules", valid)
	if rr.Code != http.StatusConflict {
		t.Fatalf("duplicate got %d %s", rr.Code, rr.Body.String())
	}
}

func TestBuiltinRuleCannotBeDeletedButCanBeTuned(t *testing.T) {
	env := newAdminEnv(t)
	builtin := &store.DLPRule{
		Name: "builtin-phone", Category: "pii", MatchType: "regex", Pattern: `1[3-9][0-9]{9}`,
		Action: "mask", Replacement: "***", Priority: 10, Enabled: true, Builtin: true,
	}
	if err := env.store.DLPRules.Create(context.Background(), builtin); err != nil {
		t.Fatal(err)
	}

	if rr := env.do("super_admin", http.MethodDelete, "/admin/api/dlp-rules/"+builtin.ID.String(), ""); rr.Code != http.StatusBadRequest {
		t.Fatalf("delete builtin got %d %s", rr.Code, rr.Body.String())
	}

	rename := `{"name":"renamed","category":"pii","match_type":"regex","pattern":"1[3-9][0-9]{9}","action":"audit","enabled":false}`
	if rr := env.do("super_admin", http.MethodPut, "/admin/api/dlp-rules/"+builtin.ID.String(), rename); rr.Code != http.StatusBadRequest {
		t.Fatalf("rename builtin got %d %s", rr.Code, rr.Body.String())
	}

	tune := `{"name":"builtin-phone","category":"pii","match_type":"regex","pattern":"1[3-9][0-9]{9}","action":"audit","enabled":false}`
	if rr := env.do("super_admin", http.MethodPut, "/admin/api/dlp-rules/"+builtin.ID.String(), tune); rr.Code != http.StatusOK {
		t.Fatalf("tune builtin got %d %s", rr.Code, rr.Body.String())
	}
	stored, _ := env.store.DLPRules.Get(context.Background(), builtin.ID)
	if stored.Action != "audit" || stored.Enabled || !stored.Builtin {
		t.Fatalf("stored %+v", stored)
	}
}

func TestAuditorCannotMutateDLPRules(t *testing.T) {
	env := newAdminEnv(t)
	body := `{"name":"x","match_type":"keyword","pattern":"secret","action":"audit"}`
	if rr := env.do("auditor", http.MethodPost, "/admin/api/dlp-rules", body); rr.Code != http.StatusForbidden {
		t.Fatalf("create got %d", rr.Code)
	}
	if rr := env.do("auditor", http.MethodGet, "/admin/api/dlp-rules", ""); rr.Code != http.StatusOK {
		t.Fatalf("list got %d", rr.Code)
	}
}
