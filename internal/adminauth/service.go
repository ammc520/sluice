package adminauth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/company/api-key/internal/crypto"
	"github.com/company/api-key/internal/ratelimit"
	"github.com/company/api-key/internal/store"
)

var (
	ErrInvalidCredentials = errors.New("adminauth: invalid credentials")
	ErrRateLimited        = errors.New("adminauth: too many attempts")
	ErrSessionNotFound    = errors.New("adminauth: session not found")
	ErrSessionExpired     = errors.New("adminauth: session expired")
	ErrAccountDisabled    = errors.New("adminauth: account disabled")
)

// Service manages administrator login and server-side sessions.
type Service struct {
	admins  store.AdminStore
	limiter ratelimit.Limiter
	clock   func() time.Time
	ttl     time.Duration
}

// New creates an admin authentication service.
func New(admins store.AdminStore, limiter ratelimit.Limiter, clock func() time.Time, ttl time.Duration) *Service {
	if clock == nil {
		clock = time.Now
	}
	if ttl <= 0 {
		ttl = 12 * time.Hour
	}
	return &Service{admins: admins, limiter: limiter, clock: clock, ttl: ttl}
}

// TTL returns the session lifetime.
func (s *Service) TTL() time.Duration { return s.ttl }

var (
	dummyOnce sync.Once
	dummyHash string
)

func dummyPasswordHash() string {
	dummyOnce.Do(func() {
		dummyHash, _ = crypto.HashPassword("not-a-real-password")
	})
	return dummyHash
}

// Login verifies credentials and creates a session. It returns the plaintext
// session and CSRF tokens, which are set as cookies by the HTTP layer.
func (s *Service) Login(ctx context.Context, username, password, ip string) (string, string, *store.AdminUser, error) {
	if s.limiter != nil {
		allowed, _, err := s.limiter.Allow(ctx, "admin-login:"+ip, 5)
		if err == nil && !allowed {
			return "", "", nil, ErrRateLimited
		}
	}

	user, err := s.admins.FindByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			crypto.VerifyPassword(dummyPasswordHash(), password)
			return "", "", nil, ErrInvalidCredentials
		}
		return "", "", nil, err
	}
	if user.Status != 1 {
		return "", "", nil, ErrAccountDisabled
	}
	if !crypto.VerifyPassword(user.PasswordHash, password) {
		return "", "", nil, ErrInvalidCredentials
	}

	sessionToken, err := randomToken()
	if err != nil {
		return "", "", nil, err
	}
	csrfToken, err := randomToken()
	if err != nil {
		return "", "", nil, err
	}

	now := s.clock()
	sess := &store.AdminSession{
		ID:        uuid.New(),
		AdminID:   user.ID,
		TokenHash: crypto.HashToken(sessionToken),
		CSRFHash:  crypto.HashToken(csrfToken),
		ExpiresAt: now.Add(s.ttl),
	}
	if err := s.admins.CreateSession(ctx, sess); err != nil {
		return "", "", nil, err
	}

	user.LastLoginAt = &now
	_ = s.admins.Update(ctx, user)

	return sessionToken, csrfToken, user, nil
}

// Validate resolves a session token to an administrator.
func (s *Service) Validate(ctx context.Context, sessionToken string) (*store.AdminUser, *store.AdminSession, error) {
	if sessionToken == "" {
		return nil, nil, ErrSessionNotFound
	}
	sess, err := s.admins.FindSessionByHash(ctx, crypto.HashToken(sessionToken))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, nil, ErrSessionNotFound
		}
		return nil, nil, err
	}
	now := s.clock()
	if !now.Before(sess.ExpiresAt) {
		_ = s.admins.DeleteSession(ctx, sess.TokenHash)
		return nil, nil, ErrSessionExpired
	}
	user, err := s.admins.Get(ctx, sess.AdminID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, nil, ErrSessionNotFound
		}
		return nil, nil, err
	}
	if user.Status != 1 {
		return nil, nil, ErrAccountDisabled
	}
	_ = s.admins.TouchSession(ctx, sess.ID, now)
	return user, sess, nil
}

// Logout deletes the session for a plaintext session token.
func (s *Service) Logout(ctx context.Context, sessionToken string) error {
	if sessionToken == "" {
		return nil
	}
	return s.admins.DeleteSession(ctx, crypto.HashToken(sessionToken))
}

// ValidateCSRF compares the double-submit header to the session's stored hash.
func ValidateCSRF(session *store.AdminSession, headerToken string) bool {
	if session == nil || headerToken == "" {
		return false
	}
	want := crypto.HashToken(headerToken)
	return subtle.ConstantTimeCompare([]byte(want), []byte(session.CSRFHash)) == 1
}

func randomToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
