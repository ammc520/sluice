package proxy

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/company/sluice/internal/store"
)

func testProvider(protocol string, isDefault bool) store.Provider {
	return store.Provider{
		ID:        uuid.New(),
		Name:      protocol + "-provider",
		Protocol:  protocol,
		BaseURL:   "https://example.com",
		IsDefault: isDefault,
		Enabled:   true,
	}
}

func testRoute(provider store.Provider, matchType, pattern string, priority int, created time.Time) store.ModelRoute {
	return store.ModelRoute{
		ID:         uuid.New(),
		ProviderID: provider.ID,
		Protocol:   provider.Protocol,
		Pattern:    pattern,
		MatchType:  matchType,
		Priority:   priority,
		Enabled:    true,
		CreatedAt:  created,
	}
}

func TestProtocolForPath(t *testing.T) {
	cases := map[string]string{
		"/v1/chat/completions":      "openai_compatible",
		"/v1/completions":           "openai_compatible",
		"/v1/embeddings":            "openai_compatible",
		"/v1/models":                "openai_compatible",
		"/v1/messages":              "anthropic",
		"/v1/messages/count_tokens": "anthropic",
	}
	for path, want := range cases {
		got, ok := ProtocolForPath(path)
		if !ok || got != want {
			t.Errorf("%s: got %q,%v want %q", path, got, ok, want)
		}
	}
	for _, path := range []string{
		"/v1/responses", "/healthz", "/v1/messagesx",
		"/v1/messages/", "/v1/messages/count_tokens/extra",
		"/v1/messages/../admin", "/v1/messages/%2e%2e/admin",
		"/v1/chat/completions/extra",
	} {
		if _, ok := ProtocolForPath(path); ok {
			t.Errorf("%s should be unsupported", path)
		}
	}
}

func TestResolverPrefersExactThenPrefixThenGlob(t *testing.T) {
	globProvider := testProvider("openai_compatible", false)
	prefixProvider := testProvider("openai_compatible", false)
	exactProvider := testProvider("openai_compatible", false)
	now := time.Now()
	routes := []store.ModelRoute{
		testRoute(globProvider, "glob", "gpt-*", 1, now),
		testRoute(prefixProvider, "prefix", "gpt-", 2, now),
		testRoute(exactProvider, "exact", "gpt-4o", 3, now),
	}
	r := NewResolver(
		[]store.Provider{globProvider, prefixProvider, exactProvider},
		routes,
	)
	target, err := r.Resolve("/v1/chat/completions", "gpt-4o")
	if err != nil {
		t.Fatal(err)
	}
	if target.Provider.ID != exactProvider.ID {
		t.Fatalf("exact route lost: %s", target.Provider.ID)
	}

	target, err = r.Resolve("/v1/chat/completions", "gpt-4o-mini")
	if err != nil {
		t.Fatal(err)
	}
	if target.Provider.ID != prefixProvider.ID {
		t.Fatalf("prefix route lost: %s", target.Provider.ID)
	}
}

func TestResolverPriorityAndCreationOrder(t *testing.T) {
	first := testProvider("openai_compatible", false)
	second := testProvider("openai_compatible", false)
	third := testProvider("openai_compatible", false)
	now := time.Now()
	routes := []store.ModelRoute{
		testRoute(third, "prefix", "claude-", 10, now.Add(2*time.Second)),
		testRoute(second, "prefix", "claude-", 5, now.Add(time.Second)),
		testRoute(first, "prefix", "claude-", 5, now),
	}
	r := NewResolver([]store.Provider{first, second, third}, routes)
	target, err := r.Resolve("/v1/chat/completions", "claude-3")
	if err != nil {
		t.Fatal(err)
	}
	if target.Provider.ID != first.ID {
		t.Fatalf("expected lowest priority then oldest, got %s", target.Provider.ID)
	}
}

func TestResolverPrefixPatternMayEndInStar(t *testing.T) {
	p := testProvider("openai_compatible", false)
	r := NewResolver([]store.Provider{p}, []store.ModelRoute{
		testRoute(p, "prefix", "deepseek-*", 100, time.Now()),
	})
	if _, err := r.Resolve("/v1/chat/completions", "deepseek-chat"); err != nil {
		t.Fatalf("prefix with star: %v", err)
	}
	if _, err := r.Resolve("/v1/chat/completions", "gpt-4"); !errors.Is(err, ErrNoRoute) {
		t.Fatalf("unexpected match: %v", err)
	}
}

func TestResolverUsesDefaultProvider(t *testing.T) {
	defaultProvider := testProvider("anthropic", true)
	other := testProvider("openai_compatible", true)
	r := NewResolver([]store.Provider{other, defaultProvider}, nil)
	target, err := r.Resolve("/v1/messages", "claude-3-5-sonnet")
	if err != nil {
		t.Fatal(err)
	}
	if target.Provider.ID != defaultProvider.ID || target.Protocol != "anthropic" {
		t.Fatalf("got %+v", target)
	}
	if target.UpstreamModel != "claude-3-5-sonnet" {
		t.Fatalf("model=%q", target.UpstreamModel)
	}
}

func TestResolverNoDefaultFails(t *testing.T) {
	disabledDefault := testProvider("openai_compatible", true)
	disabledDefault.Enabled = false
	r := NewResolver([]store.Provider{disabledDefault}, nil)
	if _, err := r.Resolve("/v1/chat/completions", "gpt-4o"); !errors.Is(err, ErrNoRoute) {
		t.Fatalf("got %v", err)
	}
}

func TestResolverSkipsDisabledAndMismatched(t *testing.T) {
	disabledRouteProvider := testProvider("openai_compatible", false)
	disabledProvider := testProvider("openai_compatible", false)
	disabledProvider.Enabled = false
	mismatched := testProvider("openai_compatible", false)

	disabledRoute := testRoute(disabledRouteProvider, "exact", "gpt-4o", 1, time.Now())
	disabledRoute.Enabled = false
	r := NewResolver(
		[]store.Provider{disabledRouteProvider, disabledProvider, mismatched},
		[]store.ModelRoute{
			disabledRoute,
			testRoute(disabledProvider, "exact", "gpt-4o", 1, time.Now()),
			{
				ID:         uuid.New(),
				ProviderID: mismatched.ID,
				Protocol:   "anthropic",
				Pattern:    "gpt-4o",
				MatchType:  "exact",
				Priority:   1,
				Enabled:    true,
				CreatedAt:  time.Now(),
			},
		},
	)
	if _, err := r.Resolve("/v1/chat/completions", "gpt-4o"); !errors.Is(err, ErrNoRoute) {
		t.Fatalf("got %v", err)
	}
}

func TestResolverRejectsCrossProtocolRoute(t *testing.T) {
	anthropic := testProvider("anthropic", false)
	openai := testProvider("openai_compatible", false)
	r := NewResolver(
		[]store.Provider{anthropic, openai},
		[]store.ModelRoute{
			{
				ID:         uuid.New(),
				ProviderID: openai.ID,
				Protocol:   "openai_compatible",
				Pattern:    "claude-*",
				MatchType:  "glob",
				Priority:   1,
				Enabled:    true,
				CreatedAt:  time.Now(),
			},
		},
	)
	if _, err := r.Resolve("/v1/messages", "claude-3"); !errors.Is(err, ErrNoRoute) {
		t.Fatalf("got %v", err)
	}
}

func TestResolverUsesUpstreamModelAlias(t *testing.T) {
	p := testProvider("openai_compatible", false)
	alias := "provider-real-model"
	route := testRoute(p, "exact", "company-model", 1, time.Now())
	route.UpstreamModel = &alias
	r := NewResolver([]store.Provider{p}, []store.ModelRoute{route})
	target, err := r.Resolve("/v1/chat/completions", "company-model")
	if err != nil {
		t.Fatal(err)
	}
	if target.UpstreamModel != alias {
		t.Fatalf("upstream model=%q", target.UpstreamModel)
	}
}

func TestResolverUnsupportedPath(t *testing.T) {
	r := NewResolver(nil, nil)
	if _, err := r.Resolve("/v1/unknown", "x"); !errors.Is(err, ErrUnsupportedEndpoint) {
		t.Fatalf("got %v", err)
	}
}
