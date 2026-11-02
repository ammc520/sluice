// Package dlp inspects request text for sensitive data and applies
// block/mask/audit actions.
package dlp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/company/api-key/internal/store"
)

// ErrUnsupportedContent reports binary, image, file, or audio content that
// the text-only DLP engine cannot inspect safely.
var ErrUnsupportedContent = errors.New("dlp: unsupported binary or media content")

// Result is the outcome of inspecting a request body or text.
type Result struct {
	Action string
	Hits   []Hit
	Body   []byte
}

// Hit is one rule match.
type Hit struct {
	RuleID       uuid.UUID
	RuleName     string
	Action       string
	AlertEnabled bool
	Start        int
	End          int
}

type compiledRule struct {
	rule     store.DLPRule
	re       *regexp.Regexp
	validate func(string) bool
}

// Engine inspects text with a reloadable set of rules.
type Engine struct {
	mu    sync.RWMutex
	rules []compiledRule
}

// NewEngine compiles enabled rules. Invalid rules are skipped and reported so
// a single bad rule cannot take down DLP enforcement.
func NewEngine(rules []store.DLPRule) (*Engine, []error) {
	compiled, errs := compileRules(rules)
	return &Engine{rules: compiled}, errs
}

// Reload atomically replaces the active rule set.
func (e *Engine) Reload(rules []store.DLPRule) []error {
	compiled, errs := compileRules(rules)
	e.mu.Lock()
	e.rules = compiled
	e.mu.Unlock()
	return errs
}

func compileRules(rules []store.DLPRule) ([]compiledRule, []error) {
	out := make([]compiledRule, 0, len(rules))
	var errs []error
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		if rule.Action != "block" && rule.Action != "mask" && rule.Action != "audit" {
			errs = append(errs, fmt.Errorf("dlp: rule %q: invalid action %q", rule.Name, rule.Action))
			continue
		}
		pattern := rule.Pattern
		if rule.MatchType == "keyword" {
			pattern = "(?i)" + regexp.QuoteMeta(rule.Pattern)
		} else if rule.MatchType != "regex" {
			errs = append(errs, fmt.Errorf("dlp: rule %q: invalid match type %q", rule.Name, rule.MatchType))
			continue
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			errs = append(errs, fmt.Errorf("dlp: rule %q: %w", rule.Name, err))
			continue
		}
		if rule.Replacement == "" {
			rule.Replacement = "***"
		}
		out = append(out, compiledRule{rule: rule, re: re, validate: validatorFor(rule)})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].rule.Priority != out[j].rule.Priority {
			return out[i].rule.Priority < out[j].rule.Priority
		}
		return out[i].rule.CreatedAt.Before(out[j].rule.CreatedAt)
	})
	return out, errs
}

func (e *Engine) snapshot() []compiledRule {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return append([]compiledRule(nil), e.rules...)
}

// Inspect scans a JSON request body, or plain text when the body is not JSON.
// For JSON, all string values except control fields are scanned and masked.
func (e *Engine) Inspect(body []byte) (Result, error) {
	if len(body) == 0 {
		return Result{Action: "none", Body: body}, nil
	}
	if !utf8.Valid(body) || bytes.IndexByte(body, 0) >= 0 {
		return Result{}, ErrUnsupportedContent
	}

	rules := e.snapshot()
	if !json.Valid(body) {
		return scanText(string(body), rules), nil
	}

	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return scanText(string(body), rules), nil
	}
	if err := rejectUnsupported(value); err != nil {
		return Result{}, err
	}

	result := Result{Action: "none"}
	rewritten, err := inspectValue(value, rules, &result)
	if err != nil {
		return Result{}, err
	}
	// Preserve the exact request bytes unless masking requires a rewrite.
	if !hasMaskHits(result.Hits) {
		result.Body = body
		return result, nil
	}
	encoded, err := json.Marshal(rewritten)
	if err != nil {
		return Result{}, err
	}
	result.Body = encoded
	return result, nil
}

func hasMaskHits(hits []Hit) bool {
	for _, hit := range hits {
		if hit.Action == "mask" {
			return true
		}
	}
	return false
}

// Test scans input as plain text and returns the masked preview.
func (e *Engine) Test(text string) (Result, error) {
	if !utf8.ValidString(text) || strings.IndexByte(text, 0) >= 0 {
		return Result{}, ErrUnsupportedContent
	}
	return scanText(text, e.snapshot()), nil
}

var skippedKeys = map[string]bool{
	"model":                 true,
	"role":                  true,
	"type":                  true,
	"id":                    true,
	"stream":                true,
	"max_tokens":            true,
	"max_completion_tokens": true,
	"temperature":           true,
	"top_p":                 true,
	"stop":                  true,
}

var unsupportedTypes = map[string]bool{
	"image_url":   true,
	"image":       true,
	"document":    true,
	"input_audio": true,
	"file":        true,
}

func inspectValue(value any, rules []compiledRule, result *Result) (any, error) {
	switch typed := value.(type) {
	case string:
		scanned := scanText(typed, rules)
		mergeResult(result, scanned)
		return string(scanned.Body), nil
	case map[string]any:
		for key, child := range typed {
			if skippedKeys[strings.ToLower(key)] {
				continue
			}
			rewritten, err := inspectValue(child, rules, result)
			if err != nil {
				return nil, err
			}
			typed[key] = rewritten
		}
		return typed, nil
	case []any:
		for i, child := range typed {
			rewritten, err := inspectValue(child, rules, result)
			if err != nil {
				return nil, err
			}
			typed[i] = rewritten
		}
		return typed, nil
	default:
		return value, nil
	}
}

func rejectUnsupported(value any) error {
	switch typed := value.(type) {
	case map[string]any:
		if raw, ok := typed["type"]; ok {
			if kind, ok := raw.(string); ok && unsupportedTypes[kind] {
				return ErrUnsupportedContent
			}
		}
		if _, ok := typed["file_data"]; ok {
			return ErrUnsupportedContent
		}
		if source, ok := typed["source"].(map[string]any); ok {
			if _, ok := source["data"]; ok {
				return ErrUnsupportedContent
			}
		}
		for _, child := range typed {
			if err := rejectUnsupported(child); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range typed {
			if err := rejectUnsupported(child); err != nil {
				return err
			}
		}
	}
	return nil
}

func mergeResult(dst *Result, src Result) {
	dst.Hits = append(dst.Hits, src.Hits...)
	if actionRank(src.Action) > actionRank(dst.Action) {
		dst.Action = src.Action
	}
}

type maskInterval struct {
	start       int
	end         int
	replacement string
	priority    int
}

func scanText(text string, rules []compiledRule) Result {
	result := Result{Action: "none"}
	var masks []maskInterval

	for _, compiled := range rules {
		for _, loc := range compiled.re.FindAllStringIndex(text, -1) {
			if compiled.validate != nil && !compiled.validate(text[loc[0]:loc[1]]) {
				continue
			}
			hit := Hit{
				RuleID:       compiled.rule.ID,
				RuleName:     compiled.rule.Name,
				Action:       compiled.rule.Action,
				AlertEnabled: compiled.rule.AlertEnabled,
				Start:        loc[0],
				End:          loc[1],
			}
			result.Hits = append(result.Hits, hit)
			if actionRank(hit.Action) > actionRank(result.Action) {
				result.Action = hit.Action
			}
			if hit.Action == "mask" {
				masks = append(masks, maskInterval{
					start: loc[0], end: loc[1],
					replacement: compiled.rule.Replacement,
					priority:    compiled.rule.Priority,
				})
			}
		}
	}

	result.Body = []byte(applyMasks(text, masks))
	return result
}

func applyMasks(text string, masks []maskInterval) string {
	if len(masks) == 0 {
		return text
	}
	sort.SliceStable(masks, func(i, j int) bool {
		if masks[i].start != masks[j].start {
			return masks[i].start < masks[j].start
		}
		if masks[i].end != masks[j].end {
			return masks[i].end > masks[j].end
		}
		return masks[i].priority < masks[j].priority
	})

	merged := make([]maskInterval, 0, len(masks))
	for _, current := range masks {
		if len(merged) == 0 {
			merged = append(merged, current)
			continue
		}
		last := &merged[len(merged)-1]
		if current.start < last.end {
			if current.end > last.end {
				last.end = current.end
			}
			if current.priority < last.priority {
				last.priority = current.priority
				last.replacement = current.replacement
			}
			continue
		}
		merged = append(merged, current)
	}

	for i := len(merged) - 1; i >= 0; i-- {
		interval := merged[i]
		text = text[:interval.start] + interval.replacement + text[interval.end:]
	}
	return text
}

func actionRank(action string) int {
	switch action {
	case "block":
		return 3
	case "mask":
		return 2
	case "audit":
		return 1
	default:
		return 0
	}
}
