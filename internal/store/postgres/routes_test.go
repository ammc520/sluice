package postgres

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/company/api-key/internal/store"
)

func TestListEnabledRoutesOrdersByPriority(t *testing.T) {
	s := newTestStore(t)
	p := seedProvider(t, s)
	mustCreateRoute(t, s, &store.ModelRoute{ProviderID: p.ID, Protocol: "openai_compatible", Pattern: "gpt-*", MatchType: "prefix", Priority: 200, Enabled: true})
	mustCreateRoute(t, s, &store.ModelRoute{ProviderID: p.ID, Protocol: "openai_compatible", Pattern: "gpt-4o", MatchType: "exact", Priority: 10, Enabled: true})
	routes, err := s.Routes.ListEnabled(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 2 || routes[0].Priority != 10 {
		t.Fatalf("got %+v", routes)
	}
}

func TestListEnabledRoutesExcludesDisabled(t *testing.T) {
	s := newTestStore(t)
	p := seedProvider(t, s)
	mustCreateRoute(t, s, &store.ModelRoute{ProviderID: p.ID, Protocol: "openai_compatible", Pattern: "a*", MatchType: "prefix", Priority: 10, Enabled: false})
	enabled, _ := s.Routes.ListEnabled(ctx)
	all, _ := s.Routes.List(ctx)
	if len(enabled) != 0 || len(all) != 1 {
		t.Fatalf("enabled=%d all=%d", len(enabled), len(all))
	}
}

func TestDeleteRouteMissing(t *testing.T) {
	s := newTestStore(t)
	if err := s.Routes.Delete(ctx, uuid.New()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}
