package portalauth_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/company/sluice/internal/crypto"
	"github.com/company/sluice/internal/portalauth"
	"github.com/company/sluice/internal/ratelimit"
	"github.com/company/sluice/internal/store"
	"github.com/company/sluice/internal/store/postgres"
	"github.com/company/sluice/internal/testutil"
)

type testClock struct{ now time.Time }

func (c *testClock) Now() time.Time          { return c.now }
func (c *testClock) Advance(d time.Duration) { c.now = c.now.Add(d) }

type failingLimiter struct{}

func (failingLimiter) Allow(context.Context, string, int) (bool, time.Duration, error) {
	return false, 0, errors.New("limiter down")
}

type denyLimiter struct{}

func (denyLimiter) Allow(context.Context, string, int) (bool, time.Duration, error) {
	return false, time.Minute, nil
}

func setup(t *testing.T) (*postgres.Store, *store.Employee, *store.EmployeeAccount, *testClock) {
	t.Helper()
	s := postgres.New(testutil.OpenTestDB(t))
	e := &store.Employee{
		ID:         uuid.New(),
		EmployeeNo: "E" + uuid.NewString()[:8],
		Name:       "Portal User",
		Status:     1,
	}
	if err := s.Employees.Create(context.Background(), e); err != nil {
		t.Fatalf("seed employee: %v", err)
	}
	hash, err := crypto.HashPassword("initial-password-123")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	a := &store.EmployeeAccount{
		EmployeeID:         e.ID,
		PasswordHash:       hash,
		MustChangePassword: true,
	}
	if err := s.Accounts.CreateAccount(context.Background(), a); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	return s, e, a, &testClock{now: time.Now()}
}

func TestLoginAndValidate(t *testing.T) {
	s, e, _, clock := setup(t)
	svc := portalauth.New(s.Accounts, s.Employees, ratelimit.NewMemory(), clock.Now, time.Hour)
	ctx := context.Background()

	session, csrf, employee, account, err := svc.Login(ctx, e.EmployeeNo, "initial-password-123", "192.0.2.1", "test-agent")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if session == "" || csrf == "" || session == csrf {
		t.Fatalf("bad session/csrf tokens")
	}
	if employee.ID != e.ID || account.EmployeeID != e.ID {
		t.Fatalf("login returned wrong identity")
	}
	if !account.MustChangePassword {
		t.Fatalf("must_change_password lost on login")
	}

	gotEmployee, gotAccount, sess, err := svc.Validate(ctx, session)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if gotEmployee.ID != e.ID || gotAccount.EmployeeID != e.ID || sess.EmployeeID != e.ID {
		t.Fatalf("validate returned wrong identity")
	}
	if _, _, _, err := svc.Validate(ctx, "not-a-session"); !errors.Is(err, portalauth.ErrSessionNotFound) {
		t.Fatalf("unknown session: want ErrSessionNotFound, got %v", err)
	}

	// Logout removes the session.
	if err := svc.Logout(ctx, session); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if _, _, _, err := svc.Validate(ctx, session); !errors.Is(err, portalauth.ErrSessionNotFound) {
		t.Fatalf("session survived logout: %v", err)
	}
}

func TestLoginUnknownEmployeeIsIndistinguishable(t *testing.T) {
	s, _, _, clock := setup(t)
	svc := portalauth.New(s.Accounts, s.Employees, ratelimit.NewMemory(), clock.Now, time.Hour)

	_, _, _, _, err := svc.Login(context.Background(), "NO-SUCH-EMPLOYEE", "whatever-password", "192.0.2.2", "")
	if !errors.Is(err, portalauth.ErrInvalidCredentials) {
		t.Fatalf("unknown employee: want ErrInvalidCredentials, got %v", err)
	}
}

func TestLoginWrongPasswordLocksAfterFiveAttempts(t *testing.T) {
	s, e, _, clock := setup(t)
	svc := portalauth.New(s.Accounts, s.Employees, ratelimit.NewMemory(), clock.Now, time.Hour)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		if _, _, _, _, err := svc.Login(ctx, e.EmployeeNo, "wrong-password", "192.0.2.3", ""); !errors.Is(err, portalauth.ErrInvalidCredentials) {
			t.Fatalf("attempt %d: want ErrInvalidCredentials, got %v", i+1, err)
		}
	}
	if _, _, _, _, err := svc.Login(ctx, e.EmployeeNo, "initial-password-123", "192.0.2.3", ""); !errors.Is(err, portalauth.ErrAccountLocked) {
		t.Fatalf("locked account: want ErrAccountLocked, got %v", err)
	}

	clock.Advance(16 * time.Minute)
	if _, _, _, _, err := svc.Login(ctx, e.EmployeeNo, "initial-password-123", "192.0.2.3", ""); err != nil {
		t.Fatalf("login after lock expiry: %v", err)
	}
}

func TestLockedAccountHidesLockoutFromWrongPasswords(t *testing.T) {
	s, e, _, clock := setup(t)
	svc := portalauth.New(s.Accounts, s.Employees, ratelimit.NewMemory(), clock.Now, time.Hour)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		if _, _, _, _, err := svc.Login(ctx, e.EmployeeNo, "wrong-password", "192.0.2.20", ""); !errors.Is(err, portalauth.ErrInvalidCredentials) {
			t.Fatalf("attempt %d: want ErrInvalidCredentials, got %v", i+1, err)
		}
	}
	// A wrong guess on a locked account must be indistinguishable from a wrong
	// guess on an unknown one; otherwise 429 becomes an account-existence oracle.
	if _, _, _, _, err := svc.Login(ctx, e.EmployeeNo, "wrong-password", "192.0.2.20", ""); !errors.Is(err, portalauth.ErrInvalidCredentials) {
		t.Fatalf("wrong password while locked: want ErrInvalidCredentials, got %v", err)
	}
	// The owner holding the correct password is told about the lockout.
	if _, _, _, _, err := svc.Login(ctx, e.EmployeeNo, "initial-password-123", "192.0.2.20", ""); !errors.Is(err, portalauth.ErrAccountLocked) {
		t.Fatalf("correct password while locked: want ErrAccountLocked, got %v", err)
	}
}

func TestChangePasswordRateLimited(t *testing.T) {
	s, e, _, clock := setup(t)
	svc := portalauth.New(s.Accounts, s.Employees, ratelimit.NewMemory(), clock.Now, time.Hour)
	ctx := context.Background()

	for i := 0; i < 10; i++ {
		if err := svc.ChangePassword(ctx, e.ID, "wrong-current", "brand-new-password-456"); !errors.Is(err, portalauth.ErrInvalidCredentials) {
			t.Fatalf("attempt %d: want ErrInvalidCredentials, got %v", i+1, err)
		}
	}
	if err := svc.ChangePassword(ctx, e.ID, "wrong-current", "brand-new-password-456"); !errors.Is(err, portalauth.ErrRateLimited) {
		t.Fatalf("want ErrRateLimited, got %v", err)
	}
}

func TestLoginRateLimited(t *testing.T) {
	s, _, _, clock := setup(t)
	svc := portalauth.New(s.Accounts, s.Employees, ratelimit.NewMemory(), clock.Now, time.Hour)
	ctx := context.Background()

	for i := 0; i < 10; i++ {
		_, _, _, _, err := svc.Login(ctx, "UNKNOWN", "password", "192.0.2.4", "")
		if !errors.Is(err, portalauth.ErrInvalidCredentials) {
			t.Fatalf("attempt %d: want ErrInvalidCredentials, got %v", i+1, err)
		}
	}
	if _, _, _, _, err := svc.Login(ctx, "UNKNOWN", "password", "192.0.2.4", ""); !errors.Is(err, portalauth.ErrRateLimited) {
		t.Fatalf("want ErrRateLimited, got %v", err)
	}
}

func TestLoginLimiterFailureFailsClosed(t *testing.T) {
	s, e, _, clock := setup(t)
	svc := portalauth.New(s.Accounts, s.Employees, failingLimiter{}, clock.Now, time.Hour)

	_, _, _, _, err := svc.Login(context.Background(), e.EmployeeNo, "initial-password-123", "192.0.2.5", "")
	if !errors.Is(err, portalauth.ErrAuthUnavailable) {
		t.Fatalf("limiter failure: want ErrAuthUnavailable, got %v", err)
	}
}

func TestLoginDeniedLimiter(t *testing.T) {
	s, e, _, clock := setup(t)
	svc := portalauth.New(s.Accounts, s.Employees, denyLimiter{}, clock.Now, time.Hour)

	_, _, _, _, err := svc.Login(context.Background(), e.EmployeeNo, "initial-password-123", "192.0.2.6", "")
	if !errors.Is(err, portalauth.ErrRateLimited) {
		t.Fatalf("denied limiter: want ErrRateLimited, got %v", err)
	}
}

func TestDisabledEmployeeCannotLogInOrKeepSession(t *testing.T) {
	s, e, _, clock := setup(t)
	svc := portalauth.New(s.Accounts, s.Employees, ratelimit.NewMemory(), clock.Now, time.Hour)
	ctx := context.Background()

	session, _, _, _, err := svc.Login(ctx, e.EmployeeNo, "initial-password-123", "192.0.2.7", "")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	e.Status = 0
	if err := s.Employees.Update(ctx, e); err != nil {
		t.Fatalf("disable employee: %v", err)
	}
	if _, _, _, err := svc.Validate(ctx, session); !errors.Is(err, portalauth.ErrAccountDisabled) {
		t.Fatalf("validate disabled: want ErrAccountDisabled, got %v", err)
	}
	if _, _, _, _, err := svc.Login(ctx, e.EmployeeNo, "initial-password-123", "192.0.2.7", ""); !errors.Is(err, portalauth.ErrAccountDisabled) {
		t.Fatalf("disabled login: want ErrAccountDisabled, got %v", err)
	}
}

func TestSessionExpiry(t *testing.T) {
	s, e, _, clock := setup(t)
	svc := portalauth.New(s.Accounts, s.Employees, ratelimit.NewMemory(), clock.Now, time.Hour)

	session, _, _, _, err := svc.Login(context.Background(), e.EmployeeNo, "initial-password-123", "192.0.2.8", "")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	clock.Advance(2 * time.Hour)
	if _, _, _, err := svc.Validate(context.Background(), session); !errors.Is(err, portalauth.ErrSessionExpired) {
		t.Fatalf("expired session: want ErrSessionExpired, got %v", err)
	}
	// The expired row is swept, so a second use looks like an unknown session.
	if _, _, _, err := svc.Validate(context.Background(), session); !errors.Is(err, portalauth.ErrSessionNotFound) {
		t.Fatalf("expired session not deleted: %v", err)
	}
}

func TestChangePasswordVerifiesAndRevokesSessions(t *testing.T) {
	s, e, _, clock := setup(t)
	svc := portalauth.New(s.Accounts, s.Employees, ratelimit.NewMemory(), clock.Now, time.Hour)
	ctx := context.Background()

	session, _, _, _, err := svc.Login(ctx, e.EmployeeNo, "initial-password-123", "192.0.2.9", "")
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	if err := svc.ChangePassword(ctx, e.ID, "wrong-current", "brand-new-password-456"); !errors.Is(err, portalauth.ErrInvalidCredentials) {
		t.Fatalf("wrong current password: want ErrInvalidCredentials, got %v", err)
	}
	if err := svc.ChangePassword(ctx, e.ID, "initial-password-123", "brand-new-password-456"); err != nil {
		t.Fatalf("change password: %v", err)
	}
	if _, _, _, err := svc.Validate(ctx, session); !errors.Is(err, portalauth.ErrSessionNotFound) {
		t.Fatalf("old session survived password change: %v", err)
	}

	_, _, _, account, err := svc.Login(ctx, e.EmployeeNo, "brand-new-password-456", "192.0.2.9", "")
	if err != nil {
		t.Fatalf("login with new password: %v", err)
	}
	if account.MustChangePassword {
		t.Fatalf("must_change_password not cleared")
	}
}

func TestValidateCSRF(t *testing.T) {
	sess := &store.EmployeeSession{CSRFHash: crypto.HashToken("csrf-value")}
	if !portalauth.ValidateCSRF(sess, "csrf-value") {
		t.Fatalf("valid CSRF rejected")
	}
	if portalauth.ValidateCSRF(sess, "csrf-other") {
		t.Fatalf("invalid CSRF accepted")
	}
	if portalauth.ValidateCSRF(nil, "csrf-value") {
		t.Fatalf("nil session accepted")
	}
	if portalauth.ValidateCSRF(sess, "") {
		t.Fatalf("empty header accepted")
	}
}
