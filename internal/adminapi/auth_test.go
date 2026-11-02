package adminapi

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

	"github.com/company/api-key/internal/adminauth"
	"github.com/company/api-key/internal/crypto"
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
	h := NewAuthHandler(svc, false)

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
