package adminapi

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/company/sluice/internal/dlp"
	"github.com/company/sluice/internal/store"
)

const (
	maxDLPTestBodyBytes = 1 << 20
	maxDLPTestTextBytes = 256 * 1024
	maxDLPTestRules     = 200
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

func (p *dlpTestRulePayload) toStore() (store.DLPRule, string) {
	rule := dlpRulePayload{
		Name:         p.Name,
		Description:  p.Description,
		Category:     p.Category,
		MatchType:    p.MatchType,
		Pattern:      p.Pattern,
		Action:       p.Action,
		Replacement:  p.Replacement,
		Priority:     p.Priority,
		Enabled:      p.Enabled,
		AlertEnabled: &p.AlertEnabled,
	}
	if message := rule.normalize(); message != "" {
		return store.DLPRule{}, message
	}
	enabled := true
	if rule.Enabled != nil {
		enabled = *rule.Enabled
	}
	priority := 100
	if rule.Priority != nil {
		priority = *rule.Priority
	}
	return store.DLPRule{
		ID:           uuid.New(),
		Name:         rule.Name,
		Description:  rule.Description,
		Category:     rule.Category,
		MatchType:    rule.MatchType,
		Pattern:      rule.Pattern,
		Action:       rule.Action,
		Replacement:  rule.Replacement,
		Priority:     priority,
		Enabled:      enabled,
		AlertEnabled: p.AlertEnabled,
	}, ""
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
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxDLPTestBodyBytes)
	var payload dlpTestRequest
	if err := c.ShouldBindJSON(&payload); err != nil {
		BadRequest(c, "invalid JSON body")
		return
	}
	if len(payload.Text) > maxDLPTestTextBytes {
		BadRequest(c, "text is too long")
		return
	}
	if len(payload.Rules) > maxDLPTestRules {
		BadRequest(c, "too many rules")
		return
	}

	var rules []store.DLPRule
	if payload.Rules != nil {
		rules = make([]store.DLPRule, 0, len(payload.Rules))
		for i := range payload.Rules {
			rule, message := payload.Rules[i].toStore()
			if message != "" {
				BadRequest(c, "invalid rule: "+message)
				return
			}
			rules = append(rules, rule)
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
		switch {
		case errors.Is(err, dlp.ErrUnsupportedContent):
			Fail(c, http.StatusUnsupportedMediaType, "unsupported_content", "text contains unsupported content")
		case errors.Is(err, dlp.ErrTooManyMatches):
			BadRequest(c, "text has too many matches")
		default:
			InternalError(c)
		}
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
