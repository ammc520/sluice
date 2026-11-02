package adminapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func createAdminUser(t *testing.T, env *adminEnv, body string) adminUserDTO {
	t.Helper()
	rr := env.do("super_admin", http.MethodPost, "/admin/api/admin-users", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create admin user got %d: %s", rr.Code, rr.Body.String())
	}
	var user adminUserDTO
	decodeData(t, rr, &user)
	return user
}

func listAdminUsers(t *testing.T, env *adminEnv) []adminUserDTO {
	t.Helper()
	rr := env.do("super_admin", http.MethodGet, "/admin/api/admin-users", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("list admin users got %d: %s", rr.Code, rr.Body.String())
	}
	var list struct {
		Items []adminUserDTO `json:"items"`
	}
	decodeData(t, rr, &list)
	return list.Items
}

func rawLogin(env *adminEnv, username, password string) int {
	env.t.Helper()
	body := fmt.Sprintf(`{"username":%q,"password":%q}`, username, password)
	return env.doRaw(http.MethodPost, "/admin/api/auth/login", body, nil, false).Code
}

func TestAdminUsersRequireSuperAdmin(t *testing.T) {
	env := newAdminEnv(t)
	id := uuid.NewString()
	endpoints := []struct {
		method, path, body string
	}{
		{http.MethodGet, "/admin/api/admin-users", ""},
		{http.MethodPost, "/admin/api/admin-users", `{"username":"bob","password":"strong-password-123","role":"operator"}`},
		{http.MethodPut, "/admin/api/admin-users/" + id, `{"role":"auditor"}`},
		{http.MethodPost, "/admin/api/admin-users/" + id + "/reset-password", `{"password":"strong-password-456"}`},
	}
	for _, role := range []string{"operator", "auditor"} {
		for _, endpoint := range endpoints {
			rr := env.do(role, endpoint.method, endpoint.path, endpoint.body)
			if rr.Code != http.StatusForbidden {
				t.Fatalf("%s %s %s got %d: %s", role, endpoint.method, endpoint.path, rr.Code, rr.Body.String())
			}
		}
	}
	if count := env.adminAuditCount("create_admin_user"); count != 0 {
		t.Fatalf("forbidden requests created %d users", count)
	}
}

func TestAdminUserCreateListAndNoPasswordHash(t *testing.T) {
	env := newAdminEnv(t)
	initial := listAdminUsers(t, env)
	if len(initial) != 3 {
		t.Fatalf("initial users %d", len(initial))
	}
	rr := env.do("super_admin", http.MethodGet, "/admin/api/admin-users", "")
	for _, forbidden := range []string{"argon2id", "password_hash", `"password":`} {
		if strings.Contains(rr.Body.String(), forbidden) {
			t.Fatalf("list leaked %q: %s", forbidden, rr.Body.String())
		}
	}

	user := createAdminUser(t, env,
		`{"username":"bob","password":"strong-password-123","role":"operator"}`)
	if user.Username != "bob" || user.Role != "operator" || user.Status != 1 || user.ID == "" {
		t.Fatalf("created %+v", user)
	}
	users := listAdminUsers(t, env)
	if len(users) != 4 {
		t.Fatalf("users after create %d", len(users))
	}
	if count := env.adminAuditCount("create_admin_user"); count != 1 {
		t.Fatalf("create audit rows %d", count)
	}
	if code := rawLogin(env, "bob", "strong-password-123"); code != http.StatusOK {
		t.Fatalf("new user login got %d", code)
	}

	duplicate := env.do("super_admin", http.MethodPost, "/admin/api/admin-users",
		`{"username":"bob","password":"another-password-123","role":"operator"}`)
	if duplicate.Code != http.StatusConflict {
		t.Fatalf("duplicate got %d: %s", duplicate.Code, duplicate.Body.String())
	}
	for _, body := range []string{
		`{"username":"ab","password":"strong-password-123","role":"operator"}`,
		`{"username":"bad user","password":"strong-password-123","role":"operator"}`,
		`{"username":"carol","password":"short","role":"operator"}`,
		`{"username":"carol","password":"strong-password-123"}`,
		`{"username":"carol","password":"strong-password-123","role":"root"}`,
		`{"username":"carol","password":"strong-password-123","role":"operator","status":2}`,
	} {
		rr := env.do("super_admin", http.MethodPost, "/admin/api/admin-users", body)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("%s got %d: %s", body, rr.Code, rr.Body.String())
		}
	}
}

func TestAdminUserUpdateAndLastSuperAdminGuard(t *testing.T) {
	env := newAdminEnv(t)
	bob := createAdminUser(t, env,
		`{"username":"bob","password":"strong-password-123","role":"operator"}`)

	rr := env.do("super_admin", http.MethodPut, "/admin/api/admin-users/"+bob.ID,
		`{"role":"auditor","status":0}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("update got %d: %s", rr.Code, rr.Body.String())
	}
	var updated adminUserDTO
	decodeData(t, rr, &updated)
	if updated.Role != "auditor" || updated.Status != 0 || updated.Username != "bob" {
		t.Fatalf("updated %+v", updated)
	}

	users := listAdminUsers(t, env)
	var superID string
	for _, user := range users {
		if user.Username == "admin" {
			superID = user.ID
		}
	}
	if superID == "" {
		t.Fatal("seeded super admin not found")
	}
	for _, body := range []string{`{"status":0}`, `{"role":"operator"}`} {
		rr := env.do("super_admin", http.MethodPut, "/admin/api/admin-users/"+superID, body)
		if rr.Code != http.StatusConflict {
			t.Fatalf("%s got %d: %s", body, rr.Code, rr.Body.String())
		}
	}
	if code := env.do("super_admin", http.MethodPut,
		"/admin/api/admin-users/"+uuid.NewString(), `{"role":"auditor"}`).Code; code != http.StatusNotFound {
		t.Fatalf("missing user got %d", code)
	}
	for _, body := range []string{`{"role":"root"}`, `{"status":2}`, `{"username":"a"}`} {
		rr := env.do("super_admin", http.MethodPut, "/admin/api/admin-users/"+bob.ID, body)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("%s got %d: %s", body, rr.Code, rr.Body.String())
		}
	}

	createAdminUser(t, env,
		`{"username":"root2","password":"strong-password-456","role":"super_admin"}`)
	rr = env.do("super_admin", http.MethodPut, "/admin/api/admin-users/"+superID, `{"status":0}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("disable with another super admin got %d: %s", rr.Code, rr.Body.String())
	}
	var active int
	if err := env.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM admin_users WHERE role='super_admin' AND status=1`).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active != 1 {
		t.Fatalf("active super admins %d", active)
	}
	if count := env.adminAuditCount("update_admin_user"); count != 2 {
		t.Fatalf("update audit rows %d", count)
	}
}

func TestAdminUserResetPassword(t *testing.T) {
	env := newAdminEnv(t)
	bob := createAdminUser(t, env,
		`{"username":"bob","password":"strong-password-123","role":"operator"}`)

	if code := rawLogin(env, "bob", "strong-password-123"); code != http.StatusOK {
		t.Fatalf("initial login got %d", code)
	}
	rr := env.do("super_admin", http.MethodPost,
		"/admin/api/admin-users/"+bob.ID+"/reset-password", `{"password":"new-strong-pass-456"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("reset got %d: %s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "new-strong-pass-456") {
		t.Fatalf("reset response leaked password: %s", rr.Body.String())
	}
	if code := rawLogin(env, "bob", "strong-password-123"); code != http.StatusUnauthorized {
		t.Fatalf("old password login got %d", code)
	}
	if code := rawLogin(env, "bob", "new-strong-pass-456"); code != http.StatusOK {
		t.Fatalf("new password login got %d", code)
	}
	if count := env.adminAuditCount("reset_admin_password"); count != 1 {
		t.Fatalf("reset audit rows %d", count)
	}

	rr = env.do("super_admin", http.MethodPost,
		"/admin/api/admin-users/"+bob.ID+"/reset-password", `{"password":"short"}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("short reset got %d: %s", rr.Code, rr.Body.String())
	}
	rr = env.do("super_admin", http.MethodPost,
		"/admin/api/admin-users/"+uuid.NewString()+"/reset-password", `{"password":"new-strong-pass-789"}`)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("missing reset got %d: %s", rr.Code, rr.Body.String())
	}
}
