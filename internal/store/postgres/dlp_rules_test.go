package postgres

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/company/sluice/internal/store"
)

func TestDLPRuleCRUDAndBuiltinProtection(t *testing.T) {
	s := newTestStore(t)
	r := &store.DLPRule{
		Name: "phone", Description: "phone", Category: "pii", MatchType: "regex",
		Pattern: `1[3-9]\d{9}`, Action: "mask", Replacement: "***", Priority: 10, Enabled: true,
	}
	if err := s.DLPRules.Create(ctx, r); err != nil {
		t.Fatal(err)
	}
	got, err := s.DLPRules.Get(ctx, r.ID)
	if err != nil || got.Name != "phone" {
		t.Fatalf("got=%+v err=%v", got, err)
	}

	r.Action = "audit"
	if err := s.DLPRules.Update(ctx, r); err != nil {
		t.Fatal(err)
	}
	enabled, _ := s.DLPRules.ListEnabled(ctx)
	if len(enabled) != 1 || enabled[0].Action != "audit" {
		t.Fatalf("got %+v", enabled)
	}

	builtin := &store.DLPRule{Name: "builtin", Category: "pii", MatchType: "keyword", Pattern: "x", Action: "audit", Builtin: true, Enabled: true}
	if err := s.DLPRules.Create(ctx, builtin); err != nil {
		t.Fatal(err)
	}
	if err := s.DLPRules.Delete(ctx, builtin.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("builtin delete got %v", err)
	}

	if err := s.DLPRules.Delete(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DLPRules.Get(ctx, uuid.New()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}
