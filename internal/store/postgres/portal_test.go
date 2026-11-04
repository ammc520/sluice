package postgres

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/company/sluice/internal/crypto"
	"github.com/company/sluice/internal/store"
)

func seedAccount(t *testing.T, s *Store, e *store.Employee) *store.EmployeeAccount {
	t.Helper()
	hash, err := crypto.HashPassword("initial-password-123")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	a := &store.EmployeeAccount{
		EmployeeID:         e.ID,
		PasswordHash:       hash,
		MustChangePassword: true,
	}
	if err := s.Accounts.CreateAccount(ctx, a); err != nil {
		t.Fatalf("create account: %v", err)
	}
	return a
}

func seedRequest(t *testing.T, s *Store, e *store.Employee, expires time.Time) *store.TokenRequest {
	t.Helper()
	total := int64(1_000_000)
	r := &store.TokenRequest{
		EmployeeID:     e.ID,
		Kind:           "token",
		Reason:         "need a token for the internal tool",
		RequestedTotal: &total,
		ExpiresAt:      expires,
	}
	if err := s.TokenRequests.Create(ctx, r); err != nil {
		t.Fatalf("create request: %v", err)
	}
	return r
}

// seedReviewer creates the admin row that reviews requests. Approve/Deny
// enforce the reviewed_by foreign key, so tests cannot use a random UUID.
func seedReviewer(t *testing.T, s *Store) uuid.UUID {
	t.Helper()
	hash, err := crypto.HashPassword("reviewer-password-123")
	if err != nil {
		t.Fatalf("hash reviewer password: %v", err)
	}
	u := &store.AdminUser{
		Username:     "reviewer-" + uuid.NewString()[:8],
		PasswordHash: hash,
		Role:         "operator",
		Status:       1,
	}
	if err := s.Admins.Create(ctx, u); err != nil {
		t.Fatalf("create reviewer: %v", err)
	}
	return u.ID
}

func approveRequest(t *testing.T, s *Store, r *store.TokenRequest, reviewer uuid.UUID, deadline time.Time) {
	t.Helper()
	total := int64(1_000_000)
	_, err := s.TokenRequests.Approve(ctx, r.ID, reviewer, store.TokenRequestGrant{
		QuotaType:     "tokens",
		QuotaPeriod:   "monthly",
		QuotaTotal:    &total,
		RateLimitRPM:  60,
		Note:          "approved",
		ClaimDeadline: deadline,
	}, time.Now())
	if err != nil {
		t.Fatalf("approve request: %v", err)
	}
}

func claimToken(t *testing.T, employeeID uuid.UUID) *store.Token {
	t.Helper()
	plain, hash, prefix, last4, err := crypto.GenerateToken()
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	if crypto.HashToken(plain) != hash {
		t.Fatalf("hash helper disagrees with generated hash")
	}
	ps := monthStart(time.Now())
	return &store.Token{
		EmployeeID:   employeeID,
		TokenHash:    hash,
		TokenPrefix:  prefix,
		TokenLast4:   last4,
		QuotaType:    "tokens",
		QuotaPeriod:  "monthly",
		QuotaTotal:   ptr(int64(1_000_000)),
		QuotaUsed:    0,
		RateLimitRPM: 60,
		IPAllowlist:  []string{},
		Status:       1,
		PeriodStart:  &ps,
	}
}

func TestEmployeeAccountLifecycle(t *testing.T) {
	s := newTestStore(t)
	e := seedEmployee(t, s)
	seedAccount(t, s, e)

	a, got, err := s.Accounts.FindAccountByEmployeeNo(ctx, e.EmployeeNo)
	if err != nil {
		t.Fatalf("find account: %v", err)
	}
	if got.ID != e.ID || got.Name != e.Name {
		t.Fatalf("employee join wrong: %+v", got)
	}
	if !a.MustChangePassword {
		t.Fatalf("must_change_password lost")
	}

	// Deleting the account must also drop live sessions.
	sess := &store.EmployeeSession{
		EmployeeID: e.ID,
		TokenHash:  "session-hash",
		CSRFHash:   "csrf-hash",
		ExpiresAt:  time.Now().Add(time.Hour),
	}
	if err := s.Accounts.CreateSession(ctx, sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := s.Accounts.DeleteAccount(ctx, e.ID); err != nil {
		t.Fatalf("delete account: %v", err)
	}
	if _, err := s.Accounts.FindSessionByHash(ctx, "session-hash"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("session survived account deletion: %v", err)
	}
	if _, err := s.Accounts.GetAccount(ctx, e.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("account not deleted: %v", err)
	}
}

func TestTokenRequestOneActiveAtATime(t *testing.T) {
	s := newTestStore(t)
	e := seedEmployee(t, s)

	first := seedRequest(t, s, e, time.Now().Add(24*time.Hour))
	if _, err := s.TokenRequests.Get(ctx, first.ID); err != nil {
		t.Fatalf("get request: %v", err)
	}

	second := &store.TokenRequest{
		EmployeeID: e.ID,
		Reason:     "another request while the first is pending",
		ExpiresAt:  time.Now().Add(24 * time.Hour),
	}
	if err := s.TokenRequests.Create(ctx, second); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate active request accepted: %v", err)
	}

	// Expiring the live row frees the partial unique index.
	if _, err := s.TokenRequests.Cancel(ctx, first.ID, e.ID, time.Now()); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	second = &store.TokenRequest{
		EmployeeID: e.ID,
		Reason:     "replacement after cancellation",
		ExpiresAt:  time.Now().Add(24 * time.Hour),
	}
	if err := s.TokenRequests.Create(ctx, second); err != nil {
		t.Fatalf("create after cancel: %v", err)
	}
}

func TestTokenRequestExpiryIsSweptOnCreate(t *testing.T) {
	s := newTestStore(t)
	e := seedEmployee(t, s)

	stale := seedRequest(t, s, e, time.Now().Add(-time.Minute))
	// Force the stale deadline: Create computes nothing, so a past expires_at
	// already leaves the row expired by clock, but its status is still pending.
	fresh := &store.TokenRequest{
		EmployeeID: e.ID,
		Reason:     "replacement request",
		ExpiresAt:  time.Now().Add(24 * time.Hour),
	}
	if err := s.TokenRequests.Create(ctx, fresh); err != nil {
		t.Fatalf("create after stale: %v", err)
	}
	got, err := s.TokenRequests.Get(ctx, stale.ID)
	if err != nil {
		t.Fatalf("get stale: %v", err)
	}
	if got.Status != "expired" {
		t.Fatalf("stale status=%q, want expired", got.Status)
	}
}

func TestClaimCreatesTokenOnlyOnce(t *testing.T) {
	s := newTestStore(t)
	e := seedEmployee(t, s)
	reviewer := seedReviewer(t, s)
	r := seedRequest(t, s, e, time.Now().Add(time.Hour))
	deadline := time.Now().Add(72 * time.Hour)
	approveRequest(t, s, r, reviewer, deadline)

	tok := claimToken(t, e.ID)
	now := time.Now()
	claimed, err := s.TokenRequests.Claim(ctx, r.ID, e.ID, tok, "192.0.2.10", now)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if claimed.Status != "claimed" || claimed.TokenID == nil || *claimed.TokenID != tok.ID {
		t.Fatalf("claim result: %+v", claimed)
	}

	var storedHash, prefix, last4 string
	if err := s.pool.QueryRow(ctx,
		`SELECT token_hash, token_prefix, token_last4 FROM tokens WHERE id=$1`, tok.ID).
		Scan(&storedHash, &prefix, &last4); err != nil {
		t.Fatalf("token not inserted: %v", err)
	}
	if storedHash != tok.TokenHash || prefix != tok.TokenPrefix || last4 != tok.TokenLast4 {
		t.Fatalf("token metadata mismatch: %q %q %q", storedHash, prefix, last4)
	}

	// The plaintext is never a database column; only the hash exists.
	var plaintextHits int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.columns
		 WHERE table_name='tokens' AND column_name IN ('token','plaintext','token_plain')`).
		Scan(&plaintextHits); err != nil {
		t.Fatalf("inspect columns: %v", err)
	}
	if plaintextHits != 0 {
		t.Fatalf("tokens table has a plaintext column")
	}

	logs, err := s.EmployeeAudits.ListByEmployee(ctx, e.ID, 10)
	if err != nil {
		t.Fatalf("list employee audit: %v", err)
	}
	if len(logs) != 1 || logs[0].Action != "token_claim" || logs[0].TargetID != tok.ID.String() {
		t.Fatalf("claim audit missing: %+v", logs)
	}

	again := claimToken(t, e.ID)
	if _, err := s.TokenRequests.Claim(ctx, r.ID, e.ID, again, "192.0.2.11", time.Now()); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("second claim: want conflict, got %v", err)
	}
	var tokenCount int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM tokens WHERE employee_id=$1`, e.ID).
		Scan(&tokenCount); err != nil {
		t.Fatalf("count tokens: %v", err)
	}
	if tokenCount != 1 {
		t.Fatalf("token count=%d, want 1", tokenCount)
	}
}

func TestClaimConcurrentOnlyOneWins(t *testing.T) {
	s := newTestStore(t)
	e := seedEmployee(t, s)
	r := seedRequest(t, s, e, time.Now().Add(time.Hour))
	approveRequest(t, s, r, seedReviewer(t, s), time.Now().Add(72*time.Hour))

	const workers = 8
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, workers)
	tokens := make([]*store.Token, workers)
	for i := 0; i < workers; i++ {
		tokens[i] = claimToken(t, e.ID)
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, errs[i] = s.TokenRequests.Claim(ctx, r.ID, e.ID, tokens[i], "192.0.2.12", time.Now())
		}(i)
	}
	close(start)
	wg.Wait()

	successes := 0
	for i, err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, store.ErrConflict):
		default:
			t.Fatalf("worker %d: unexpected error %v", i, err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful claims=%d, want exactly 1", successes)
	}

	var (
		tokenID uuid.UUID
		status  string
		count   int
	)
	if err := s.pool.QueryRow(ctx,
		`SELECT token_id, status FROM token_requests WHERE id=$1`, r.ID).
		Scan(&tokenID, &status); err != nil {
		t.Fatalf("read request: %v", err)
	}
	if status != "claimed" {
		t.Fatalf("status=%q", status)
	}
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM tokens WHERE employee_id=$1`, e.ID).
		Scan(&count); err != nil {
		t.Fatalf("count tokens: %v", err)
	}
	if count != 1 {
		t.Fatalf("tokens=%d, want 1", count)
	}
	// The stored token must belong to a worker that saw success.
	for i, err := range errs {
		if err == nil && tokens[i].ID != tokenID {
			t.Fatalf("stored token id does not match the winning claim")
		}
	}
}

func TestClaimExpiredAndForeignRequests(t *testing.T) {
	s := newTestStore(t)
	owner := seedEmployee(t, s)
	other := seedEmployee(t, s)

	r := seedRequest(t, s, owner, time.Now().Add(time.Hour))
	// Approve with an already-elapsed claim deadline.
	approveRequest(t, s, r, seedReviewer(t, s), time.Now().Add(-time.Minute))

	if _, err := s.TokenRequests.Claim(ctx, r.ID, owner.ID, claimToken(t, owner.ID), "192.0.2.13", time.Now()); !errors.Is(err, store.ErrRequestExpired) {
		t.Fatalf("expired claim: want ErrRequestExpired, got %v", err)
	}

	// A foreign employee must not learn the request exists.
	if _, err := s.TokenRequests.Claim(ctx, r.ID, other.ID, claimToken(t, other.ID), "192.0.2.14", time.Now()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("foreign claim: want ErrNotFound, got %v", err)
	}
	if _, err := s.TokenRequests.Cancel(ctx, r.ID, other.ID, time.Now()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("foreign cancel: want ErrNotFound, got %v", err)
	}
}

func TestApproveAndDenyOnlyPending(t *testing.T) {
	s := newTestStore(t)
	e := seedEmployee(t, s)
	reviewer := seedReviewer(t, s)
	r := seedRequest(t, s, e, time.Now().Add(time.Hour))
	approveRequest(t, s, r, reviewer, time.Now().Add(72*time.Hour))

	if _, err := s.TokenRequests.Approve(ctx, r.ID, reviewer, store.TokenRequestGrant{
		QuotaType: "tokens", QuotaPeriod: "monthly", RateLimitRPM: 60,
		ClaimDeadline: time.Now().Add(time.Hour),
	}, time.Now()); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("double approve: want conflict, got %v", err)
	}
	if _, err := s.TokenRequests.Deny(ctx, r.ID, reviewer, "no", time.Now()); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("deny approved: want conflict, got %v", err)
	}
	if _, err := s.TokenRequests.Cancel(ctx, r.ID, e.ID, time.Now()); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("cancel approved: want conflict, got %v", err)
	}

	// The approved request keeps the one-active-request index occupied, so the
	// deny case uses a second employee.
	other := seedEmployee(t, s)
	denied := seedRequest(t, s, other, time.Now().Add(time.Hour))
	if _, err := s.TokenRequests.Deny(ctx, denied.ID, reviewer, "quota exceeded", time.Now()); err != nil {
		t.Fatalf("deny pending: %v", err)
	}
}

func TestRecordLoginFailureIsAtomic(t *testing.T) {
	s := newTestStore(t)
	e := seedEmployee(t, s)
	seedAccount(t, s, e)

	const workers = 8
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if err := s.Accounts.RecordLoginFailure(ctx, e.ID, 5, 15*time.Minute, time.Now()); err != nil {
				t.Errorf("record failure: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()

	a, err := s.Accounts.GetAccount(ctx, e.ID)
	if err != nil {
		t.Fatalf("get account: %v", err)
	}
	// Five failures trip the lock and reset the counter; the remaining three
	// increments must survive (a lost update would report fewer).
	if a.LockedUntil == nil {
		t.Fatalf("account was not locked after %d concurrent failures", workers)
	}
	if a.FailedAttempts != workers-5 {
		t.Fatalf("failed_attempts=%d, want %d", a.FailedAttempts, workers-5)
	}
}

func TestChangePasswordDoesNotOverwriteAdminReset(t *testing.T) {
	s := newTestStore(t)
	e := seedEmployee(t, s)
	a := seedAccount(t, s, e)

	adminHash, err := crypto.HashPassword("admin-reset-password-789")
	if err != nil {
		t.Fatalf("hash admin password: %v", err)
	}
	if err := s.Accounts.ReplacePassword(ctx, e.ID, adminHash, true); err != nil {
		t.Fatalf("admin reset: %v", err)
	}

	employeeHash, err := crypto.HashPassword("employee-new-password-456")
	if err != nil {
		t.Fatalf("hash employee password: %v", err)
	}
	changed, err := s.Accounts.ChangePassword(ctx, e.ID, a.PasswordHash, employeeHash)
	if err != nil {
		t.Fatalf("change password: %v", err)
	}
	if changed {
		t.Fatalf("stale hash overwrote a concurrent administrator reset")
	}
	got, err := s.Accounts.GetAccount(ctx, e.ID)
	if err != nil {
		t.Fatalf("get account: %v", err)
	}
	if got.PasswordHash != adminHash || !got.MustChangePassword {
		t.Fatalf("admin reset was not preserved")
	}
}

func TestEmployeeModelUsageGroupsAndSorts(t *testing.T) {
	s := newTestStore(t)
	e := seedEmployee(t, s)
	day1 := time.Date(2026, 3, 10, 4, 0, 0, 0, time.UTC)
	day2 := day1.Add(24 * time.Hour)

	records := []*store.AuditRecord{
		{EventTime: day1, RequestID: uuid.NewString(), EmployeeID: &e.ID, Method: "POST",
			Path: "/v1/chat/completions", Model: "model-a", StatusCode: 200,
			TotalTokens: 100, DLPAction: "none"},
		{EventTime: day2, RequestID: uuid.NewString(), EmployeeID: &e.ID, Method: "POST",
			Path: "/v1/chat/completions", Model: "model-a", StatusCode: 200,
			TotalTokens: 50, DLPAction: "none"},
		{EventTime: day1, RequestID: uuid.NewString(), EmployeeID: &e.ID, Method: "POST",
			Path: "/v1/chat/completions", Model: "model-b", StatusCode: 200,
			TotalTokens: 400, DLPAction: "none"},
		{EventTime: day1, RequestID: uuid.NewString(), EmployeeID: &e.ID, Method: "POST",
			Path: "/v1/chat/completions", Model: "model-b", StatusCode: 502,
			TotalTokens: 400, DLPAction: "none"},
		{EventTime: day1, RequestID: uuid.NewString(), EmployeeID: &e.ID, Method: "POST",
			Path: "/v1/chat/completions", Model: "", StatusCode: 200,
			TotalTokens: 10, DLPAction: "none"},
	}
	if err := s.Audit.InsertBatch(ctx, records); err != nil {
		t.Fatalf("insert audit: %v", err)
	}

	got, err := s.Audit.EmployeeModelUsage(ctx, e.ID,
		time.Date(2026, 3, 9, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 3, 12, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("model usage: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("models=%d, want 3: %+v", len(got), got)
	}
	if got[0].Model != "model-b" || got[0].Requests != 2 || got[0].Tokens != 800 || got[0].Errors != 1 {
		t.Fatalf("first model=%+v", got[0])
	}
	if got[1].Model != "model-a" || got[1].Tokens != 150 || len(got[1].Days) != 2 {
		t.Fatalf("second model=%+v", got[1])
	}
	if got[2].Model != "未知" || got[2].Tokens != 10 {
		t.Fatalf("unknown model=%+v", got[2])
	}
}

func TestSessionHashLookupAndRevocation(t *testing.T) {
	s := newTestStore(t)
	e := seedEmployee(t, s)
	seedAccount(t, s, e)

	first := &store.EmployeeSession{
		EmployeeID: e.ID, TokenHash: "hash-one", CSRFHash: "csrf-one",
		ExpiresAt: time.Now().Add(time.Hour),
	}
	second := &store.EmployeeSession{
		EmployeeID: e.ID, TokenHash: "hash-two", CSRFHash: "csrf-two",
		ExpiresAt: time.Now().Add(time.Hour),
	}
	for _, sess := range []*store.EmployeeSession{first, second} {
		if err := s.Accounts.CreateSession(ctx, sess); err != nil {
			t.Fatalf("create session: %v", err)
		}
	}
	got, err := s.Accounts.FindSessionByHash(ctx, "hash-one")
	if err != nil || got.ID != first.ID {
		t.Fatalf("find session: %+v %v", got, err)
	}
	if err := s.Accounts.DeleteSessionsByEmployeeID(ctx, e.ID); err != nil {
		t.Fatalf("revoke sessions: %v", err)
	}
	for _, hash := range []string{"hash-one", "hash-two"} {
		if _, err := s.Accounts.FindSessionByHash(ctx, hash); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("session %s survived revocation: %v", hash, err)
		}
	}
}
