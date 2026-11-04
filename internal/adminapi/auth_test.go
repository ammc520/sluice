package adminapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/company/api-key/internal/adminauth"
	"github.com/company/api-key/internal/crypto"
	"github.com/company/api-key/internal/ratelimit"
	"github.com/company/api-key/internal/store"
)

type fakeAdminStore struct {
	users    map[string]*store.AdminUser
	sessions map[string]*store.AdminSession
}

func newFakeAdminStore(t *testing.T) *fakeAdminStore {
	t.Helper()
	hash, err := crypto.HashPassword("correct-password")
	if err != nil {
		t.Fatal(err)
	}
	u := &store.AdminUser{ID: uuid.New(), Username: "admin", PasswordHash: hash, Role: "super_admin", Status: 1}
	return &fakeAdminStore{
		users:    map[string]*store.AdminUser{"admin": u},
		sessions: map[string]*store.AdminSession{},
	}
}

func (f *fakeAdminStore) FindByUsername(_ context.Context, username string) (*store.AdminUser, error) {
	if u, ok := f.users[username]; ok {
		return u, nil
	}
	return nil, store.ErrNotFound
}
func (f *fakeAdminStore) Get(_ context.Context, id uuid.UUID) (*store.AdminUser, error) {
	for _, u := range f.users {
		if u.ID == id {
			return u, nil
		}
	}
	return nil, store.ErrNotFound
}
func (f *fakeAdminStore) Create(_ context.Context, u *store.AdminUser) error {
	f.users[u.Username] = u
	return nil
}
func (f *fakeAdminStore) Update(context.Context, *store.AdminUser) error { return nil }
func (f *fakeAdminStore) List(context.Context) ([]store.AdminUser, error) {
	return nil, nil
}
func (f *fakeAdminStore) CreateSession(_ context.Context, s *store.AdminSession) error {
	f.sessions[s.TokenHash] = s
	return nil
}
func (f *fakeAdminStore) FindSessionByHash(_ context.Context, hash string) (*store.AdminSession, error) {
	if s, ok := f.sessions[hash]; ok {
		return s, nil
	}
	return nil, store.ErrNotFound
}
func (f *fakeAdminStore) TouchSession(context.Context, uuid.UUID, time.Time) error { return nil }
func (f *fakeAdminStore) DeleteSession(_ context.Context, hash string) error {
	delete(f.sessions, hash)
	return nil
}
func (f *fakeAdminStore) DeleteSessionsByAdminID(_ context.Context, adminID uuid.UUID) error {
	for hash, sess := range f.sessions {
		if sess.AdminID == adminID {
			delete(f.sessions, hash)
		}
	}
	return nil
}
func (f *fakeAdminStore) DeleteExpiredSessions(context.Context, time.Time) error { return nil }
func (f *fakeAdminStore) InsertAdminAudit(context.Context, *store.AdminAuditLog) error {
	return nil
}

func newAuthEnv(t *testing.T) (*gin.Engine, *fakeAdminStore) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	fake := newFakeAdminStore(t)
	svc := adminauth.New(fake, nil, time.Now, time.Hour)
	h := NewAuthHandler(svc, false, nil)

	r := gin.New()
	r.POST("/login", h.Login)
	authed := r.Group("/", adminauth.RequireSession(svc))
	authed.GET("/me", h.Me)
	authed.POST("/logout", adminauth.RequireCSRF(), h.Logout)
	return r, fake
}

func postJSON(r *gin.Engine, path string, body any, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	_ = json.NewEncoder(&buf).Encode(body)
	req := httptest.NewRequest(http.MethodPost, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	return rr
}

func TestLoginHandlerSetsCookies(t *testing.T) {
	r, _ := newAuthEnv(t)
	rr := postJSON(r, "/login", gin.H{"username": "admin", "password": "correct-password"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rr.Code, rr.Body.String())
	}

	var session, csrf *http.Cookie
	for _, c := range rr.Result().Cookies() {
		switch c.Name {
		case adminauth.SessionCookie:
			session = c
		case adminauth.CSRFCookie:
			csrf = c
		}
	}
	if session == nil || csrf == nil {
		t.Fatalf("missing cookies: %v", rr.Result().Header.Values("Set-Cookie"))
	}
	if !session.HttpOnly {
		t.Fatal("session cookie must be HttpOnly")
	}
	if csrf.HttpOnly {
		t.Fatal("csrf cookie must be readable by the SPA")
	}
	if session.SameSite != http.SameSiteStrictMode || csrf.SameSite != http.SameSiteStrictMode {
		t.Fatal("cookies must be SameSite=Strict")
	}
	if session.Value == "" || csrf.Value == "" || session.Value == csrf.Value {
		t.Fatal("tokens must be present and distinct")
	}
	if session.Path != adminauth.SessionCookiePath {
		t.Fatalf("session cookie path=%q, want %q", session.Path, adminauth.SessionCookiePath)
	}
	if csrf.Path != adminauth.CSRFCookiePath {
		t.Fatalf("csrf cookie path=%q, want %q", csrf.Path, adminauth.CSRFCookiePath)
	}
	// Pre-migration root-scoped cookies must be expired on login.
	for _, c := range rr.Result().Cookies() {
		if c.Path == adminauth.LegacyCookiePath && c.Value != "" {
			t.Fatalf("legacy cookie %s not expired: %+v", c.Name, c)
		}
	}
	if !strings.Contains(rr.Body.String(), `"username":"admin"`) {
		t.Fatalf("body %s", rr.Body.String())
	}
}

func TestLoginHandlerRejectsBadCredentials(t *testing.T) {
	r, _ := newAuthEnv(t)
	rr := postJSON(r, "/login", gin.H{"username": "admin", "password": "nope"})
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status %d", rr.Code)
	}
	if strings.Contains(rr.Body.String(), "admin") && strings.Contains(rr.Body.String(), "nope") {
		t.Fatal("response leaked credentials")
	}
}

func TestMeRequiresSession(t *testing.T) {
	r, _ := newAuthEnv(t)
	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status %d", rr.Code)
	}
}

func TestLogoutRequiresCSRFAndClearsCookies(t *testing.T) {
	r, fake := newAuthEnv(t)
	login := postJSON(r, "/login", gin.H{"username": "admin", "password": "correct-password"})
	if login.Code != http.StatusOK {
		t.Fatalf("login status %d", login.Code)
	}
	cookies := login.Result().Cookies()
	var session, csrf *http.Cookie
	for _, c := range cookies {
		if c.Name == adminauth.SessionCookie {
			session = c
		}
		if c.Name == adminauth.CSRFCookie {
			csrf = c
		}
	}

	req := httptest.NewRequest(http.MethodPost, "/logout", nil)
	req.AddCookie(session)
	req.AddCookie(csrf)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("logout without CSRF got %d", rr.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/logout", nil)
	req.AddCookie(session)
	req.AddCookie(csrf)
	req.Header.Set(adminauth.CSRFHeader, csrf.Value)
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("logout got %d body %s", rr.Code, rr.Body.String())
	}
	if len(fake.sessions) != 0 {
		t.Fatal("session was not deleted")
	}
}

// A forged X-Forwarded-For must not give an attacker a fresh login-rate-limit
// bucket. Only proxies listed in TRUSTED_PROXIES may set the client IP.
func TestAdminLoginRateLimitIgnoresSpoofedForwardedFor(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fake := newFakeAdminStore(t)
	svc := adminauth.New(fake, ratelimit.NewMemory(), time.Now, time.Hour)
	h := NewAuthHandler(svc, false, []string{"10.0.0.0/8"})
	r := gin.New()
	r.POST("/login", h.Login)

	attempt := func(remote, forwarded string) int {
		req := httptest.NewRequest(http.MethodPost, "/login",
			strings.NewReader(`{"username":"admin","password":"wrong-password"}`))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = remote
		if forwarded != "" {
			req.Header.Set("X-Forwarded-For", forwarded)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec.Code
	}

	for i := 0; i < 5; i++ {
		if code := attempt("192.0.2.10:1000", fmt.Sprintf("198.51.100.%d", i+1)); code != http.StatusUnauthorized {
			t.Fatalf("attempt %d status=%d, want 401", i+1, code)
		}
	}
	if code := attempt("192.0.2.10:2000", "203.0.113.99"); code != http.StatusTooManyRequests {
		t.Fatalf("sixth attempt status=%d, want 429", code)
	}

	// When the direct peer is a trusted proxy, the forwarded address is used.
	if code := attempt("10.0.0.5:3000", "198.51.100.50"); code != http.StatusUnauthorized {
		t.Fatalf("trusted proxy attempt status=%d, want 401", code)
	}
	for i := 0; i < 4; i++ {
		attempt("10.0.0.5:3000", "198.51.100.50")
	}
	if code := attempt("10.0.0.5:3000", "198.51.100.50"); code != http.StatusTooManyRequests {
		t.Fatalf("trusted proxy sixth attempt status=%d, want 429", code)
	}
}
