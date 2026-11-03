package adminapi

import (
	"context"
	"errors"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/company/api-key/internal/store"
)

// RouteHandler serves model route administration endpoints.
type RouteHandler struct {
	routes    store.RouteStore
	providers store.ProviderStore
	refresh   func(context.Context)
}

// NewRouteHandler creates the model route handler. refresh is called after
// mutations so the runtime resolver picks up the change.
func NewRouteHandler(routes store.RouteStore, providers store.ProviderStore, refresh func(context.Context)) *RouteHandler {
	return &RouteHandler{routes: routes, providers: providers, refresh: refresh}
}

type routeDTO struct {
	ID            string    `json:"id"`
	ProviderID    string    `json:"provider_id"`
	Protocol      string    `json:"protocol"`
	Pattern       string    `json:"pattern"`
	MatchType     string    `json:"match_type"`
	UpstreamModel *string   `json:"upstream_model"`
	Priority      int       `json:"priority"`
	Enabled       bool      `json:"enabled"`
	CreatedAt     time.Time `json:"created_at"`
}

func toRouteDTO(r *store.ModelRoute) routeDTO {
	return routeDTO{
		ID:            r.ID.String(),
		ProviderID:    r.ProviderID.String(),
		Protocol:      r.Protocol,
		Pattern:       r.Pattern,
		MatchType:     r.MatchType,
		UpstreamModel: r.UpstreamModel,
		Priority:      r.Priority,
		Enabled:       r.Enabled,
		CreatedAt:     r.CreatedAt,
	}
}

type routePayload struct {
	ProviderID    string `json:"provider_id"`
	Protocol      string `json:"protocol"`
	Pattern       string `json:"pattern"`
	MatchType     string `json:"match_type"`
	UpstreamModel string `json:"upstream_model"`
	Priority      *int   `json:"priority"`
	Enabled       *bool  `json:"enabled"`
}

func (p *routePayload) normalize() string {
	p.Protocol = strings.TrimSpace(p.Protocol)
	p.Pattern = strings.TrimSpace(p.Pattern)
	p.MatchType = strings.TrimSpace(p.MatchType)
	p.UpstreamModel = strings.TrimSpace(p.UpstreamModel)

	if p.MatchType == "" {
		p.MatchType = "prefix"
	}
	switch p.MatchType {
	case "exact", "prefix", "glob":
	default:
		return "match_type must be exact, prefix, or glob"
	}
	if p.Pattern == "" {
		return "pattern is required"
	}
	if len(p.Pattern) > 255 {
		return "pattern is too long"
	}
	if len(p.UpstreamModel) > 255 {
		return "upstream_model is too long"
	}
	if p.MatchType == "glob" {
		if _, err := path.Match(p.Pattern, ""); err != nil {
			return "pattern is not a valid glob"
		}
	}
	if p.Protocol != "openai_compatible" && p.Protocol != "anthropic" {
		return "protocol must be openai_compatible or anthropic"
	}
	if p.Priority != nil && *p.Priority < 0 {
		return "priority must be non-negative"
	}
	return ""
}

// List handles GET /admin/api/model-routes.
func (h *RouteHandler) List(c *gin.Context) {
	items, err := h.routes.List(c.Request.Context())
	if err != nil {
		InternalError(c)
		return
	}
	out := make([]routeDTO, 0, len(items))
	for i := range items {
		out = append(out, toRouteDTO(&items[i]))
	}
	OK(c, http.StatusOK, gin.H{"items": out})
}

// Create handles POST /admin/api/model-routes.
func (h *RouteHandler) Create(c *gin.Context) {
	var payload routePayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		BadRequest(c, "invalid JSON body")
		return
	}
	if msg := payload.normalize(); msg != "" {
		BadRequest(c, msg)
		return
	}
	providerID, err := uuid.Parse(strings.TrimSpace(payload.ProviderID))
	if err != nil {
		BadRequest(c, "provider_id is required")
		return
	}

	ctx := c.Request.Context()
	provider, err := h.providers.Get(ctx, providerID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			BadRequest(c, "provider not found")
			return
		}
		InternalError(c)
		return
	}
	if provider.Protocol != payload.Protocol {
		BadRequest(c, "route protocol must match provider protocol")
		return
	}

	priority := 100
	if payload.Priority != nil {
		priority = *payload.Priority
	}
	enabled := true
	if payload.Enabled != nil {
		enabled = *payload.Enabled
	}
	var upstream *string
	if payload.UpstreamModel != "" {
		upstream = &payload.UpstreamModel
	}
	r := &store.ModelRoute{
		ProviderID:    providerID,
		Protocol:      payload.Protocol,
		Pattern:       payload.Pattern,
		MatchType:     payload.MatchType,
		UpstreamModel: upstream,
		Priority:      priority,
		Enabled:       enabled,
	}
	if err := h.routes.Create(ctx, r); err != nil {
		InternalError(c)
		return
	}
	if h.refresh != nil {
		h.refresh(ctx)
	}
	OK(c, http.StatusCreated, toRouteDTO(r))
}

// Delete handles DELETE /admin/api/model-routes/:id.
func (h *RouteHandler) Delete(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.routes.Delete(c.Request.Context(), id); err != nil {
		writeStoreError(c, err, "route not found", "route conflict", "invalid route")
		return
	}
	if h.refresh != nil {
		h.refresh(c.Request.Context())
	}
	OK(c, http.StatusOK, gin.H{"deleted": true})
}
