package adminapi

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/company/api-key/internal/store"
)

// DLPRuleHandler serves DLP rule administration endpoints.
type DLPRuleHandler struct {
	rules   store.DLPRuleStore
	refresh func(context.Context)
}

// NewDLPRuleHandler creates the DLP rule handler. refresh is called after
// mutations so the DLP engine reloads its compiled rules.
func NewDLPRuleHandler(rules store.DLPRuleStore, refresh func(context.Context)) *DLPRuleHandler {
	return &DLPRuleHandler{rules: rules, refresh: refresh}
}

type dlpRuleDTO struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	Category     string    `json:"category"`
	MatchType    string    `json:"match_type"`
	Pattern      string    `json:"pattern"`
	Action       string    `json:"action"`
	Replacement  string    `json:"replacement"`
	Priority     int       `json:"priority"`
	Enabled      bool      `json:"enabled"`
	Builtin      bool      `json:"builtin"`
	AlertEnabled bool      `json:"alert_enabled"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func toDLPRuleDTO(r *store.DLPRule) dlpRuleDTO {
	return dlpRuleDTO{
		ID:           r.ID.String(),
		Name:         r.Name,
		Description:  r.Description,
		Category:     r.Category,
		MatchType:    r.MatchType,
		Pattern:      r.Pattern,
		Action:       r.Action,
		Replacement:  r.Replacement,
		Priority:     r.Priority,
		Enabled:      r.Enabled,
		Builtin:      r.Builtin,
		AlertEnabled: r.AlertEnabled,
		CreatedAt:    r.CreatedAt,
		UpdatedAt:    r.UpdatedAt,
	}
}

type dlpRulePayload struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	Category     string `json:"category"`
	MatchType    string `json:"match_type"`
	Pattern      string `json:"pattern"`
	Action       string `json:"action"`
	Replacement  string `json:"replacement"`
	Priority     *int   `json:"priority"`
	Enabled      *bool  `json:"enabled"`
	AlertEnabled *bool  `json:"alert_enabled"`
}

func (p *dlpRulePayload) normalize() string {
	p.Name = strings.TrimSpace(p.Name)
	p.Description = strings.TrimSpace(p.Description)
	p.Category = strings.TrimSpace(p.Category)
	p.MatchType = strings.TrimSpace(p.MatchType)
	p.Pattern = strings.TrimSpace(p.Pattern)
	p.Action = strings.TrimSpace(p.Action)
	p.Replacement = strings.TrimSpace(p.Replacement)

	if p.Category == "" {
		p.Category = "custom"
	}
	switch {
	case p.Name == "":
		return "name is required"
	case len(p.Name) > 128:
		return "name is too long"
	case len(p.Category) > 64:
		return "category is too long"
	case p.MatchType != "regex" && p.MatchType != "keyword":
		return "match_type must be regex or keyword"
	case p.Pattern == "":
		return "pattern is required"
	case len(p.Pattern) > 4096:
		return "pattern is too long"
	case p.Action != "block" && p.Action != "mask" && p.Action != "audit":
		return "action must be block, mask, or audit"
	case p.Priority != nil && *p.Priority < 0:
		return "priority must be non-negative"
	}
	if p.MatchType == "regex" {
		if _, err := regexp.Compile(p.Pattern); err != nil {
			return "pattern is not a valid regular expression"
		}
	}
	if p.Replacement == "" {
		p.Replacement = "***"
	}
	if len(p.Replacement) > 128 {
		return "replacement is too long"
	}
	return ""
}

// List handles GET /admin/api/dlp-rules.
func (h *DLPRuleHandler) List(c *gin.Context) {
	items, err := h.rules.List(c.Request.Context())
	if err != nil {
		InternalError(c)
		return
	}
	out := make([]dlpRuleDTO, 0, len(items))
	for i := range items {
		out = append(out, toDLPRuleDTO(&items[i]))
	}
	OK(c, http.StatusOK, gin.H{"items": out})
}

// Create handles POST /admin/api/dlp-rules.
func (h *DLPRuleHandler) Create(c *gin.Context) {
	var payload dlpRulePayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		BadRequest(c, "invalid JSON body")
		return
	}
	if msg := payload.normalize(); msg != "" {
		BadRequest(c, msg)
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
	alertEnabled := payload.AlertEnabled != nil && *payload.AlertEnabled

	r := &store.DLPRule{
		Name:         payload.Name,
		Description:  payload.Description,
		Category:     payload.Category,
		MatchType:    payload.MatchType,
		Pattern:      payload.Pattern,
		Action:       payload.Action,
		Replacement:  payload.Replacement,
		Priority:     priority,
		Enabled:      enabled,
		AlertEnabled: alertEnabled,
	}
	if err := h.rules.Create(c.Request.Context(), r); err != nil {
		writeStoreError(c, err, "rule not found", "a rule with this name already exists", "invalid rule")
		return
	}
	h.reload(c.Request.Context())
	OK(c, http.StatusCreated, toDLPRuleDTO(r))
}

// Update handles PUT /admin/api/dlp-rules/:id.
func (h *DLPRuleHandler) Update(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var payload dlpRulePayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		BadRequest(c, "invalid JSON body")
		return
	}
	if msg := payload.normalize(); msg != "" {
		BadRequest(c, msg)
		return
	}

	ctx := c.Request.Context()
	r, err := h.rules.Get(ctx, id)
	if err != nil {
		writeStoreError(c, err, "rule not found", "a rule with this name already exists", "invalid rule")
		return
	}
	if r.Builtin && (payload.Name != r.Name || payload.Category != r.Category ||
		payload.MatchType != r.MatchType || payload.Pattern != r.Pattern) {
		BadRequest(c, "builtin rules only allow changing description, action, replacement, priority, and flags")
		return
	}
	r.Name = payload.Name
	r.Description = payload.Description
	r.Category = payload.Category
	r.MatchType = payload.MatchType
	r.Pattern = payload.Pattern
	r.Action = payload.Action
	r.Replacement = payload.Replacement
	if payload.Priority != nil {
		r.Priority = *payload.Priority
	}
	if payload.Enabled != nil {
		r.Enabled = *payload.Enabled
	}
	if payload.AlertEnabled != nil {
		r.AlertEnabled = *payload.AlertEnabled
	}
	if err := h.rules.Update(ctx, r); err != nil {
		writeStoreError(c, err, "rule not found", "a rule with this name already exists", "invalid rule")
		return
	}
	h.reload(ctx)
	OK(c, http.StatusOK, toDLPRuleDTO(r))
}

// Delete handles DELETE /admin/api/dlp-rules/:id.
func (h *DLPRuleHandler) Delete(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	r, err := h.rules.Get(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			Fail(c, http.StatusNotFound, "not_found", "rule not found")
			return
		}
		InternalError(c)
		return
	}
	if r.Builtin {
		Fail(c, http.StatusBadRequest, "invalid_request", "builtin rules can be disabled but not deleted")
		return
	}
	if err := h.rules.Delete(ctx, id); err != nil {
		writeStoreError(c, err, "rule not found", "a rule with this name already exists", "invalid rule")
		return
	}
	h.reload(ctx)
	OK(c, http.StatusOK, gin.H{"deleted": true})
}

func (h *DLPRuleHandler) reload(ctx context.Context) {
	if h.refresh != nil {
		h.refresh(ctx)
	}
}
