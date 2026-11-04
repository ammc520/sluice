// Package portalauth authenticates employees on the self-service portal.
// It is deliberately separate from adminauth: a portal cookie can never
// satisfy an administrator route and vice versa.
package portalauth

import (
	"context"
	cryptorand "crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/company/sluice/internal/crypto"
	"github.com/company/sluice/internal/ratelimit"
	"github.com/company/sluice/internal/store"
)

var (
	ErrInvalidCredentials = errors.New("portalauth: invalid credentials")
	ErrRateLimited        = errors.New("portalauth: too many attempts")
	ErrAccountLocked      = errors.New("portalauth: account temporarily locked")
	ErrAuthUnavailable    = errors.New("portalauth: authentication unavailable")
	ErrSessionNotFound    = errors.New("portalauth: session not found")
	ErrSessionExpired     = errors.New("portalauth: session expired")
	ErrAccountDisabled    = errors.New("portalauth: account disabled")
	ErrWeakPassword       = errors.New("portalauth: password does not meet policy")
)

const (
	// Login limits are token buckets expressed per minute. The per-account
	// limit is tighter than the per-IP limit because offices commonly share a
	// NAT address.
	loginIPLimit   = 30
	loginUserLimit = 10

	maxFailedAttempts = 5
	lockoutDuration   = 15 * time.Minute
)

// Service manages employee login and server-side sessions.
type Service struct {
	accounts  store.EmployeeAccountStore
	employees store.EmployeeStore
	limiter   ratelimit.Limiter
	clock     func() time.Time
	ttl       time.Duration
}

// New creates a portal authentication service.
func New(accounts store.EmployeeAccountStore, employees store.EmployeeStore, limiter ratelimit.Limiter, clock func() time.Time, ttl time.Duration) *Service {
	if clock == nil {
		clock = time.Now
	}
	if ttl <= 0 {
		ttl = 8 * time.Hour
	}
	return &Service{
		accounts:  accounts,
		employees: employees,
		limiter:   limiter,
		clock:     clock,
		ttl:       ttl,
	}
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

// Login verifies the employee number and password, then creates a session.
func (s *Service) Login(ctx context.Context, employeeNo, password, ip, userAgent string) (string, string, *store.Employee, *store.EmployeeAccount, error) {
	employeeNo = strings.TrimSpace(employeeNo)
	if employeeNo == "" || password == "" {
		return "", "", nil, nil, ErrInvalidCredentials
	}

	if s.limiter != nil {
		if err := s.allow(ctx, "portal-login-ip:"+ip, loginIPLimit); err != nil {
			return "", "", nil, nil, err
		}
		if err := s.allow(ctx, "portal-login-user:"+employeeNo, loginUserLimit); err != nil {
			return "", "", nil, nil, err
		}
	}

	account, employee, err := s.accounts.FindAccountByEmployeeNo(ctx, employeeNo)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// Equalize timing for unknown employee numbers.
			crypto.VerifyPassword(dummyPasswordHash(), password)
			return "", "", nil, nil, ErrInvalidCredentials
		}
		return "", "", nil, nil, err
	}

	now := s.clock()
	if employee.Status != 1 {
		return "", "", nil, nil, ErrAccountDisabled
	}
	locked := account.LockedUntil != nil && now.Before(*account.LockedUntil)
	// Always run the password check, even while locked. A wrong guess on a
	// locked account must look exactly like a wrong guess on an unknown one;
	// only the owner holding the correct password learns about the lockout.
	if !crypto.VerifyPassword(account.PasswordHash, password) {
		if !locked {
			s.recordFailure(ctx, account.EmployeeID, now)
		}
		return "", "", nil, nil, ErrInvalidCredentials
	}
	if locked {
		return "", "", nil, nil, ErrAccountLocked
	}

	account.FailedAttempts = 0
	account.LockedUntil = nil
	account.LastLoginAt = &now
	if err := s.accounts.RecordLoginSuccess(ctx, employee.ID, now); err != nil {
		return "", "", nil, nil, err
	}

	sessionToken, err := randomToken()
	if err != nil {
		return "", "", nil, nil, err
	}
	csrfToken, err := randomToken()
	if err != nil {
		return "", "", nil, nil, err
	}
	if len(userAgent) > 512 {
		userAgent = userAgent[:512]
	}
	sess := &store.EmployeeSession{
		ID:         uuid.New(),
		EmployeeID: employee.ID,
		TokenHash:  crypto.HashToken(sessionToken),
		CSRFHash:   crypto.HashToken(csrfToken),
		IP:         ip,
		UserAgent:  userAgent,
		ExpiresAt:  now.Add(s.ttl),
	}
	if err := s.accounts.CreateSession(ctx, sess); err != nil {
		return "", "", nil, nil, err
	}
	return sessionToken, csrfToken, employee, account, nil
}

// recordFailure applies the lockout policy. Audit/write failures must not turn
// a wrong password into a successful login, so the error is intentionally
// ignored after the credentials have already been rejected. The counter is
// incremented in SQL so concurrent attempts cannot lose a failure.
func (s *Service) recordFailure(ctx context.Context, employeeID uuid.UUID, now time.Time) {
	_ = s.accounts.RecordLoginFailure(ctx, employeeID, maxFailedAttempts, lockoutDuration, now)
}

// Validate resolves a session cookie to the employee, account, and session.
func (s *Service) Validate(ctx context.Context, sessionToken string) (*store.Employee, *store.EmployeeAccount, *store.EmployeeSession, error) {
	if sessionToken == "" {
		return nil, nil, nil, ErrSessionNotFound
	}
	sess, err := s.accounts.FindSessionByHash(ctx, crypto.HashToken(sessionToken))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, nil, nil, ErrSessionNotFound
		}
		return nil, nil, nil, err
	}
	now := s.clock()
	if !now.Before(sess.ExpiresAt) {
		_ = s.accounts.DeleteSession(ctx, sess.TokenHash)
		return nil, nil, nil, ErrSessionExpired
	}
	employee, err := s.employees.Get(ctx, sess.EmployeeID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			_ = s.accounts.DeleteSession(ctx, sess.TokenHash)
			return nil, nil, nil, ErrSessionNotFound
		}
		return nil, nil, nil, err
	}
	if employee.Status != 1 {
		return nil, nil, nil, ErrAccountDisabled
	}
	account, err := s.accounts.GetAccount(ctx, sess.EmployeeID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			_ = s.accounts.DeleteSession(ctx, sess.TokenHash)
			return nil, nil, nil, ErrSessionNotFound
		}
		return nil, nil, nil, err
	}
	_ = s.accounts.TouchSession(ctx, sess.ID, now)
	return employee, account, sess, nil
}

// ChangePassword verifies the current password, stores the new hash, and
// revokes every session. The client must log in again.
func (s *Service) ChangePassword(ctx context.Context, employeeID uuid.UUID, currentPassword, newPassword string) error {
	// Possession of a session must not allow unlimited current-password
	// guessing; the same per-employee limiter that guards login applies here.
	if s.limiter != nil {
		if err := s.allow(ctx, "portal-password-change:"+employeeID.String(), loginUserLimit); err != nil {
			return err
		}
	}
	account, err := s.accounts.GetAccount(ctx, employeeID)
	if err != nil {
		return err
	}
	if !crypto.VerifyPassword(account.PasswordHash, currentPassword) {
		return ErrInvalidCredentials
	}
	encoded, err := crypto.HashPassword(newPassword)
	if err != nil {
		return err
	}
	changed, err := s.accounts.ChangePassword(ctx, employeeID, account.PasswordHash, encoded)
	if err != nil {
		return err
	}
	if !changed {
		// An administrator reset the password between our read and write; the
		// reset wins and the employee must log in with the new initial password.
		return ErrInvalidCredentials
	}
	return s.accounts.DeleteSessionsByEmployeeID(ctx, employeeID)
}

// Logout deletes the session for a plaintext cookie value.
func (s *Service) Logout(ctx context.Context, sessionToken string) error {
	if sessionToken == "" {
		return nil
	}
	return s.accounts.DeleteSession(ctx, crypto.HashToken(sessionToken))
}

// ValidateCSRF compares the double-submit header to the session hash.
func ValidateCSRF(session *store.EmployeeSession, headerToken string) bool {
	if session == nil || headerToken == "" {
		return false
	}
	want := crypto.HashToken(headerToken)
	return subtle.ConstantTimeCompare([]byte(want), []byte(session.CSRFHash)) == 1
}

func (s *Service) allow(ctx context.Context, key string, limit int) error {
	allowed, _, err := s.limiter.Allow(ctx, key, limit)
	if err != nil {
		// Login is security-sensitive: never turn a limiter failure into
		// unlimited password attempts.
		return ErrAuthUnavailable
	}
	if !allowed {
		return ErrRateLimited
	}
	return nil
}

func randomToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := io.ReadFull(cryptorand.Reader, buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
