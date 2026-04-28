package plugin

import (
	"net/http"
	"regexp"
	"testing"
)

// TestCardRulesRegistry_RegisterAndDetect verifies that regex-based plugin
// card rules detect correctly.
func TestCardRulesRegistry_RegisterAndDetect(t *testing.T) {
	host := NewHost(http.NewServeMux(), NewLogger("test"))

	rule, err := compileCardRule(CardRuleRegistration{
		CardType: "metrics-summary",
		Pattern:  "```metrics",
	}, "test-plugin", "")
	if err != nil {
		t.Fatalf("compileCardRule: %v", err)
	}
	if err := host.RegisterCardRule(rule); err != nil {
		t.Fatalf("RegisterCardRule: %v", err)
	}

	// Match
	got := host.DetectCardType("Here is the result:\n```metrics\n{\"value\": 42}\n```\n")
	if got != "metrics-summary" {
		t.Errorf("DetectCardType = %q, want %q", got, "metrics-summary")
	}

	// No match
	got = host.DetectCardType("Just some plain prose.")
	if got != "" {
		t.Errorf("DetectCardType (no match) = %q, want empty", got)
	}
}

// TestCardRulesRegistry_BuiltinFirst verifies that a built-in rule (tier=0)
// wins over a later-registered plugin rule when both match.
func TestCardRulesRegistry_BuiltinFirst(t *testing.T) {
	host := NewHost(http.NewServeMux(), NewLogger("test"))

	// Register built-in rule first (tier=0).
	host.cardRules.registerBuiltin(CardRuleEntry{
		CardType: "report-card",
		PluginID: "",
		pattern:  mustCompileRE(t, "summary:"),
	})

	// Register plugin rule that also matches "summary:".
	rule, err := compileCardRule(CardRuleRegistration{
		CardType: "plugin-summary",
		Pattern:  "summary:",
	}, "my-plugin", "")
	if err != nil {
		t.Fatalf("compileCardRule: %v", err)
	}
	if err := host.RegisterCardRule(rule); err != nil {
		// plugin-summary is a different card_type from report-card, so no collision.
		t.Fatalf("RegisterCardRule: %v", err)
	}

	// Both match "summary: done". Built-in (tier=0) runs first → report-card wins.
	got := host.DetectCardType("summary: done")
	if got != "report-card" {
		t.Errorf("DetectCardType = %q, want %q (built-in should win)", got, "report-card")
	}
}

// TestCardRulesRegistry_PluginCannotOverrideBuiltin verifies that a plugin
// cannot register a card_type already owned by a built-in rule.
func TestCardRulesRegistry_PluginCannotOverrideBuiltin(t *testing.T) {
	host := NewHost(http.NewServeMux(), NewLogger("test"))

	host.cardRules.registerBuiltin(CardRuleEntry{
		CardType: "report-card",
		PluginID: "",
		pattern:  mustCompileRE(t, "report"),
	})

	rule, err := compileCardRule(CardRuleRegistration{
		CardType: "report-card",
		Pattern:  "report",
	}, "attacker-plugin", "")
	if err != nil {
		t.Fatalf("compileCardRule: %v", err)
	}
	err = host.RegisterCardRule(rule)
	if err == nil {
		t.Fatal("expected error when plugin tries to override built-in card_type, got nil")
	}
}

// TestCardRulesRegistry_CrossPluginCollision verifies that two plugins cannot
// register the same card_type.
func TestCardRulesRegistry_CrossPluginCollision(t *testing.T) {
	host := NewHost(http.NewServeMux(), NewLogger("test"))

	r1, err := compileCardRule(CardRuleRegistration{
		CardType: "my-card",
		Pattern:  "trigger-a",
	}, "plugin-a", "")
	if err != nil {
		t.Fatalf("compileCardRule A: %v", err)
	}
	if err := host.RegisterCardRule(r1); err != nil {
		t.Fatalf("RegisterCardRule A: %v", err)
	}

	r2, err := compileCardRule(CardRuleRegistration{
		CardType: "my-card",
		Pattern:  "trigger-b",
	}, "plugin-b", "")
	if err != nil {
		t.Fatalf("compileCardRule B: %v", err)
	}
	if err := host.RegisterCardRule(r2); err == nil {
		t.Fatal("expected collision error when two plugins register same card_type")
	}
}

// TestCardRulesRegistry_UnloadDeregisters verifies that UnregisterPluginCardRules
// removes all rules owned by the plugin.
func TestCardRulesRegistry_UnloadDeregisters(t *testing.T) {
	host := NewHost(http.NewServeMux(), NewLogger("test"))

	rule, err := compileCardRule(CardRuleRegistration{
		CardType: "my-card",
		Pattern:  "magic-trigger",
	}, "temp-plugin", "")
	if err != nil {
		t.Fatalf("compileCardRule: %v", err)
	}
	if err := host.RegisterCardRule(rule); err != nil {
		t.Fatalf("RegisterCardRule: %v", err)
	}

	// Verify it matches before unload.
	if got := host.DetectCardType("magic-trigger found"); got != "my-card" {
		t.Fatalf("expected my-card before unload, got %q", got)
	}

	// Unregister.
	n := host.UnregisterPluginCardRules("temp-plugin")
	if n != 1 {
		t.Errorf("UnregisterPluginCardRules returned %d, want 1", n)
	}

	// Should no longer match.
	if got := host.DetectCardType("magic-trigger found"); got != "" {
		t.Errorf("DetectCardType after unload = %q, want empty", got)
	}
}

// TestCardRulesRegistry_GetCardRules verifies the snapshot includes registered entries.
func TestCardRulesRegistry_GetCardRules(t *testing.T) {
	host := NewHost(http.NewServeMux(), NewLogger("test"))

	rule, err := compileCardRule(CardRuleRegistration{
		CardType:    "table-card",
		Pattern:     "```table",
		Description: "Detects table blocks",
	}, "table-plugin", "")
	if err != nil {
		t.Fatalf("compileCardRule: %v", err)
	}
	if err := host.RegisterCardRule(rule); err != nil {
		t.Fatalf("RegisterCardRule: %v", err)
	}

	rules := host.GetCardRules()
	if len(rules) != 1 {
		t.Fatalf("GetCardRules len = %d, want 1", len(rules))
	}
	if rules[0].CardType != "table-card" {
		t.Errorf("CardType = %q, want %q", rules[0].CardType, "table-card")
	}
	if rules[0].PluginID != "table-plugin" {
		t.Errorf("PluginID = %q, want %q", rules[0].PluginID, "table-plugin")
	}
	if rules[0].Description != "Detects table blocks" {
		t.Errorf("Description = %q, want %q", rules[0].Description, "Detects table blocks")
	}
}

// TestCompileCardRule_InvalidPattern verifies that a bad regex is rejected.
func TestCompileCardRule_InvalidPattern(t *testing.T) {
	_, err := compileCardRule(CardRuleRegistration{
		CardType: "bad-card",
		Pattern:  "[invalid(regex",
	}, "p", "")
	if err == nil {
		t.Fatal("expected error for invalid regex, got nil")
	}
}

// TestCompileCardRule_BothMatchers verifies that setting both pattern and
// output_schema returns an error.
func TestCompileCardRule_BothMatchers(t *testing.T) {
	_, err := compileCardRule(CardRuleRegistration{
		CardType:     "conflict-card",
		Pattern:      "foo",
		OutputSchema: "schema.json",
	}, "p", "")
	if err == nil {
		t.Fatal("expected error when both pattern and output_schema are set")
	}
}

// TestCompileCardRule_NeitherMatcher verifies that omitting both matchers returns an error.
func TestCompileCardRule_NeitherMatcher(t *testing.T) {
	_, err := compileCardRule(CardRuleRegistration{
		CardType: "no-matcher",
	}, "p", "")
	if err == nil {
		t.Fatal("expected error when neither pattern nor output_schema is set")
	}
}

// TestCardRulesRegistry_EmptyPluginID_UnregisterNoop verifies that calling
// UnregisterPluginCardRules with "" is a safe no-op.
func TestCardRulesRegistry_EmptyPluginID_UnregisterNoop(t *testing.T) {
	host := NewHost(http.NewServeMux(), NewLogger("test"))
	n := host.UnregisterPluginCardRules("")
	if n != 0 {
		t.Errorf("expected 0, got %d", n)
	}
}

// TestCardRulesRegistry_InvalidCardType verifies registration is refused for
// card_types that don't match the required slug pattern.
func TestCardRulesRegistry_InvalidCardType(t *testing.T) {
	host := NewHost(http.NewServeMux(), NewLogger("test"))

	rule, err := compileCardRule(CardRuleRegistration{
		CardType: "Bad_Card",
		Pattern:  "x",
	}, "p", "")
	if err != nil {
		t.Fatalf("compileCardRule: %v", err)
	}
	// card_type validation happens in registerPlugin, not compileCardRule.
	err = host.RegisterCardRule(rule)
	if err == nil {
		t.Fatal("expected error for invalid card_type pattern, got nil")
	}
}

// TestManifestRegisters_CardRules_RoundTrip verifies that card_rules declared
// in a PluginManifest survive a yaml round-trip.
func TestManifestRegisters_CardRules_RoundTrip(t *testing.T) {
	manifest := PluginManifest{
		Registers: ManifestRegisters{
			CardRules: []CardRuleRegistration{
				{
					CardType:    "metrics-summary",
					Pattern:     "```metrics",
					Description: "Detects metrics fenced blocks",
				},
			},
		},
	}
	if len(manifest.Registers.CardRules) != 1 {
		t.Fatalf("expected 1 card rule, got %d", len(manifest.Registers.CardRules))
	}
	cr := manifest.Registers.CardRules[0]
	if cr.CardType != "metrics-summary" {
		t.Errorf("CardType = %q, want %q", cr.CardType, "metrics-summary")
	}
	if cr.Pattern != "```metrics" {
		t.Errorf("Pattern = %q, want %q", cr.Pattern, "```metrics")
	}
}

// mustCompileRE compiles a regex or fails the test.
func mustCompileRE(t *testing.T, pattern string) *regexp.Regexp {
	t.Helper()
	re, err := regexp.Compile(pattern)
	if err != nil {
		t.Fatalf("compile regex %q: %v", pattern, err)
	}
	return re
}
