package postgres

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/company/api-key/internal/store"
)

func TestAdminUsersCRUD(t *testing.T) {
	s := newTestStore(t)

	u := &store.AdminUser{Username: "root", PasswordHash: "hash", Role: "super_admin", Status: 1}
	if err := s.Admins.Create(ctx, u); err != nil {
		t.Fatalf("create: %v", err)
	}
	if u.ID == uuid.Nil || u.CreatedAt.IsZero() {
		t.Fatalf("create did not populate fields: %+v", u)
	}

	got, err := s.Admins.FindByUsername(ctx, "root")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if got.ID != u.ID || got.Role != "super_admin" {
		t.Fatalf("got %+v", got)
	}

	if _, err := s.Admins.FindByUsername(ctx, "missing"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing user got %v", err)
	}

	u.Role = "auditor"
	u.Status = 2
	now := time.Now().UTC().Truncate(time.Microsecond)
	u.LastLoginAt = &now
	if err := s.Admins.Update(ctx, u); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ = s.Admins.Get(ctx, u.ID)
	if got.Role != "auditor" || got.Status != 2 || got.LastLoginAt == nil {
		t.Fatalf("after update: %+v", got)
	}

	list, err := s.Admins.List(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("list=%v err=%v", list, err)
	}

	if err := s.Admins.Update(ctx, &store.AdminUser{ID: uuid.New(), Username: "ghost"}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("update missing got %v", err)
	}
}

func TestAdminSessionRoundTrip(t *testing.T) {
	s := newTestStore(t)
	u := &store.AdminUser{Username: "root", PasswordHash: "hash", Role: "super_admin", Status: 1}
	if err := s.Admins.Create(ctx, u); err != nil {
		t.Fatal(err)
	}

	sess := &store.AdminSession{
		AdminID:   u.ID,
		TokenHash: strings.Repeat("a", 64),
		CSRFHash:  strings.Repeat("b", 64),
		ExpiresAt: time.Now().Add(time.Hour),
	}
	if err := s.Admins.CreateSession(ctx, sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if sess.ID == uuid.Nil {
		t.Fatal("session id not populated")
	}

	got, err := s.Admins.FindSessionByHash(ctx, strings.Repeat("a", 64))
	if err != nil {
		t.Fatalf("find session: %v", err)
	}
	if got.AdminID != u.ID || got.CSRFHash != strings.Repeat("b", 64) {
		t.Fatalf("got %+v", got)
	}

	at := time.Now().UTC().Truncate(time.Microsecond)
	if err := s.Admins.TouchSession(ctx, got.ID, at); err != nil {
		t.Fatalf("touch: %v", err)
	}
	got, _ = s.Admins.FindSessionByHash(ctx, strings.Repeat("a", 64))
	if !got.LastSeenAt.Equal(at) {
		t.Fatalf("last seen %v want %v", got.LastSeenAt, at)
	}

	if err := s.Admins.DeleteSession(ctx, strings.Repeat("a", 64)); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.Admins.FindSessionByHash(ctx, strings.Repeat("a", 64)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("after delete got %v", err)
	}
}

func TestDeleteExpiredSessions(t *testing.T) {
	s := newTestStore(t)
	u := &store.AdminUser{Username: "root", PasswordHash: "hash", Role: "super_admin", Status: 1}
	if err := s.Admins.Create(ctx, u); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	for i, exp := range []time.Time{now.Add(-time.Hour), now.Add(time.Hour)} {
		if err := s.Admins.CreateSession(ctx, &store.AdminSession{
			AdminID: u.ID, TokenHash: "h" + string(rune('a'+i)), CSRFHash: "c", ExpiresAt: exp,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Admins.DeleteExpiredSessions(ctx, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Admins.FindSessionByHash(ctx, "ha"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("expired session survived")
	}
	if _, err := s.Admins.FindSessionByHash(ctx, "hb"); err != nil {
		t.Fatal("live session was deleted")
	}
}

func TestInsertAdminAudit(t *testing.T) {
	s := newTestStore(t)
	u := &store.AdminUser{Username: "root", PasswordHash: "hash", Role: "super_admin", Status: 1}
	if err := s.Admins.Create(ctx, u); err != nil {
		t.Fatal(err)
	}

	a := &store.AdminAuditLog{
		AdminID:    u.ID,
		Action:     "reveal_content",
		TargetType: "audit_log",
		TargetID:   "42",
		Detail:     []byte(`{"reason":"incident"}`),
		ClientIP:   "203.0.113.9",
	}
	if err := s.Admins.InsertAdminAudit(ctx, a); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if a.ID == 0 || a.CreatedAt.IsZero() {
		t.Fatalf("audit not populated: %+v", a)
	}

	var detail string
	if err := s.Pool().QueryRow(ctx, `SELECT detail::text FROM admin_audit_logs WHERE id=$1`, a.ID).Scan(&detail); err != nil {
		t.Fatal(err)
	}
	if detail != `{"reason": "incident"}` && detail != `{"reason":"incident"}` {
		t.Fatalf("detail %s", detail)
	}
}
