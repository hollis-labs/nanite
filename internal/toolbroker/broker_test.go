package toolbroker

import (
	"context"
	"testing"

	"github.com/hollis-labs/tiamat-tool-broker/broker"
)

func TestSelectTools_ReturnsTools(t *testing.T) {
	cfg := &Config{
		Rules:              broker.DefaultRules(),
		WorkspaceOverrides: make(map[string][]broker.Rule),
		AgentOverrides:     make(map[string][]broker.Rule),
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
