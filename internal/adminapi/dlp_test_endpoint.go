package adminapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/company/api-key/internal/dlp"
	"github.com/company/api-key/internal/store"
)

// DLPTestHandler previews DLP matches without writing data.
type DLPTestHandler struct {
	rules store.DLPRuleStore
}

// NewDLPTestHandler creates the rule testing endpoint handler.
func NewDLPTestHandler(rules store.DLPRuleStore) *DLPTestHandler {
	return &DLPTestHandler{rules: rules}
}

type dlpTestRulePayload struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	Category     string `json:"category"`
	MatchType    string `json:"match_type"`
	Pattern      string `json:"pattern"`
	Action       string `json:"action"`
	Replacement  string `json:"replacement"`
	Priority     *int   `json:"priority"`
	Enabled      *bool  `json:"enabled"`
	AlertEnabled bool   `json:"alert_enabled"`
}

func (p *dlpTestRulePayload) toStore() store.DLPRule {
	enabled := true
	if p.Enabled != nil {
		enabled = *p.Enabled
	}
	priority := 100
	if p.Priority != nil {
		priority = *p.Priority
	}
	category := strings.TrimSpace(p.Category)
	if category == "" {
		category = "custom"
	}
	replacement := p.Replacement
	if replacement == "" {
		replacement = "***"
	}
	return store.DLPRule{
		ID:           uuid.New(),
		Name:         strings.TrimSpace(p.Name),
		Description:  p.Description,
		Category:     category,
		MatchType:    p.MatchType,
		Pattern:      p.Pattern,
		Action:       p.Action,
		Replacement:  replacement,
		Priority:     priority,
		Enabled:      enabled,
		AlertEnabled: p.AlertEnabled,
	}
}

type dlpTestRequest struct {
	Text  string               `json:"text"`
	Rules []dlpTestRulePayload `json:"rules"`
}

type dlpTestHitDTO struct {
	RuleID       string `json:"rule_id"`
	RuleName     string `json:"rule_name"`
	Action       string `json:"action"`
	AlertEnabled bool   `json:"alert_enabled"`
	Start        int    `json:"start"`
	End          int    `json:"end"`
}

// Test handles POST /admin/api/dlp-rules/test.
func (h *DLPTestHandler) Test(c *gin.Context) {
	var payload dlpTestRequest
	if err := c.ShouldBindJSON(&payload); err != nil {
		BadRequest(c, "invalid JSON body")
		return
	}

	var rules []store.DLPRule
	if payload.Rules != nil {
		for i := range payload.Rules {
			rules = append(rules, payload.Rules[i].toStore())
		}
	} else {
		enabled, err := h.rules.ListEnabled(c.Request.Context())
		if err != nil {
			InternalError(c)
			return
		}
		rules = enabled
	}

	engine, compileErrs := dlp.NewEngine(rules)
	if len(compileErrs) > 0 && payload.Rules != nil {
		BadRequest(c, "invalid rule: "+compileErrs[0].Error())
		return
	}
	result, err := engine.Test(payload.Text)
	if err != nil {
		if errors.Is(err, dlp.ErrUnsupportedContent) {
			Fail(c, http.StatusUnsupportedMediaType, "unsupported_content", "text contains unsupported content")
			return
		}
		InternalError(c)
		return
	}

	hits := make([]dlpTestHitDTO, 0, len(result.Hits))
	for _, hit := range result.Hits {
		hits = append(hits, dlpTestHitDTO{
			RuleID:       hit.RuleID.String(),
			RuleName:     hit.RuleName,
			Action:       hit.Action,
			AlertEnabled: hit.AlertEnabled,
			Start:        hit.Start,
			End:          hit.End,
		})
	}
	OK(c, http.StatusOK, gin.H{
		"action":      result.Action,
		"hits":        hits,
		"masked_text": string(result.Body),
	})
}
