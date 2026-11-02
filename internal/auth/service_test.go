package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/company/api-key/internal/crypto"
	"github.com/company/api-key/internal/store"
)

type fakeTokenStore struct {
	token     *store.Token
	findErr   error
	findCalls int
}

func (f *fakeTokenStore) Create(context.Context, *store.Token) error { return nil }
func (f *fakeTokenStore) Get(context.Context, uuid.UUID) (*store.Token, error) {
	return f.token, nil
}
func (f *fakeTokenStore) FindByHash(context.Context, string) (*store.Token, error) {
	f.findCalls++
	if f.findErr != nil {
		return nil, f.findErr
	}
	return f.token, nil
}
func (f *fakeTokenStore) Update(context.Context, *store.Token) error { return nil }
func (f *fakeTokenStore) List(context.Context, store.TokenFilter) ([]store.Token, int, error) {
	return nil, 0, nil
}
func (f *fakeTokenStore) ReserveQuota(context.Context, uuid.UUID, int64, time.Time) (store.Reservation, error) {
	return store.Reservation{}, nil
}
func (f *fakeTokenStore) SettleQuota(context.Context, store.Reservation, int64) error { return nil }
func (f *fakeTokenStore) Touch(context.Context, uuid.UUID, time.Time) error           { return nil }

type fakeEmployeeStore struct {
	employee *store.Employee
	getErr   error
}

func (f *fakeEmployeeStore) Create(context.Context, *store.Employee) error { return nil }
func (f *fakeEmployeeStore) Get(context.Context, uuid.UUID) (*store.Employee, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.employee, nil
}
func (f *fakeEmployeeStore) Update(context.Context, *store.Employee) error { return nil }
func (f *fakeEmployeeStore) List(context.Context, store.EmployeeFilter) ([]store.Employee, int, error) {
	return nil, 0, nil
}

type fixture struct {
	svc       *Service
	plain     string
	tokens    *fakeTokenStore
	employees *fakeEmployeeStore
	cache     *MemoryCache
	now       time.Time
}

func newAuthFixture(t *testing.T) *fixture {
	t.Helper()
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	plain, hash, prefix, last4, err := crypto.GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	emp := &store.Employee{ID: uuid.New(), Name: "Alice", Status: 1}
	tok := &store.Token{
		ID:           uuid.New(),
		EmployeeID:   emp.ID,
		TokenHash:    hash,
		TokenPrefix:  prefix,
		TokenLast4:   last4,
		QuotaType:    "tokens",
		QuotaPeriod:  "monthly",
		RateLimitRPM: 60,
		IPAllowlist:  []string{},
		Status:       1,
	}
	tokens := &fakeTokenStore{token: tok}
	employees := &fakeEmployeeStore{employee: emp}
	cache := NewMemoryCache()
	f := &fixture{plain: plain, tokens: tokens, employees: employees, cache: cache, now: now}
	clock := func() time.Time { return f.now }
	cache.SetClock(clock)
	f.svc = New(tokens, employees, cache, nil, clock)
	return f
}

func requestWith(headers map[string]string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	r.RemoteAddr = "203.0.113.10:1234"
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	return r
}

func TestAuthenticateAcceptsBearerAndXAPIKey(t *testing.T) {
	f := newAuthFixture(t)
	for name, header := range map[string]string{"bearer": "Authorization", "x-api-key": "x-api-key"} {
		t.Run(name, func(t *testing.T) {
			value := f.plain
			if header == "Authorization" {
				value = "Bearer " + f.plain
			}
			p, err := f.svc.Authenticate(requestWith(map[string]string{header: value}))
			if err != nil {
				t.Fatal(err)
			}
			if p.Token.TokenHash == "" || p.Employee.Name != "Alice" {
				t.Fatalf("principal %+v", p)
			}
		})
	}

	// Both headers carrying the same token are accepted.
	p, err := f.svc.Authenticate(requestWith(map[string]string{
		"Authorization": "Bearer " + f.plain,
		"x-api-key":     f.plain,
	}))
	if err != nil || p == nil {
		t.Fatalf("same token in both headers: %v", err)
	}
}

func TestAuthenticateRejectsMissingOrConflictingTokens(t *testing.T) {
	f := newAuthFixture(t)
	cases := []struct {
		name    string
		headers map[string]string
		want    error
	}{
		{"missing", nil, ErrMissingToken},
		{"conflicting", map[string]string{"Authorization": "Bearer " + f.plain, "x-api-key": "sk-company-different"}, ErrInvalidToken},
		{"malformed authorization", map[string]string{"Authorization": "Basic abc"}, ErrInvalidToken},
		{"not a company token", map[string]string{"Authorization": "Bearer sk-openai-abc"}, ErrInvalidToken},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := f.svc.Authenticate(requestWith(tc.headers)); !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
		})
	}
}

func TestAuthenticateUnknownToken(t *testing.T) {
	f := newAuthFixture(t)
	f.tokens.findErr = store.ErrNotFound
	if _, err := f.svc.Authenticate(requestWith(map[string]string{"x-api-key": f.plain})); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("got %v", err)
	}
}

func TestAuthenticateRejectsIPOutsideAllowlist(t *testing.T) {
	f := newAuthFixture(t)
	f.tokens.token.IPAllowlist = []string{"10.0.0.0/8"}
	r := requestWith(map[string]string{"Authorization": "Bearer " + f.plain})
	r.RemoteAddr = "192.0.2.10:1234"
	if _, err := f.svc.Authenticate(r); !errors.Is(err, ErrIPNotAllowed) {
		t.Fatalf("got %v", err)
	}

	f.tokens.token.IPAllowlist = []string{"203.0.113.10"}
	if _, err := f.svc.Authenticate(requestWith(map[string]string{"Authorization": "Bearer " + f.plain})); err != nil {
		t.Fatalf("exact IP should pass: %v", err)
	}
}

func TestAuthenticateUsesTrustedProxyHeaders(t *testing.T) {
	f := newAuthFixture(t)
	f.tokens.token.IPAllowlist = []string{"203.0.113.0/24"}
	svc := New(f.tokens, f.employees, f.cache, []string{"10.0.0.0/8"}, func() time.Time { return f.now })

	r := requestWith(map[string]string{
		"Authorization":   "Bearer " + f.plain,
		"X-Forwarded-For": "198.51.100.9, 203.0.113.8",
	})
	r.RemoteAddr = "10.0.0.5:1234"
	if _, err := svc.Authenticate(r); err != nil {
		t.Fatalf("trusted proxy request rejected: %v", err)
	}

	// X-Forwarded-For is ignored when the peer is not trusted.
	spoofed := requestWith(map[string]string{
		"Authorization":   "Bearer " + f.plain,
		"X-Forwarded-For": "203.0.113.8",
	})
	spoofed.RemoteAddr = "192.0.2.10:1234"
	if _, err := f.svc.Authenticate(spoofed); !errors.Is(err, ErrIPNotAllowed) {
		t.Fatalf("untrusted forwarded header accepted: %v", err)
	}
}

func TestAuthenticateRejectsDisabledAndExpired(t *testing.T) {
	t.Run("token disabled", func(t *testing.T) {
		f := newAuthFixture(t)
		f.tokens.token.Status = 0
		if _, err := f.svc.Authenticate(requestWith(map[string]string{"x-api-key": f.plain})); !errors.Is(err, ErrTokenDisabled) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("employee disabled", func(t *testing.T) {
		f := newAuthFixture(t)
		f.employees.employee.Status = 0
		if _, err := f.svc.Authenticate(requestWith(map[string]string{"x-api-key": f.plain})); !errors.Is(err, ErrEmployeeDisabled) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("expired", func(t *testing.T) {
		f := newAuthFixture(t)
		past := f.now.Add(-time.Minute)
		f.tokens.token.ExpiresAt = &past
		if _, err := f.svc.Authenticate(requestWith(map[string]string{"x-api-key": f.plain})); !errors.Is(err, ErrTokenExpired) {
			t.Fatalf("got %v", err)
		}
	})
}

type failingCache struct{}

func (failingCache) Get(context.Context, string) (*Principal, error) {
	return nil, errors.New("cache down")
}
func (failingCache) Set(context.Context, string, *Principal, time.Duration) error {
	return errors.New("cache down")
}
func (failingCache) Delete(context.Context, string) error { return errors.New("cache down") }

func TestAuthenticateFallsBackWhenCacheFails(t *testing.T) {
	f := newAuthFixture(t)
	svc := New(f.tokens, f.employees, failingCache{}, nil, func() time.Time { return f.now })
	if _, err := svc.Authenticate(requestWith(map[string]string{"Authorization": "Bearer " + f.plain})); err != nil {
		t.Fatalf("cache failure should not fail auth: %v", err)
	}
}

func TestAuthenticateCachesPrincipal(t *testing.T) {
	f := newAuthFixture(t)
	r1 := requestWith(map[string]string{"Authorization": "Bearer " + f.plain})
	r2 := requestWith(map[string]string{"Authorization": "Bearer " + f.plain})
	if _, err := f.svc.Authenticate(r1); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Authenticate(r2); err != nil {
		t.Fatal(err)
	}
	if f.tokens.findCalls != 1 {
		t.Fatalf("find calls=%d, want 1 (second request should hit cache)", f.tokens.findCalls)
	}

	if err := f.cache.Delete(context.Background(), crypto.HashToken(f.plain)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Authenticate(requestWith(map[string]string{"Authorization": "Bearer " + f.plain})); err != nil {
		t.Fatal(err)
	}
	if f.tokens.findCalls != 2 {
		t.Fatalf("find calls=%d after invalidation", f.tokens.findCalls)
	}
}

func TestCachedPrincipalStillChecksExpiry(t *testing.T) {
	f := newAuthFixture(t)
	expires := f.now.Add(10 * time.Second)
	f.tokens.token.ExpiresAt = &expires
	if _, err := f.svc.Authenticate(requestWith(map[string]string{"x-api-key": f.plain})); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(20 * time.Second)
	// The cache entry is still live, but the token itself has expired.
	if _, err := f.svc.Authenticate(requestWith(map[string]string{"x-api-key": f.plain})); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("got %v", err)
	}
}

func TestClientIP(t *testing.T) {
	r := requestWith(map[string]string{"X-Forwarded-For": "198.51.100.9, 203.0.113.8"})
	r.RemoteAddr = "10.0.0.5:1234"
	if got := ClientIP(r, []string{"10.0.0.0/8"}); got != "203.0.113.8" {
		t.Fatalf("got %q", got)
	}
	if got := ClientIP(r, nil); got != "10.0.0.5" {
		t.Fatalf("untrusted got %q", got)
	}
}
