package api

import "github.com/hollis-labs/nanite/internal/store"

// UserSettingsView is the GET and PUT /api/settings response: every
// persisted user_settings field plus the computed embedding_status. It
// replaces a store.UserSettings → map[string]any round-trip, so its fields
// are in alphabetical key order, matching the sorted map keys byte for byte.
// ext_settings and tool_load_preferences drop out when empty; a nil
// provider_fallback_chain stays null.
//
// One difference from the map path: it carried integers through float64, so
// an int above 2^53 lost precision on the wire. The view keeps it exact.
type UserSettingsView struct {
	AutoRepairPref                 string            `json:"auto_repair_pref"`
	CompactionStrategy             string            `json:"compaction_strategy"`
	ContextBudgetPct               float64           `json:"context_budget_pct"`
	ContextOverflowRecovery        bool              `json:"context_overflow_recovery"`
	ContextWindowTokens            int               `json:"context_window_tokens"`
	DefaultAgent                   string            `json:"default_agent"`
	DefaultModel                   string            `json:"default_model"`
	DefaultProvider                string            `json:"default_provider"`
	DeveloperMode                  bool              `json:"developer_mode"`
	EmbeddingMode                  string            `json:"embedding_mode"`
	EmbeddingModel                 string            `json:"embedding_model"`
	EmbeddingProvider              string            `json:"embedding_provider"`
	EmbeddingStatus                string            `json:"embedding_status"`
	ExtSettings                    map[string]any    `json:"ext_settings,omitempty"`
	ProviderFallbackChain          []string          `json:"provider_fallback_chain"`
	RecoverMode                    bool              `json:"recover_mode"`
	SubagentApprovalRequired       bool              `json:"subagent_approval_required"`
	SubagentApprovalTimeoutSeconds int               `json:"subagent_approval_timeout_seconds"`
	SubagentRuntime                string            `json:"subagent_runtime"`
	SummarizerModel                string            `json:"summarizer_model"`
	SummarizerProvider             string            `json:"summarizer_provider"`
	TaskBackend                    string            `json:"task_backend"`
	ToolCacheEnabled               bool              `json:"tool_cache_enabled"`
	ToolCallDisplayMode            string            `json:"tool_call_display_mode"`
	ToolClassifierMode             string            `json:"tool_classifier_mode"`
	ToolClassifierModel            string            `json:"tool_classifier_model"`
	ToolClassifierProvider         string            `json:"tool_classifier_provider"`
	ToolClassifierTimeoutMS        int               `json:"tool_classifier_timeout_ms"`
	ToolDrawerRetention            int               `json:"tool_drawer_retention"`
	ToolLoadPreferences            map[string]string `json:"tool_load_preferences,omitempty"`
	ToolPerTurnCap                 int               `json:"tool_per_turn_cap"`
	ToolResultCacheTTLSeconds      int               `json:"tool_result_cache_ttl_seconds"`
	ToolResultHardCapBytes         int               `json:"tool_result_hard_cap_bytes"`
	ToolResultSoftTruncBytes       int               `json:"tool_result_soft_truncate_bytes"`
	ToolStreamBehavior             string            `json:"tool_stream_behavior"`
	UtilityModel                   string            `json:"utility_model"`
	UtilityProvider                string            `json:"utility_provider"`
}

func userSettingsToView(us *store.UserSettings, embeddingStatus string) UserSettingsView {
	return UserSettingsView{
		AutoRepairPref:                 us.AutoRepairPref,
		CompactionStrategy:             us.CompactionStrategy,
		ContextBudgetPct:               us.ContextBudgetPct,
		ContextOverflowRecovery:        us.ContextOverflowRecovery,
		ContextWindowTokens:            us.ContextWindowTokens,
		DefaultAgent:                   us.DefaultAgent,
		DefaultModel:                   us.DefaultModel,
		DefaultProvider:                us.DefaultProvider,
		DeveloperMode:                  us.DeveloperMode,
		EmbeddingMode:                  us.EmbeddingMode,
		EmbeddingModel:                 us.EmbeddingModel,
		EmbeddingProvider:              us.EmbeddingProvider,
		EmbeddingStatus:                embeddingStatus,
		ExtSettings:                    us.ExtSettings,
		ProviderFallbackChain:          us.ProviderFallbackChain,
		RecoverMode:                    us.RecoverMode,
		SubagentApprovalRequired:       us.SubagentApprovalRequired,
		SubagentApprovalTimeoutSeconds: us.SubagentApprovalTimeoutSeconds,
		SubagentRuntime:                us.SubagentRuntime,
		SummarizerModel:                us.SummarizerModel,
		SummarizerProvider:             us.SummarizerProvider,
		TaskBackend:                    us.TaskBackend,
		ToolCacheEnabled:               us.ToolCacheEnabled,
		ToolCallDisplayMode:            us.ToolCallDisplayMode,
		ToolClassifierMode:             us.ToolClassifierMode,
		ToolClassifierModel:            us.ToolClassifierModel,
		ToolClassifierProvider:         us.ToolClassifierProvider,
		ToolClassifierTimeoutMS:        us.ToolClassifierTimeoutMS,
		ToolDrawerRetention:            us.ToolDrawerRetention,
		ToolLoadPreferences:            us.ToolLoadPreferences,
		ToolPerTurnCap:                 us.ToolPerTurnCap,
		ToolResultCacheTTLSeconds:      us.ToolResultCacheTTLSeconds,
		ToolResultHardCapBytes:         us.ToolResultHardCapBytes,
		ToolResultSoftTruncBytes:       us.ToolResultSoftTruncBytes,
		ToolStreamBehavior:             us.ToolStreamBehavior,
		UtilityModel:                   us.UtilityModel,
		UtilityProvider:                us.UtilityProvider,
	}
}
