package toolclient

// DefaultToolTokenBudgetPct is the default fraction of the context window reserved for tool definitions.
const DefaultToolTokenBudgetPct = 0.20

// DefaultContextWindowTokens is the default context window size in tokens.
const DefaultContextWindowTokens = 200000

// Config holds tool-selection configuration.
//
// Phase 0 item 22 (decision log §11, docs/engineering/architecture/03-steering.md):
// the go-toolbroker rule-matching layer this Config used to configure
// (Rules / WorkspaceOverrides / AgentOverrides / RulesFor, plus
// LoadConfig's YAML rule-file loading) is retired. NaniteDefaultRules — the
// ruleset this Config used to carry — was a single "*" catch-all rule that
// matched every intent and included every registered tool, so the rule
// engine was already a no-op in production; selectToolsUncapped now
// returns the full registered catalog directly. Real narrowing happens
// downstream of selection: tool_permissions (ToolClient.CheckPermission),
// the schema-v2 tools allowlist (filterToolsByAllowlist), the chat-role
// surface filter (applyChatSurfaceFilter), the developer_mode dev-tool
// gate, and progressive discovery — none of which lived in this Config.
type Config struct {
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

// DefaultConfig returns a Config with Nanite's default budget/skills knobs.
func DefaultConfig() *Config {
	return &Config{
		ToolTokenBudgetPct:  DefaultToolTokenBudgetPct,
		ContextWindowTokens: DefaultContextWindowTokens,
		ErrTowardMorePad:    DefaultErrTowardMorePad,
	}
}
