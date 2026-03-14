package toolbroker

import (
	"log"

	"github.com/hollis-labs/tool-broker/broker"
)

// DefaultToolTokenBudgetPct is the default fraction of the context window reserved for tool definitions.
const DefaultToolTokenBudgetPct = 0.20

// DefaultContextWindowTokens is the default context window size in tokens.
const DefaultContextWindowTokens = 200000

// Config holds tool broker configuration.
type Config struct {
	// Rules are the base rules (from tiamat-tool-broker defaults or custom YAML).
	Rules []broker.Rule

	// WorkspaceOverrides maps workspace ID to additional rules.
	WorkspaceOverrides map[string][]broker.Rule

	// AgentOverrides maps agent ID to additional rules.
	AgentOverrides map[string][]broker.Rule

	// ToolTokenBudgetPct is the fraction of the context window budget allocated to tool definitions.
	// Default: 0.20 (20%).
	ToolTokenBudgetPct float64

	// ContextWindowTokens is the total context window size in tokens.
	// Default: 200000.
	ContextWindowTokens int
}

// DefaultConfig returns a Config using the default rules from tiamat-tool-broker.
func DefaultConfig() *Config {
	return &Config{
		Rules:               broker.DefaultRules(),
		WorkspaceOverrides:  make(map[string][]broker.Rule),
		AgentOverrides:      make(map[string][]broker.Rule),
		ToolTokenBudgetPct:  DefaultToolTokenBudgetPct,
		ContextWindowTokens: DefaultContextWindowTokens,
	}
}

// LoadConfig loads a Config from a YAML file path, falling back to defaults on error.
func LoadConfig(path string) *Config {
	if path == "" {
		return DefaultConfig()
	}

	rules, err := broker.LoadRulesFromFile(path)
	if err != nil {
		log.Printf("toolbroker: failed to load rules from %s: %v (using defaults)", path, err)
		return DefaultConfig()
	}

	return &Config{
		Rules:               rules,
		WorkspaceOverrides:  make(map[string][]broker.Rule),
		AgentOverrides:      make(map[string][]broker.Rule),
		ToolTokenBudgetPct:  DefaultToolTokenBudgetPct,
		ContextWindowTokens: DefaultContextWindowTokens,
	}
}

// RulesFor returns the merged rule set for a given workspace and agent.
// Base rules are always included; overrides are appended.
func (c *Config) RulesFor(workspaceID, agentID string) []broker.Rule {
	rules := make([]broker.Rule, len(c.Rules))
	copy(rules, c.Rules)

	if workspaceID != "" {
		if ws, ok := c.WorkspaceOverrides[workspaceID]; ok {
			rules = append(rules, ws...)
		}
	}

	if agentID != "" {
		if ag, ok := c.AgentOverrides[agentID]; ok {
			rules = append(rules, ag...)
		}
	}

	return rules
}
