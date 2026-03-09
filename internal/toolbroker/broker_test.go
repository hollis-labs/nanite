package toolbroker

import (
	"context"
	"testing"

	"github.com/hollis-labs/tiamat-tool-broker/broker"
)

func TestSelectTools_ReturnsTools(t *testing.T) {
	cfg := &Config{
		Rules:               broker.DefaultRules(),
		WorkspaceOverrides:  make(map[string][]broker.Rule),
		AgentOverrides:      make(map[string][]broker.Rule),
		ToolTokenBudgetPct:  DefaultToolTokenBudgetPct,
		ContextWindowTokens: DefaultContextWindowTokens,
	}
	tb := New(nil, nil, cfg)

	// Register some tools.
	tools := []broker.ToolDefinition{
		{Name: "volon_task_create", Server: "volon", Description: "Create a task"},
		{Name: "volon_task_list", Server: "volon", Description: "List tasks"},
		{Name: "cortex_context_view", Server: "cortex", Description: "View context"},
	}
	tb.RegisterTools(tools)

	// Select with wildcard intent.
	selected, err := tb.SelectTools(context.Background(), "*", nil, "", "")
	if err != nil {
		t.Fatalf("SelectTools error: %v", err)
	}
	if len(selected) == 0 {
		t.Error("expected at least one tool selected")
	}
}

func TestSelectTools_CapsAtMax(t *testing.T) {
	cfg := DefaultConfig()
	tb := New(nil, nil, cfg)

	// Register more than MaxSelectedTools tools.
	tools := make([]broker.ToolDefinition, MaxSelectedTools+5)
	for i := range tools {
		tools[i] = broker.ToolDefinition{
			Name:        "tool_" + string(rune('a'+i)),
			Server:      "test",
			Description: "Test tool",
		}
	}
	tb.RegisterTools(tools)

	selected, err := tb.SelectTools(context.Background(), "*", nil, "", "")
	if err != nil {
		t.Fatalf("SelectTools error: %v", err)
	}
	if len(selected) > MaxSelectedTools {
		t.Errorf("expected at most %d tools, got %d", MaxSelectedTools, len(selected))
	}
}

func TestDefaultConfig_HasRules(t *testing.T) {
	cfg := DefaultConfig()
	if len(cfg.Rules) == 0 {
		t.Error("expected default config to have rules")
	}
}

func TestEstimateToolTokens(t *testing.T) {
	tools := []broker.ToolDefinition{
		{Name: "tool_a", Server: "test", Description: "A short description"},
		{Name: "tool_b", Server: "test", Description: "Another description for testing"},
	}

	tokens := EstimateToolTokens(tools)
	if tokens <= 0 {
		t.Errorf("expected positive token estimate, got %d", tokens)
	}

	// Tokens should increase with more tools.
	moreTools := append(tools, broker.ToolDefinition{
		Name: "tool_c", Server: "test", Description: "Yet another tool with a longer description for estimation",
	})
	moreTokens := EstimateToolTokens(moreTools)
	if moreTokens <= tokens {
		t.Errorf("expected more tokens with more tools: %d <= %d", moreTokens, tokens)
	}
}

func TestPruneToolsToTokenBudget_UnderBudget(t *testing.T) {
	tools := []broker.ToolDefinition{
		{Name: "tool_a", Server: "test", Description: "Short"},
		{Name: "tool_b", Server: "test", Description: "Short"},
	}

	// Give a very large budget — all tools should pass through.
	result := PruneToolsToTokenBudget(tools, 100000)
	if len(result) != len(tools) {
		t.Errorf("expected %d tools unchanged, got %d", len(tools), len(result))
	}
}

func TestPruneToolsToTokenBudget_OverBudget(t *testing.T) {
	tools := []broker.ToolDefinition{
		{Name: "tool_a", Server: "test", Description: "First tool"},
		{Name: "tool_b", Server: "test", Description: "Second tool"},
		{Name: "tool_c", Server: "test", Description: "Third tool"},
		{Name: "tool_d", Server: "test", Description: "Fourth tool"},
		{Name: "tool_e", Server: "test", Description: "Fifth tool"},
	}

	// Set budget to only fit ~1 tool.
	result := PruneToolsToTokenBudget(tools, 20)
	if len(result) >= len(tools) {
		t.Errorf("expected fewer tools after pruning, got %d (original %d)", len(result), len(tools))
	}
	if len(result) == 0 {
		t.Error("expected at least 1 tool to remain")
	}
}

func TestPruneToolsToTokenBudget_KeepsAtLeastOne(t *testing.T) {
	tools := []broker.ToolDefinition{
		{Name: "big_tool", Server: "test", Description: "A very long description that should exceed any tiny budget we set for testing purposes to ensure at least one tool is always kept"},
	}

	// Budget of 1 token — still must keep at least 1 tool.
	result := PruneToolsToTokenBudget(tools, 1)
	if len(result) != 1 {
		t.Errorf("expected exactly 1 tool retained, got %d", len(result))
	}
}

func TestPruneToolsToTokenBudget_EmptySlice(t *testing.T) {
	result := PruneToolsToTokenBudget(nil, 100)
	if len(result) != 0 {
		t.Errorf("expected 0 tools for nil input, got %d", len(result))
	}
}

func TestDefaultConfig_HasTokenBudget(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.ToolTokenBudgetPct != DefaultToolTokenBudgetPct {
		t.Errorf("expected ToolTokenBudgetPct=%f, got %f", DefaultToolTokenBudgetPct, cfg.ToolTokenBudgetPct)
	}
	if cfg.ContextWindowTokens != DefaultContextWindowTokens {
		t.Errorf("expected ContextWindowTokens=%d, got %d", DefaultContextWindowTokens, cfg.ContextWindowTokens)
	}
}

func TestListToolSummaries_ReturnsNameAndDescription(t *testing.T) {
	cfg := DefaultConfig()
	tb := New(nil, nil, cfg)

	// ListToolSummaries with no MCP manager returns empty.
	summaries := tb.ListToolSummaries()
	if len(summaries) != 0 {
		t.Errorf("expected 0 summaries without MCP manager, got %d", len(summaries))
	}
}

func TestToolSummary_HasCorrectFields(t *testing.T) {
	s := ToolSummary{
		Name:        "test_tool",
		Description: "A test tool",
		Server:      "test-server",
	}
	if s.Name != "test_tool" {
		t.Errorf("expected Name=test_tool, got %s", s.Name)
	}
	if s.Description != "A test tool" {
		t.Errorf("expected Description='A test tool', got %s", s.Description)
	}
	if s.Server != "test-server" {
		t.Errorf("expected Server=test-server, got %s", s.Server)
	}
}

func TestGetToolsByNames_NoMCPManager(t *testing.T) {
	cfg := DefaultConfig()
	tb := New(nil, nil, cfg)

	result := tb.GetToolsByNames([]string{"tool_a", "tool_b"})
	if len(result) != 0 {
		t.Errorf("expected 0 tools without MCP manager, got %d", len(result))
	}
}

func TestConfig_RulesFor_MergesOverrides(t *testing.T) {
	cfg := DefaultConfig()
	baseCount := len(cfg.Rules)

	cfg.WorkspaceOverrides["ws-1"] = []broker.Rule{
		{Name: "ws-rule", Intent: "*", Action: broker.Action{Type: "include"}},
	}
	cfg.AgentOverrides["agent-1"] = []broker.Rule{
		{Name: "agent-rule", Intent: "*", Action: broker.Action{Type: "exclude"}},
	}

	rules := cfg.RulesFor("ws-1", "agent-1")
	if len(rules) != baseCount+2 {
		t.Errorf("expected %d rules, got %d", baseCount+2, len(rules))
	}

	// Without overrides, should be base count.
	rules2 := cfg.RulesFor("", "")
	if len(rules2) != baseCount {
		t.Errorf("expected %d rules without overrides, got %d", baseCount, len(rules2))
	}
}
