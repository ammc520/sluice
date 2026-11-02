package adminauth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/company/api-key/internal/crypto"
	"github.com/company/api-key/internal/ratelimit"
	"github.com/company/api-key/internal/store"
)

type fakeAdminStore struct {
	users    map[string]*store.AdminUser
	sessions map[string]*store.AdminSession
	audits   []*store.AdminAuditLog
}

func newFakeAdminStore() *fakeAdminStore {
	return &fakeAdminStore{users: map[string]*store.AdminUser{}, sessions: map[string]*store.AdminSession{}}
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
	if u.ID == uuid.Nil {
		u.ID = uuid.New()
	}
	u.CreatedAt = time.Now()
	f.users[u.Username] = u
	return nil
}
func (f *fakeAdminStore) Update(_ context.Context, u *store.AdminUser) error {
	f.users[u.Username] = u
	return nil
}
func (f *fakeAdminStore) List(context.Context) ([]store.AdminUser, error) {
	out := []store.AdminUser{}
	for _, u := range f.users {
		out = append(out, *u)
	}
	return out, nil
}
func (f *fakeAdminStore) CreateSession(_ context.Context, s *store.AdminSession) error {
	if s.ID == uuid.Nil {
		s.ID = uuid.New()
	}
	s.CreatedAt = time.Now()
	s.LastSeenAt = time.Now()
	f.sessions[s.TokenHash] = s
	return nil
}
func (f *fakeAdminStore) FindSessionByHash(_ context.Context, hash string) (*store.AdminSession, error) {
	if s, ok := f.sessions[hash]; ok {
		return s, nil
	}
	return nil, store.ErrNotFound
}
func (f *fakeAdminStore) TouchSession(_ context.Context, id uuid.UUID, at time.Time) error {
	for _, s := range f.sessions {
		if s.ID == id {
			s.LastSeenAt = at
		}
	}
	return nil
}
func (f *fakeAdminStore) DeleteSession(_ context.Context, hash string) error {
	delete(f.sessions, hash)
	return nil
}
func (f *fakeAdminStore) DeleteExpiredSessions(context.Context, time.Time) error { return nil }
func (f *fakeAdminStore) InsertAdminAudit(_ context.Context, a *store.AdminAuditLog) error {
	f.audits = append(f.audits, a)
	return nil
}

func newAuthFixture(t *testing.T) (*Service, *fakeAdminStore) {
	t.Helper()
	fake := newFakeAdminStore()
	hash, err := crypto.HashPassword("correct-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := fake.Create(context.Background(), &store.AdminUser{Username: "admin", PasswordHash: hash, Role: "super_admin", Status: 1}); err != nil {
		t.Fatal(err)
	}
	svc := New(fake, nil, time.Now, time.Hour)
	return svc, fake
}

func TestLoginRejectsWrongPassword(t *testing.T) {
	svc, _ := newAuthFixture(t)
	_, _, _, err := svc.Login(context.Background(), "admin", "wrong", "1.2.3.4")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("got %v", err)
	}
}

func TestLoginSuccessAndValidate(t *testing.T) {
	svc, _ := newAuthFixture(t)
	session, csrf, user, err := svc.Login(context.Background(), "admin", "correct-password", "1.2.3.4")
	if err != nil {
		t.Fatal(err)
	}
	if session == "" || csrf == "" || user.Username != "admin" {
		t.Fatalf("session=%q csrf=%q user=%+v", session, csrf, user)
	}
	u, sess, err := svc.Validate(context.Background(), session)
	if err != nil || u.ID != user.ID || sess.AdminID != user.ID {
		t.Fatalf("u=%+v sess=%+v err=%v", u, sess, err)
	}
}

func TestExpiredSessionRejected(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	fake := newFakeAdminStore()
	hash, _ := crypto.HashPassword("pw")
	fake.Create(context.Background(), &store.AdminUser{Username: "admin", PasswordHash: hash, Role: "super_admin", Status: 1})
	svc := New(fake, nil, func() time.Time { return now }, time.Hour)

	session, _, _, err := svc.Login(context.Background(), "admin", "pw", "ip")
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Hour)
	if _, _, err := svc.Validate(context.Background(), session); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("got %v", err)
	}
}

func TestCSRFMismatchFails(t *testing.T) {
	session := &store.AdminSession{CSRFHash: crypto.HashToken("right")}
	if ValidateCSRF(session, "wrong") {
		t.Fatal("accepted wrong CSRF")
	}
	if !ValidateCSRF(session, "right") {
		t.Fatal("rejected correct CSRF")
	}
	if ValidateCSRF(session, "") {
		t.Fatal("accepted empty CSRF")
	}
}

func TestLoginRateLimited(t *testing.T) {
	svc, _ := newAuthFixture(t)
	mem := ratelimit.NewMemory()
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	mem.SetClock(func() time.Time { return now })
	svc.limiter = mem

	for i := 0; i < 5; i++ {
		svc.Login(context.Background(), "admin", "wrong", "9.9.9.9")
	}
	if _, _, _, err := svc.Login(context.Background(), "admin", "wrong", "9.9.9.9"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("got %v", err)
	}
}

type failingLimiter struct{}

func (failingLimiter) Allow(context.Context, string, int) (bool, time.Duration, error) {
	return false, 0, errors.New("limiter down")
}

func TestLoginFailsClosedWhenLimiterFails(t *testing.T) {
	svc, _ := newAuthFixture(t)
	svc.limiter = failingLimiter{}
	if _, _, _, err := svc.Login(context.Background(), "admin", "correct-password", "9.9.9.9"); !errors.Is(err, ErrAuthUnavailable) {
		t.Fatalf("got %v", err)
	}
}

func TestRoleMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	setUser := func(role string) gin.HandlerFunc {
		return func(c *gin.Context) {
			c.Set(ContextUser, &store.AdminUser{Role: role})
			c.Next()
		}
	}
	r.GET("/super", setUser("auditor"), RequireRole("super_admin"), func(c *gin.Context) { c.Status(200) })
	r.GET("/super-ok", setUser("super_admin"), RequireRole("super_admin"), func(c *gin.Context) { c.Status(200) })
	r.GET("/operator-ok", setUser("super_admin"), RequireRole("operator"), func(c *gin.Context) { c.Status(200) })

	if rr := httptest.NewRecorder(); true {
		req := httptest.NewRequest(http.MethodGet, "/super", nil)
		r.ServeHTTP(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Fatalf("auditor got %d", rr.Code)
		}
	}
	if rr := httptest.NewRecorder(); true {
		req := httptest.NewRequest(http.MethodGet, "/super-ok", nil)
		r.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("super got %d", rr.Code)
		}
	}
	if rr := httptest.NewRecorder(); true {
		req := httptest.NewRequest(http.MethodGet, "/operator-ok", nil)
		r.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("super on operator route got %d", rr.Code)
		}
	}
}
