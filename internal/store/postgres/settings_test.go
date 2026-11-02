package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/company/api-key/internal/store"
)

func TestSettingsStoreUpsert(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if _, err := s.Settings.Get(ctx, "audit"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing setting got %v", err)
	}
	if err := s.Settings.Set(ctx, "audit", json.RawMessage(`{"retention_days":180}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.Settings.Set(ctx, "audit", json.RawMessage(`{"retention_days":90}`)); err != nil {
		t.Fatal(err)
	}
	got, err := s.Settings.Get(ctx, "audit")
	if err != nil {
		t.Fatal(err)
	}
	var audit struct {
		RetentionDays int `json:"retention_days"`
	}
	if err := json.Unmarshal(got, &audit); err != nil || audit.RetentionDays != 90 {
		t.Fatalf("got %s err=%v", got, err)
	}

	if err := s.Settings.Set(ctx, "empty", nil); err != nil {
		t.Fatal(err)
	}
	empty, err := s.Settings.Get(ctx, "empty")
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(empty, &decoded); err != nil || len(decoded) != 0 {
		t.Fatalf("empty setting got %s err=%v", empty, err)
	}
}
