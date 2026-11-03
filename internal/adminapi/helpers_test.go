package adminapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/company/api-key/internal/adminauth"
	"github.com/company/api-key/internal/crypto"
	"github.com/company/api-key/internal/netguard"
	"github.com/company/api-key/internal/ratelimit"
	"github.com/company/api-key/internal/store"
	"github.com/company/api-key/internal/store/postgres"
	"github.com/company/api-key/internal/testutil"
)

type adminSession struct {
	session *http.Cookie
	csrf    *http.Cookie
}

type adminEnv struct {
	t            *testing.T
	store        *postgres.Store
	pool         *pgxpool.Pool
	engine       *gin.Engine
	auth         *adminauth.Service
	cipher       *crypto.Cipher
	employeeID   string
	departmentID int64
	providerID   string
	sessions     map[string]adminSession
	settings     *SettingsHandler
}

func newAdminEnv(t *testing.T) *adminEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)
	pool := testutil.OpenTestDB(t)
	pg := postgres.New(pool)

	hash, err := crypto.HashPassword("test-password")
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []struct{ username, role string }{
		{"admin", "super_admin"},
		{"operator", "operator"},
		{"auditor", "auditor"},
	} {
		if err := pg.Admins.Create(context.Background(), &store.AdminUser{
			Username: role.username, PasswordHash: hash, Role: role.role, Status: 1,
		}); err != nil {
			t.Fatalf("create admin: %v", err)
		}
	}

	cipher, err := crypto.NewCipher(bytes.Repeat([]byte{0x42}, 32), nil)
	if err != nil {
		t.Fatal(err)
	}
	env := &adminEnv{t: t, store: pg, pool: pool, cipher: cipher, sessions: map[string]adminSession{}}
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO departments (name) VALUES ('Engineering') RETURNING id`).Scan(&env.departmentID); err != nil {
		t.Fatalf("seed department: %v", err)
	}
	dept := env.departmentID
	emp := &store.Employee{
		EmployeeNo:   "E001",
		Name:         "Alice",
		Email:        "alice@example.com",
		DepartmentID: &dept,
		Status:       1,
	}
	if err := pg.Employees.Create(context.Background(), emp); err != nil {
		t.Fatalf("seed employee: %v", err)
	}
	env.employeeID = emp.ID.String()

	env.auth = adminauth.New(pg.Admins, ratelimit.NewMemory(), time.Now, time.Hour)
	authHandler := NewAuthHandler(env.auth, false)
	employees := NewEmployeeHandler(pg.Employees)
	tokens := NewTokenHandler(pg.Tokens, pg.Employees, time.Now)
	providerClient := netguard.NewClient(10*time.Second, 10*time.Second, netguard.WithPrivateUpstreams())
	providers := NewProviderHandler(pg.Providers, pg.Routes, env.cipher, providerClient, nil)
	modelRoutes := NewRouteHandler(pg.Routes, pg.Providers, nil)
	dlpRules := NewDLPRuleHandler(pg.DLPRules, nil)
	dlpTester := NewDLPTestHandler(pg.DLPRules)
	auditHandler := NewAuditHandler(pg.Audit, pg.Admins, env.cipher, time.Now, nil)
	dashboard := NewDashboardHandler(pg.Audit, pg.Dashboard, time.Now)
	departments := NewDepartmentHandler(pg.Departments)
	env.settings = NewSettingsHandler(pg.Settings, pg.Admins, env.cipher, nil, time.Now, nil)
	adminUsers := NewAdminUsersHandler(pg.Admins, nil)

	r := gin.New()
	api := r.Group("/admin/api")
	api.POST("/auth/login", authHandler.Login)

	authed := api.Group("", adminauth.RequireSession(env.auth), adminauth.RequireCSRF())
	authed.GET("/auth/me", authHandler.Me)
	authed.POST("/auth/logout", authHandler.Logout)

	authed.GET("/employees", employees.List)
	authed.GET("/departments", departments.List)
	authed.POST("/employees", adminauth.RequireRole("operator"), employees.Create)
	authed.PUT("/employees/:id", adminauth.RequireRole("operator"), employees.Update)

	authed.GET("/tokens", tokens.List)
	authed.POST("/tokens", adminauth.RequireRole("operator"), tokens.Create)
	authed.PUT("/tokens/:id", adminauth.RequireRole("operator"), tokens.Update)
	authed.POST("/tokens/:id/revoke", adminauth.RequireRole("operator"), tokens.Revoke)
	authed.POST("/tokens/:id/rotate", adminauth.RequireRole("operator"), tokens.Rotate)

	authed.GET("/providers", providers.List)
	authed.GET("/providers/:id/models", adminauth.RequireRole("operator"), providers.Models)
	authed.POST("/providers", adminauth.RequireRole("operator"), providers.Create)
	authed.PUT("/providers/:id", adminauth.RequireRole("operator"), providers.Update)
	authed.DELETE("/providers/:id", adminauth.RequireRole("operator"), providers.Delete)
	authed.POST("/providers/:id/test", adminauth.RequireRole("operator"), providers.Test)

	authed.GET("/model-routes", modelRoutes.List)
	authed.POST("/model-routes", adminauth.RequireRole("operator"), modelRoutes.Create)
	authed.DELETE("/model-routes/:id", adminauth.RequireRole("operator"), modelRoutes.Delete)

	authed.GET("/dlp-rules", dlpRules.List)
	authed.POST("/dlp-rules", adminauth.RequireRole("operator"), dlpRules.Create)
	authed.PUT("/dlp-rules/:id", adminauth.RequireRole("operator"), dlpRules.Update)
	authed.DELETE("/dlp-rules/:id", adminauth.RequireRole("operator"), dlpRules.Delete)
	authed.POST("/dlp-rules/test", adminauth.RequireRole("operator"), dlpTester.Test)

	authed.GET("/audit", auditHandler.List)
	authed.GET("/audit/:id", auditHandler.Detail)
	authed.POST("/audit/:id/reveal", adminauth.RequireRole("super_admin"), auditHandler.Reveal)

	authed.GET("/dashboard/overview", dashboard.Overview)
	authed.GET("/dashboard/trend", dashboard.Trend)
	authed.GET("/dashboard/top-users", dashboard.TopUsers)
	authed.GET("/dashboard/dlp", dashboard.DLP)
	authed.GET("/dashboard/summary", dashboard.Summary)

	super := authed.Group("", adminauth.RequireRole("super_admin"))
	super.GET("/settings/smtp", env.settings.GetSMTP)
	super.PUT("/settings/smtp", env.settings.PutSMTP)
	super.POST("/settings/smtp/test", env.settings.TestSMTP)
	super.GET("/settings/audit", env.settings.GetAuditSettings)
	super.PUT("/settings/audit", env.settings.PutAuditSettings)

	super.GET("/admin-users", adminUsers.List)
	super.POST("/admin-users", adminUsers.Create)
	super.PUT("/admin-users/:id", adminUsers.Update)
	super.POST("/admin-users/:id/reset-password", adminUsers.ResetPassword)

	env.engine = r
	return env
}

// createProvider seeds a provider through the API and records its ID.
func (e *adminEnv) createProvider(t *testing.T, name, protocol string) string {
	t.Helper()
	return e.createProviderWithBase(t, name, protocol, "https://example.com")
}

func (e *adminEnv) createProviderWithBase(t *testing.T, name, protocol, baseURL string) string {
	t.Helper()
	body := fmt.Sprintf(`{"name":%q,"protocol":%q,"base_url":%q,"api_key":"sk-real-secret"}`, name, protocol, baseURL)
	rr := e.do("super_admin", http.MethodPost, "/admin/api/providers", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create provider: %d %s", rr.Code, rr.Body.String())
	}
	var dto providerDTO
	decodeData(t, rr, &dto)
	e.providerID = dto.ID
	return dto.ID
}

// login authenticates once per role and caches the cookies.
func (e *adminEnv) login(role string) adminSession {
	e.t.Helper()
	if s, ok := e.sessions[role]; ok {
		return s
	}
	username := role
	if role == "super_admin" {
		username = "admin"
	}
	rr := e.doRaw(http.MethodPost, "/admin/api/auth/login", `{"username":"`+username+`","password":"test-password"}`, nil, false)
	if rr.Code != http.StatusOK {
		e.t.Fatalf("login %s: %d %s", role, rr.Code, rr.Body.String())
	}
	var s adminSession
	for _, c := range rr.Result().Cookies() {
		switch c.Name {
		case adminauth.SessionCookie:
			s.session = c
		case adminauth.CSRFCookie:
			s.csrf = c
		}
	}
	if s.session == nil || s.csrf == nil {
		e.t.Fatalf("login %s: missing cookies", role)
	}
	e.sessions[role] = s
	return s
}

func (e *adminEnv) doRaw(method, path, body string, cookies []*http.Cookie, withCSRF bool) *httptest.ResponseRecorder {
	e.t.Helper()
	var reader *bytes.Reader
	if body == "" {
		reader = bytes.NewReader(nil)
	} else {
		reader = bytes.NewReader([]byte(body))
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	var csrf string
	for _, c := range cookies {
		req.AddCookie(c)
		if c.Name == adminauth.CSRFCookie {
			csrf = c.Value
		}
	}
	if withCSRF {
		req.Header.Set(adminauth.CSRFHeader, csrf)
	}
	rr := httptest.NewRecorder()
	e.engine.ServeHTTP(rr, req)
	return rr
}

// do executes an authenticated request as role.
func (e *adminEnv) do(role, method, path, body string) *httptest.ResponseRecorder {
	e.t.Helper()
	s := e.login(role)
	return e.doRaw(method, path, body, []*http.Cookie{s.session, s.csrf}, true)
}

func decodeData(t *testing.T, rr *httptest.ResponseRecorder, target any) {
	t.Helper()
	var env struct {
		Data  json.RawMessage `json:"data"`
		Error any             `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v body=%s", err, rr.Body.String())
	}
	if err := json.Unmarshal(env.Data, target); err != nil {
		t.Fatalf("decode data: %v body=%s", err, rr.Body.String())
	}
}
