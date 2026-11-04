package postgres

import (
	"bytes"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/company/sluice/internal/store"
)

func TestProviderKeyIsStoredAsBytes(t *testing.T) {
	s := newTestStore(t)
	p := &store.Provider{Name: "p", Protocol: "openai_compatible", BaseURL: "https://example.com", APIKeyEnc: []byte{1, 2, 3}, Enabled: true}
	if err := s.Providers.Create(ctx, p); err != nil {
		t.Fatal(err)
	}
	got, err := s.Providers.Get(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.APIKeyEnc, []byte{1, 2, 3}) {
		t.Fatal("key bytes changed")
	}
}

func TestProviderUpdateListDelete(t *testing.T) {
	s := newTestStore(t)
	p := seedProvider(t, s)

	p.Name = "renamed"
	p.TimeoutSeconds = 30
	if err := s.Providers.Update(ctx, p); err != nil {
		t.Fatal(err)
	}
	list, err := s.Providers.List(ctx)
	if err != nil || len(list) != 1 || list[0].Name != "renamed" || list[0].TimeoutSeconds != 30 {
		t.Fatalf("list=%+v err=%v", list, err)
	}

	if err := s.Providers.Delete(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Providers.Get(ctx, p.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("got %v", err)
	}
	if err := s.Providers.Delete(ctx, uuid.New()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}
