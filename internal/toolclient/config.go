package toolclient

import (
	"log/slog"

	"github.com/hollis-labs/go-toolbroker/broker"
)

// DefaultToolTokenBudgetPct is the default fraction of the context window reserved for tool definitions.
const DefaultToolTokenBudgetPct = 0.20

// DefaultContextWindowTokens is the default context window size in tokens.
const DefaultContextWindowTokens = 200000

// Config holds tool broker configuration.
type Config struct {
	// Rules are the base rules (from tool-broker lib defaults or custom YAML).
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

	// ErrTowardMorePad is the number of zero-score (neutral) tool candidates
	// the reasoning-augmented selection appends to the strict-relevance set
	// when the token budget admits them. Per CW-20260426-0010 #3, the broker
	// errs toward "a little more" rather than "a little less" — under-loading
	// costs an LLM round-trip through request_tools, which is much more
	// expensive than the ~14 tokens an extra description costs.
	// Default: 3. Set to 0 to disable the bias.
	ErrTowardMorePad int

	// SkillsDir is the absolute path to the operator-authored
	// `*.tools.preferences.md` directory. When empty, the default
	// `$HOME/.nanite/skills` is used at construction time. Set to a sentinel
	// "off" value to explicitly disable skill loading.
	SkillsDir string
}

// DefaultConfig returns a Config using the default rules from tool-broker lib.
func DefaultConfig() *Config {
	return &Config{
		Rules:               broker.DefaultRules(),
		WorkspaceOverrides:  make(map[string][]broker.Rule),
		AgentOverrides:      make(map[string][]broker.Rule),
		ToolTokenBudgetPct:  DefaultToolTokenBudgetPct,
		ContextWindowTokens: DefaultContextWindowTokens,
		ErrTowardMorePad:    DefaultErrTowardMorePad,
	}
}

// LoadConfig loads a Config from a YAML file path, falling back to defaults on error.
func LoadConfig(path string) *Config {
	if path == "" {
		return DefaultConfig()
	}

	rules, err := broker.LoadRulesFromFile(path)
	if err != nil {
		slog.Warn("toolclient: failed to load rules (using defaults)", "path", path, "err", err)
		return DefaultConfig()
	}

	return &Config{
		Rules:               rules,
		WorkspaceOverrides:  make(map[string][]broker.Rule),
		AgentOverrides:      make(map[string][]broker.Rule),
		ToolTokenBudgetPct:  DefaultToolTokenBudgetPct,
		ContextWindowTokens: DefaultContextWindowTokens,
		ErrTowardMorePad:    DefaultErrTowardMorePad,
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
