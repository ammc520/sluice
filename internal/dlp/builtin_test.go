package dlp

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/company/sluice/internal/store"
)

func TestBuiltinRulesCompileAndAreUnique(t *testing.T) {
	rules := BuiltinRules()
	if len(rules) != 7 {
		t.Fatalf("rules %d", len(rules))
	}
	names := map[string]bool{}
	for _, rule := range rules {
		if !rule.Builtin || !rule.Enabled || rule.Replacement == "" {
			t.Fatalf("rule %+v", rule)
		}
		if rule.Priority <= 0 || names[rule.Name] {
			t.Fatalf("priority/name %+v", rule)
		}
		names[rule.Name] = true
	}
	if _, errs := NewEngine(rules); len(errs) != 0 {
		t.Fatalf("compile errors %v", errs)
	}
}

func TestBuiltinsDetectCommonSecrets(t *testing.T) {
	engine, errs := NewEngine(BuiltinRules())
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	cases := []struct {
		text   string
		action string
	}{
		{"AKIAABCDEFGHIJKLMNOP", "block"},
		{"LTAIabcdefghijklmn", "block"},
		{"AKIDabcdefghijklm", "block"},
		{"-----BEGIN RSA PRIVATE KEY-----", "block"},
		{"sk-abcdefghijklmnopqrstuvwxyz", "block"},
		{"Bearer abcdefghijklmnopqrstuvwxyz", "block"},
		{"13800138000", "mask"},
		{"11010519491231002X", "mask"},
		{"4111111111111111", "mask"},
		{"10.1.2.3", "audit"},
		{"alice@example.com", "audit"},
	}
	for _, tc := range cases {
		res, err := engine.Test(tc.text)
		if err != nil {
			t.Fatalf("%s: %v", tc.text, err)
		}
		if res.Action != tc.action {
			t.Errorf("%s: action %q want %q hits=%+v", tc.text, res.Action, tc.action, res.Hits)
		}
	}
}

type fakeRuleStore struct {
	items   []store.DLPRule
	creates int
}

func (f *fakeRuleStore) Create(_ context.Context, rule *store.DLPRule) error {
	for _, existing := range f.items {
		if existing.Name == rule.Name {
			return store.ErrConflict
		}
	}
	f.creates++
	rule.ID = uuid.New()
	f.items = append(f.items, *rule)
	return nil
}

func (f *fakeRuleStore) Get(context.Context, uuid.UUID) (*store.DLPRule, error) {
	return nil, store.ErrNotFound
}

func (f *fakeRuleStore) List(context.Context) ([]store.DLPRule, error) {
	return append([]store.DLPRule(nil), f.items...), nil
}

func (f *fakeRuleStore) ListEnabled(context.Context) ([]store.DLPRule, error) {
	return append([]store.DLPRule(nil), f.items...), nil
}

func (f *fakeRuleStore) Update(context.Context, *store.DLPRule) error { return nil }
func (f *fakeRuleStore) Delete(context.Context, uuid.UUID) error      { return nil }

func TestEnsureBuiltinRulesIsIdempotentAndPreservesTuning(t *testing.T) {
	tuned := store.DLPRule{
		Name: BuiltinPhoneName, Category: "pii", MatchType: "regex",
		Pattern: `1[3-9][0-9]{9}`, Action: "audit", Replacement: "***",
		Priority: 20, Enabled: true, Builtin: true,
	}
	storeFake := &fakeRuleStore{items: []store.DLPRule{tuned}}
	if err := EnsureBuiltinRules(context.Background(), storeFake); err != nil {
		t.Fatal(err)
	}
	if len(storeFake.items) != len(BuiltinRules()) {
		t.Fatalf("items %d", len(storeFake.items))
	}
	if storeFake.items[0].Action != "audit" {
		t.Fatalf("tuned rule overwritten: %+v", storeFake.items[0])
	}

	creates := storeFake.creates
	if err := EnsureBuiltinRules(context.Background(), storeFake); err != nil {
		t.Fatal(err)
	}
	if storeFake.creates != creates || len(storeFake.items) != len(BuiltinRules()) {
		t.Fatalf("not idempotent: creates=%d items=%d", storeFake.creates, len(storeFake.items))
	}
}
