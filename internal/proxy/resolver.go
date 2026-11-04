package proxy

import (
	"path"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/company/sluice/internal/store"
)

var matchRank = map[string]int{"exact": 0, "prefix": 1, "glob": 2}

// Resolver selects a provider and upstream model for a gateway request.
type Resolver struct {
	providers []store.Provider
	byID      map[uuid.UUID]store.Provider
	routes    []store.ModelRoute
}

// NewResolver snapshots providers and routes. Callers rebuild it after admin
// mutations.
func NewResolver(providers []store.Provider, routes []store.ModelRoute) *Resolver {
	byID := make(map[uuid.UUID]store.Provider, len(providers))
	for _, p := range providers {
		byID[p.ID] = p
	}
	return &Resolver{providers: providers, byID: byID, routes: append([]store.ModelRoute(nil), routes...)}
}

// Resolve returns the target for a path and requested model.
func (r *Resolver) Resolve(requestPath, model string) (Target, error) {
	protocol, ok := ProtocolForPath(requestPath)
	if !ok {
		return Target{}, ErrUnsupportedEndpoint
	}

	type candidate struct {
		route store.ModelRoute
	}
	var matches []candidate
	for _, route := range r.routes {
		if !route.Enabled || route.Protocol != protocol || route.MatchType == "" {
			continue
		}
		provider, exists := r.byID[route.ProviderID]
		if !exists || !provider.Enabled || provider.Protocol != protocol {
			continue
		}
		if routeMatches(route, model) {
			matches = append(matches, candidate{route: route})
		}
	}

	if len(matches) > 0 {
		sort.SliceStable(matches, func(i, j int) bool {
			ri, rj := matches[i].route, matches[j].route
			if matchRank[ri.MatchType] != matchRank[rj.MatchType] {
				return matchRank[ri.MatchType] < matchRank[rj.MatchType]
			}
			if ri.Priority != rj.Priority {
				return ri.Priority < rj.Priority
			}
			return ri.CreatedAt.Before(rj.CreatedAt)
		})
		best := matches[0].route
		provider := r.byID[best.ProviderID]
		target := Target{Provider: provider, Protocol: protocol, UpstreamModel: model}
		if best.UpstreamModel != nil && *best.UpstreamModel != "" {
			target.UpstreamModel = *best.UpstreamModel
		}
		return target, nil
	}

	for _, provider := range r.providers {
		if provider.Enabled && provider.IsDefault && provider.Protocol == protocol {
			return Target{Provider: provider, Protocol: protocol, UpstreamModel: model}, nil
		}
	}
	return Target{}, ErrNoRoute
}

func routeMatches(route store.ModelRoute, model string) bool {
	switch route.MatchType {
	case "exact":
		return model == route.Pattern
	case "prefix":
		prefix := strings.TrimSuffix(route.Pattern, "*")
		return strings.HasPrefix(model, prefix)
	case "glob":
		ok, err := path.Match(route.Pattern, model)
		return err == nil && ok
	default:
		return false
	}
}

// ProtocolForPath maps an exact supported gateway path to its wire protocol.
// Prefix matching is deliberately avoided so path traversal cannot reach
// arbitrary endpoints on the provider host.
func ProtocolForPath(requestPath string) (string, bool) {
	switch requestPath {
	case "/v1/messages", "/v1/messages/count_tokens":
		return "anthropic", true
	case "/v1/chat/completions", "/v1/completions", "/v1/embeddings", "/v1/models":
		return "openai_compatible", true
	default:
		return "", false
	}
}
