package override_test

import (
	"testing"

	"github.com/hollis-labs/nanite/internal/agent/override"
)

// Test 1: Scalar last-writer-wins — session overrides base
func TestResolve_ScalarLastWriterWins(t *testing.T) {
	base := override.OverrideConfig{
		Model:    "gpt-4",
		Provider: "openai",
	}
	session := &override.OverrideConfig{
		Model: "claude-3-5-sonnet",
	}

	result := override.Resolve(base, nil, session)

	if result.Model != "claude-3-5-sonnet" {
		t.Errorf("expected Model %q, got %q", "claude-3-5-sonnet", result.Model)
	}
	if result.Provider != "openai" {
		t.Errorf("expected Provider %q to be preserved, got %q", "openai", result.Provider)
	}
}

// Test 2: Scalar zero-value skipped — empty string doesn't override
func TestResolve_ScalarZeroValueSkipped(t *testing.T) {
	base := override.OverrideConfig{
		Model:    "gpt-4",
		Provider: "openai",
	}
	session := &override.OverrideConfig{
		Model: "", // zero value — should not override
	}

	result := override.Resolve(base, nil, session)

	if result.Model != "gpt-4" {
		t.Errorf("expected Model %q to be preserved, got %q", "gpt-4", result.Model)
	}
}

// Test 3: List union with "+" prefix
func TestResolve_ListUnionWithPlusPrefix(t *testing.T) {
	base := override.OverrideConfig{
		Tools: []string{"bash", "read"},
	}
	project := &override.OverrideConfig{
		Tools: []string{"+write", "grep"},
	}

	result := override.Resolve(base, project, nil)

	want := map[string]bool{"bash": true, "read": true, "write": true, "grep": true}
	if len(result.Tools) != len(want) {
		t.Errorf("expected %d tools, got %d: %v", len(want), len(result.Tools), result.Tools)
	}
	for _, tool := range result.Tools {
		if !want[tool] {
			t.Errorf("unexpected tool %q in result", tool)
		}
	}
}

// Test 4: List removal with "-" prefix
func TestResolve_ListRemovalWithMinusPrefix(t *testing.T) {
	base := override.OverrideConfig{
		Tools: []string{"bash", "read", "write"},
	}
	project := &override.OverrideConfig{
		Tools: []string{"-write"},
	}

	result := override.Resolve(base, project, nil)

	for _, tool := range result.Tools {
		if tool == "write" {
			t.Errorf("expected %q to be removed, but it's still present", "write")
		}
	}
	want := map[string]bool{"bash": true, "read": true}
	if len(result.Tools) != len(want) {
		t.Errorf("expected %d tools, got %d: %v", len(want), len(result.Tools), result.Tools)
	}
}

// Test 5: Map deep merge — new keys added, existing keys replaced, missing keys preserved
func TestResolve_MapDeepMerge(t *testing.T) {
	base := override.OverrideConfig{
		Settings: map[string]any{
			"timeout": 30,
			"retries": 3,
		},
	}
	project := &override.OverrideConfig{
		Settings: map[string]any{
			"timeout": 60,    // replace
			"verbose": true,  // add new
		},
	}

	result := override.Resolve(base, project, nil)

	if result.Settings["timeout"] != 60 {
		t.Errorf("expected timeout=60, got %v", result.Settings["timeout"])
	}
	if result.Settings["retries"] != 3 {
		t.Errorf("expected retries=3 (preserved), got %v", result.Settings["retries"])
	}
	if result.Settings["verbose"] != true {
		t.Errorf("expected verbose=true (new key), got %v", result.Settings["verbose"])
	}
}

// Test 6: Wildcard "*" then agent-specific in ResolveWithMap
func TestResolveWithMap_WildcardThenAgentSpecific(t *testing.T) {
	base := override.OverrideConfig{
		Model: "gpt-4",
		Tools: []string{"bash"},
	}
	overrides := map[string]override.OverrideConfig{
		"*": {
			Model: "claude-3-5-sonnet", // applied first
			Tools: []string{"+read"},
		},
		"myagent": {
			Model: "claude-3-7-sonnet", // applied second, wins
			Tools: []string{"+write"},
		},
	}

	result := override.ResolveWithMap(base, overrides, "myagent", nil)

	if result.Model != "claude-3-7-sonnet" {
		t.Errorf("expected Model %q from agent-specific override, got %q", "claude-3-7-sonnet", result.Model)
	}
	wantTools := map[string]bool{"bash": true, "read": true, "write": true}
	if len(result.Tools) != len(wantTools) {
		t.Errorf("expected %d tools, got %d: %v", len(wantTools), len(result.Tools), result.Tools)
	}
}

// Test 7: Nil layers skipped — base returned unchanged
func TestResolve_NilLayersSkipped(t *testing.T) {
	base := override.OverrideConfig{
		Model:    "gpt-4",
		Provider: "openai",
		Tools:    []string{"bash", "read"},
	}

	result := override.Resolve(base, nil, nil)

	if result.Model != base.Model {
		t.Errorf("expected Model %q, got %q", base.Model, result.Model)
	}
	if result.Provider != base.Provider {
		t.Errorf("expected Provider %q, got %q", base.Provider, result.Provider)
	}
	if len(result.Tools) != len(base.Tools) {
		t.Errorf("expected %d tools, got %d", len(base.Tools), len(result.Tools))
	}
}
