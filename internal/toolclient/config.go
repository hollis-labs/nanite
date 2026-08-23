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
// downstream of selection: the agent_tools grant filter
// (service/tool.go's filterToolsByAgentTools — replaced the old
// schema-v2 tools allowlist / filterToolsByAllowlist as of
// TASKS/phase-4/05-wire-select-for-agent-to-read-agent-tools.md, and the
// legacy tool_permissions/ToolClient.CheckPermission mechanism entirely as
// of TASKS/adhoc/02-remove-tool-permissions-collapse-to-agent-tools.md),
// the chat-role surface filter (applyChatSurfaceFilter), the
// developer_mode dev-tool gate, and progressive discovery — none of
// which lived in this Config.
type Config struct {
	// ToolTokenBudgetPct is the fraction of the context window budget allocated to tool definitions.
	// Default: 0.20 (20%).
	ToolTokenBudgetPct float64

	// ContextWindowTokens is the total context window size in tokens.
	// Default: 200000.
	ContextWindowTokens int
}

// DefaultConfig returns Nanite's default tool-selection configuration.
func DefaultConfig() *Config {
	return &Config{
		ToolTokenBudgetPct:  DefaultToolTokenBudgetPct,
		ContextWindowTokens: DefaultContextWindowTokens,
	}
}
