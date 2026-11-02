package dlp

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/company/api-key/internal/store"
)

func testRule(name, pattern, action string, priority int) store.DLPRule {
	return store.DLPRule{
		Name: name, MatchType: "regex", Pattern: pattern, Action: action,
		Replacement: "***", Priority: priority, Enabled: true,
	}
}

func mustEngine(t *testing.T, rules ...store.DLPRule) *Engine {
	t.Helper()
	engine, errs := NewEngine(rules)
	if len(errs) > 0 {
		t.Fatalf("unexpected compile errors: %v", errs)
	}
	return engine
}

func builtinRule(t *testing.T, name string) store.DLPRule {
	t.Helper()
	for _, rule := range BuiltinRules() {
		if rule.Name == name {
			return rule
		}
	}
	t.Fatalf("builtin %q not found", name)
	return store.DLPRule{}
}

func TestBlockWinsOverMask(t *testing.T) {
	engine := mustEngine(t,
		testRule("phone", `1[3-9]\d{9}`, "mask", 1),
		testRule("aws", `AKIA[0-9A-Z]{16}`, "block", 2),
	)
	res, err := engine.Inspect([]byte(`{"messages":[{"role":"user","content":"AKIAABCDEFGHIJKLMNOP 13800138000"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.Action != "block" {
		t.Fatalf("got %s", res.Action)
	}
	if len(res.Hits) != 2 {
		t.Fatalf("hits %+v", res.Hits)
	}
}

func TestMaskRewritesAllHitsAndKeepsValidJSON(t *testing.T) {
	engine := mustEngine(t, testRule("phone", `1[3-9]\d{9}`, "mask", 1))
	input := []byte(`{"model":"gpt-4o","max_tokens":1024,"messages":[{"role":"user","content":"a 13800138000 b 13900139000"}]}`)
	res, err := engine.Inspect(input)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(res.Body, &out); err != nil {
		t.Fatalf("invalid JSON: %v: %s", err, res.Body)
	}
	if strings.Contains(string(res.Body), "13800138000") || strings.Contains(string(res.Body), "13900139000") {
		t.Fatalf("unmasked body %s", res.Body)
	}
	if !strings.Contains(string(res.Body), `"model":"gpt-4o"`) {
		t.Fatalf("model lost: %s", res.Body)
	}
	if out["max_tokens"] != float64(1024) {
		t.Fatalf("max_tokens %#v", out["max_tokens"])
	}
	if len(res.Hits) != 2 || res.Action != "mask" {
		t.Fatalf("result %+v", res)
	}
}

func TestImageContentIsRejected(t *testing.T) {
	engine := mustEngine(t)
	cases := []string{
		`{"model":"gpt-4o","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}]}]}`,
		`{"messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AAAA"}}]}]}`,
		`{"messages":[{"role":"user","content":[{"type":"input_audio","input_audio":{"data":"AAAA","format":"wav"}}]}]}`,
		`{"messages":[{"role":"user","content":"x","file_data":"AAAA"}]}`,
	}
	for _, input := range cases {
		if _, err := engine.Inspect([]byte(input)); !errors.Is(err, ErrUnsupportedContent) {
			t.Fatalf("%s: got %v", input, err)
		}
	}
}

func TestBankCardLuhnValidation(t *testing.T) {
	rule := builtinRule(t, BuiltinBankCardName)
	engine := mustEngine(t, rule)
	input := []byte(`{"messages":[{"content":"valid 4111111111111111 invalid 4111111111111112"}]}`)
	res, err := engine.Inspect(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) != 1 {
		t.Fatalf("hits %+v body %s", res.Hits, res.Body)
	}
	if strings.Contains(string(res.Body), "4111111111111111") {
		t.Fatalf("valid card not masked: %s", res.Body)
	}
	if !strings.Contains(string(res.Body), "4111111111111112") {
		t.Fatalf("invalid card should not be masked: %s", res.Body)
	}
}

func TestIDCardChecksumValidation(t *testing.T) {
	rule := builtinRule(t, BuiltinIDCardName)
	engine := mustEngine(t, rule)
	input := []byte(`{"messages":[{"content":"valid 11010519491231002X invalid 110105194912310021"}]}`)
	res, err := engine.Inspect(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) != 1 {
		t.Fatalf("hits %+v body %s", res.Hits, res.Body)
	}
	if strings.Contains(string(res.Body), "11010519491231002X") {
		t.Fatalf("valid id not masked: %s", res.Body)
	}
	if !strings.Contains(string(res.Body), "110105194912310021") {
		t.Fatalf("invalid id should not be masked: %s", res.Body)
	}
}

func TestPrivateIPAuditOnlyMatchesPrivateAddresses(t *testing.T) {
	rule := builtinRule(t, BuiltinPrivateIPName)
	engine := mustEngine(t, rule)
	input := []byte(`{"messages":[{"content":"10.0.0.5 192.168.1.1 8.8.8.8 999.1.1.1"}]}`)
	res, err := engine.Inspect(input)
	if err != nil {
		t.Fatal(err)
	}
	if res.Action != "audit" || len(res.Hits) != 2 {
		t.Fatalf("result %+v", res)
	}
	if string(res.Body) != string(input) {
		t.Fatalf("audit modified body: %s", res.Body)
	}
}

func TestInvalidRegexIsSkipped(t *testing.T) {
	engine, errs := NewEngine([]store.DLPRule{
		testRule("broken", "(", "mask", 1),
		testRule("keyword", "secret", "block", 2),
	})
	if len(errs) != 1 {
		t.Fatalf("errors %v", errs)
	}
	res, err := engine.Test("a secret value")
	if err != nil {
		t.Fatal(err)
	}
	if res.Action != "block" || len(res.Hits) != 1 {
		t.Fatalf("result %+v", res)
	}
}

func TestKeywordMatchIsCaseInsensitive(t *testing.T) {
	rule := store.DLPRule{
		Name: "project", MatchType: "keyword", Pattern: "Project Falcon",
		Action: "audit", Enabled: true, Priority: 10,
	}
	engine := mustEngine(t, rule)
	res, err := engine.Test("the project FALCON launch")
	if err != nil {
		t.Fatal(err)
	}
	if res.Action != "audit" || len(res.Hits) != 1 {
		t.Fatalf("result %+v", res)
	}
}

func TestControlFieldsAreNotScanned(t *testing.T) {
	rule := builtinRule(t, BuiltinCloudKeyName)
	engine := mustEngine(t, rule)
	input := []byte(`{"model":"AKIAABCDEFGHIJKLMNOP","stream":true,"messages":[{"role":"user","content":"hello"}]}`)
	res, err := engine.Inspect(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) != 0 || res.Action != "none" {
		t.Fatalf("control field scanned: %+v", res)
	}
}

func TestToolDescriptionsAreScanned(t *testing.T) {
	rule := builtinRule(t, BuiltinCloudKeyName)
	engine := mustEngine(t, rule)
	input := []byte(`{"tools":[{"type":"function","function":{"name":"lookup","description":"uses AKIAABCDEFGHIJKLMNOP"}}]}`)
	res, err := engine.Inspect(input)
	if err != nil {
		t.Fatal(err)
	}
	if res.Action != "block" || len(res.Hits) != 1 {
		t.Fatalf("result %+v", res)
	}
}

func TestNonJSONBodyIsScannedAsText(t *testing.T) {
	engine := mustEngine(t, testRule("phone", `1[3-9]\d{9}`, "mask", 1))
	res, err := engine.Inspect([]byte("call 13800138000 now"))
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Body) != "call *** now" || res.Action != "mask" {
		t.Fatalf("body %q action %q", res.Body, res.Action)
	}
}

func TestBinaryBodyIsRejected(t *testing.T) {
	engine := mustEngine(t)
	if _, err := engine.Inspect([]byte{0xff, 0xfe, 0x00, 0x01}); !errors.Is(err, ErrUnsupportedContent) {
		t.Fatalf("got %v", err)
	}
}

func TestOverlappingMasksUseHighestPriorityReplacement(t *testing.T) {
	short := testRule("short", `\d{4}`, "mask", 1)
	short.Replacement = "A"
	engine := mustEngine(t,
		short,
		store.DLPRule{Name: "long", MatchType: "regex", Pattern: `\d{6}`, Action: "mask",
			Replacement: "B", Priority: 2, Enabled: true},
	)
	res, err := engine.Test("x123456y")
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Body) != "xAy" {
		t.Fatalf("body %q", res.Body)
	}
}

func TestTestReturnsOffsetsAndMaskedPreview(t *testing.T) {
	engine := mustEngine(t, testRule("phone", `1[3-9]\d{9}`, "mask", 1))
	res, err := engine.Test("a 13800138000 b")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) != 1 {
		t.Fatalf("hits %+v", res.Hits)
	}
	hit := res.Hits[0]
	if hit.Start != 2 || hit.End != 13 {
		t.Fatalf("offsets %+v", hit)
	}
	if string(res.Body) != "a *** b" {
		t.Fatalf("body %q", res.Body)
	}
}

func TestNoRulesPreservesBody(t *testing.T) {
	engine := mustEngine(t)
	input := []byte("{ \"model\" : \"gpt-4o\", \"messages\" : [] }")
	res, err := engine.Inspect(input)
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Body) != string(input) || res.Action != "none" {
		t.Fatalf("result %+v", res)
	}
}

func TestDisabledAndAuditRules(t *testing.T) {
	engine := mustEngine(t,
		store.DLPRule{Name: "off", MatchType: "keyword", Pattern: "secret", Action: "block", Enabled: false},
		store.DLPRule{Name: "audit", MatchType: "keyword", Pattern: "internal", Action: "audit", Enabled: true},
	)
	res, err := engine.Test("secret internal")
	if err != nil {
		t.Fatal(err)
	}
	if res.Action != "audit" || len(res.Hits) != 1 || res.Hits[0].RuleName != "audit" {
		t.Fatalf("result %+v", res)
	}
}
