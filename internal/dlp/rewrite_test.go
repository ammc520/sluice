package dlp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/company/api-key/internal/store"
)

func TestMaskingNestedAndEscapedStrings(t *testing.T) {
	engine := mustEngine(t, store.DLPRule{
		Name: "project", MatchType: "keyword", Pattern: "secret",
		Action: "mask", Replacement: "[redacted]", Priority: 10, Enabled: true,
	})
	input := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"line1\nsecret 中文"},{"role":"user","content":[{"type":"text","text":"secret twice"}]}]}`)
	res, err := engine.Inspect(input)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(res.Body), "secret") {
		t.Fatalf("secret survived: %s", res.Body)
	}
	if !strings.Contains(string(res.Body), "[redacted]") || !strings.Contains(string(res.Body), "中文") {
		t.Fatalf("body %s", res.Body)
	}
	var decoded map[string]any
	if err := json.Unmarshal(res.Body, &decoded); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if decoded["model"] != "gpt-4o" {
		t.Fatalf("model changed: %#v", decoded["model"])
	}
}

func TestObjectKeysAreNotRewritten(t *testing.T) {
	engine := mustEngine(t, testRule("secret", "secret", "mask", 1))
	input := []byte(`{"messages":[{"secret":"value"}]}`)
	res, err := engine.Inspect(input)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(res.Body), `"secret":"value"`) {
		t.Fatalf("key was rewritten: %s", res.Body)
	}
}

func TestAuditRulePreservesOriginalBytes(t *testing.T) {
	engine := mustEngine(t, testRule("internal", "internal", "audit", 1))
	input := []byte("{ \"messages\" : [ { \"content\" : \"internal\" } ] }")
	res, err := engine.Inspect(input)
	if err != nil {
		t.Fatal(err)
	}
	if res.Action != "audit" || string(res.Body) != string(input) {
		t.Fatalf("result %+v", res)
	}
}
