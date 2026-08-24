package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// UserSettings holds global user-level configuration.
type UserSettings struct {
	ProviderFallbackChain []string          `json:"provider_fallback_chain"`
	DefaultProvider       string            `json:"default_provider"`
	DefaultModel          string            `json:"default_model"`
	DefaultAgent          string            `json:"default_agent"`
	UtilityProvider       string            `json:"utility_provider"`
	UtilityModel          string            `json:"utility_model"`
	ToolCallDisplayMode   string            `json:"tool_call_display_mode"`
	ToolStreamBehavior    string            `json:"tool_stream_behavior"`
	ToolDrawerRetention   int               `json:"tool_drawer_retention"`
	DeveloperMode         bool              `json:"developer_mode"`
	RecoverMode           bool              `json:"recover_mode"`
	ExtSettings           map[string]any    `json:"ext_settings,omitempty"`
	ToolLoadPreferences   map[string]string `json:"tool_load_preferences,omitempty"`
	TaskBackend           string            `json:"task_backend"`
	// AllowUnsignedPlugins permits installing plugin archives that lack a
	// verified Ed25519 signature. The setting is ONLY honored in builds
	// compiled with the `devmode` build tag (see internal/plugin/devmode).
	// In production binaries this field is intentionally inert: a
	// compromised row cannot disable signature verification.
	AllowUnsignedPlugins bool `json:"allow_unsigned_plugins"`
	// Embedding provider configuration. EmbeddingMode is one of
	// "disabled" (default) or "explicit". When disabled or when provider is
	// empty, no embedder is wired and similarity recall is unavailable.
	EmbeddingProvider string `json:"embedding_provider"`
	EmbeddingModel    string `json:"embedding_model"`
	EmbeddingMode     string `json:"embedding_mode"`
	// Context window + compaction settings (Phase 3 S3a).
	// ContextWindowTokens is the assumed provider context window (default
	// 200_000). ContextBudgetPct is the fraction used as the slot budget
	// (default 0.80). SummarizerProvider/Model select the model that runs
	// compaction Stage 2; empty means use the active chat provider/model.
	// CompactionStrategy is a forward hook ("default" or "broker"); only
	// "default" has an implementation today.
	ContextWindowTokens int     `json:"context_window_tokens"`
	ContextBudgetPct    float64 `json:"context_budget_pct"`
	SummarizerProvider  string  `json:"summarizer_provider"`
	SummarizerModel     string  `json:"summarizer_model"`
	CompactionStrategy  string  `json:"compaction_strategy"`
	// Tool broker execution-path settings (Phase 3 S4a).
	// ToolPerTurnCap is the max calls to any single tool per turn
	// (default 150; 0 = no cap). CW-20260519-0115 raised this from 10
	// after session c267 was blocked at 10 of 13 operator-requested
	// torque_task_create calls — count is a poor runaway signal;
	// pattern detectors (consecutive_fail_cap, runaway_fail_cap,
	// detectStuckLoop same-result-repeated, idle_timeout) do the
	// actual catching. This count is now a high backstop only.
	ToolPerTurnCap            int `json:"tool_per_turn_cap"`
	ToolResultCacheTTLSeconds int `json:"tool_result_cache_ttl_seconds"`
	ToolResultSoftTruncBytes  int `json:"tool_result_soft_truncate_bytes"`
	ToolResultHardCapBytes    int `json:"tool_result_hard_cap_bytes"`
	// Tool-slot cache-and-pointer settings (Phase 3 S3b).
	// ToolCacheEnabled flips the Tools slot between pointer-by-default
	// (enabled) and always-hydrate (disabled, S3a behavior).
	// ToolClassifierMode is one of "rules", "llm", or "broker" (composes
	// rules + LLM fallback + explicit signals, see plan §D3). Empty
	// ToolClassifierProvider / ToolClassifierModel fall back to the
	// summarizer provider/model (see S3a D1).
	// ToolClassifierTimeoutMS bounds the LLM-layer call; exceeding triggers
	// D9 fail-open (hydrate all for this turn).
	// ContextOverflowRecovery (folded from BLG-20260410-003, T9) enables
	// synchronous compaction+retry when a provider returns a
	// context-overflow error mid-generation.
	ToolCacheEnabled        bool   `json:"tool_cache_enabled"`
	ToolClassifierMode      string `json:"tool_classifier_mode"`
	ToolClassifierProvider  string `json:"tool_classifier_provider"`
	ToolClassifierModel     string `json:"tool_classifier_model"`
	ToolClassifierTimeoutMS int    `json:"tool_classifier_timeout_ms"`
	ContextOverflowRecovery bool   `json:"context_overflow_recovery"`
	// Subagent approval gate (G-4).
	// SubagentApprovalRequired gates every Spawn() behind a user approval envelope.
	// Default true — dogfood-safe.
	SubagentApprovalRequired bool `json:"subagent_approval_required"`
	// SubagentApprovalTimeoutSeconds is the wall-time after which a pending
	// approval is lazily auto-rejected on the next admin read. Default 86400 (24h).
	SubagentApprovalTimeoutSeconds int `json:"subagent_approval_timeout_seconds"`
	// AutoRepairPref governs the C2 LLM-augmented repair pipeline
	// (CW-20260429-0008). Allowed values:
	//   ""       — unset, treated as "always" (default policy).
	//   "always" — auto-repair recoverable tool errors when wired.
	//   "never"  — bypass the repair pipeline entirely.
	// The env var NANITE_AUTO_REPAIR is an operator-level kill switch
	// and takes precedence over this column when both disagree.
	AutoRepairPref string `json:"auto_repair_pref"`
}

// GetUserSettings returns the singleton user settings row.
func (s *Store) GetUserSettings(ctx context.Context) (*UserSettings, error) {
	var chainJSON, provider, model, agent string
	var utilProvider, utilModel, toolMode, settingsJSON string
	var toolStreamBehavior string
	var toolDrawerRetention int
	var devMode, recoverMode bool
	var toolLoadPrefsJSON string
	var taskBackend string
	var allowUnsigned bool
	var embeddingProvider, embeddingModel, embeddingMode string
	var contextWindowTokens int
	var contextBudgetPct float64
	var summarizerProvider, summarizerModel, compactionStrategy string
	var toolPerTurnCap, toolResultCacheTTL, toolResultSoftTrunc, toolResultHardCap int
	var toolCacheEnabled bool
	var toolClassifierMode, toolClassifierProvider, toolClassifierModel string
	var toolClassifierTimeoutMS int
	var contextOverflowRecovery bool
	var subagentApprovalRequired bool
	var subagentApprovalTimeoutSeconds int
	var autoRepairPref string
	err := s.DB.QueryRowContext(ctx,
		`SELECT provider_fallback_chain, default_provider, default_model,
		        default_agent, utility_provider, utility_model, tool_call_display_mode, settings,
		        developer_mode, recover_mode, tool_stream_behavior, tool_drawer_retention,
		        tool_load_preferences, task_backend, allow_unsigned_plugins,
		        embedding_provider, embedding_model, embedding_mode,
		        context_window_tokens, context_budget_pct,
		        summarizer_provider, summarizer_model, compaction_strategy,
		        tool_per_turn_cap, tool_result_cache_ttl_seconds,
		        tool_result_soft_truncate_bytes, tool_result_hard_cap_bytes,
		        tool_cache_enabled, tool_classifier_mode,
		        tool_classifier_provider, tool_classifier_model,
		        tool_classifier_timeout_ms, context_overflow_recovery,
		        subagent_approval_required, subagent_approval_timeout_seconds,
		        auto_repair_pref
		 FROM user_settings WHERE id = 1`,
	).Scan(&chainJSON, &provider, &model,
		&agent, &utilProvider, &utilModel, &toolMode, &settingsJSON,
		&devMode, &recoverMode, &toolStreamBehavior, &toolDrawerRetention,
		&toolLoadPrefsJSON, &taskBackend, &allowUnsigned,
		&embeddingProvider, &embeddingModel, &embeddingMode,
		&contextWindowTokens, &contextBudgetPct,
		&summarizerProvider, &summarizerModel, &compactionStrategy,
		&toolPerTurnCap, &toolResultCacheTTL, &toolResultSoftTrunc, &toolResultHardCap,
		&toolCacheEnabled, &toolClassifierMode,
		&toolClassifierProvider, &toolClassifierModel,
		&toolClassifierTimeoutMS, &contextOverflowRecovery,
		&subagentApprovalRequired, &subagentApprovalTimeoutSeconds,
		&autoRepairPref)
	if err != nil {
		return nil, fmt.Errorf("get user settings: %w", err)
	}

	us := &UserSettings{
		DefaultProvider:                provider,
		DefaultModel:                   model,
		DefaultAgent:                   agent,
		UtilityProvider:                utilProvider,
		UtilityModel:                   utilModel,
		ToolCallDisplayMode:            toolMode,
		ToolStreamBehavior:             toolStreamBehavior,
		ToolDrawerRetention:            toolDrawerRetention,
		DeveloperMode:                  devMode,
		RecoverMode:                    recoverMode,
		TaskBackend:                    taskBackend,
		AllowUnsignedPlugins:           allowUnsigned,
		EmbeddingProvider:              embeddingProvider,
		EmbeddingModel:                 embeddingModel,
		EmbeddingMode:                  embeddingMode,
		ContextWindowTokens:            contextWindowTokens,
		ContextBudgetPct:               contextBudgetPct,
		SummarizerProvider:             summarizerProvider,
		SummarizerModel:                summarizerModel,
		CompactionStrategy:             compactionStrategy,
		ToolPerTurnCap:                 toolPerTurnCap,
		ToolResultCacheTTLSeconds:      toolResultCacheTTL,
		ToolResultSoftTruncBytes:       toolResultSoftTrunc,
		ToolResultHardCapBytes:         toolResultHardCap,
		ToolCacheEnabled:               toolCacheEnabled,
		ToolClassifierMode:             toolClassifierMode,
		ToolClassifierProvider:         toolClassifierProvider,
		ToolClassifierModel:            toolClassifierModel,
		ToolClassifierTimeoutMS:        toolClassifierTimeoutMS,
		ContextOverflowRecovery:        contextOverflowRecovery,
		SubagentApprovalRequired:       subagentApprovalRequired,
		SubagentApprovalTimeoutSeconds: subagentApprovalTimeoutSeconds,
		AutoRepairPref:                 autoRepairPref,
	}
	if chainJSON != "" && chainJSON != "[]" {
		if err := json.Unmarshal([]byte(chainJSON), &us.ProviderFallbackChain); err != nil {
			return nil, fmt.Errorf("parse fallback chain: %w", err)
		}
	}
	if settingsJSON != "" && settingsJSON != "{}" {
		us.ExtSettings = make(map[string]any)
		if err := json.Unmarshal([]byte(settingsJSON), &us.ExtSettings); err != nil {
			return nil, fmt.Errorf("parse ext settings: %w", err)
		}
	}
	if toolLoadPrefsJSON != "" && toolLoadPrefsJSON != "{}" {
		us.ToolLoadPreferences = make(map[string]string)
		if err := json.Unmarshal([]byte(toolLoadPrefsJSON), &us.ToolLoadPreferences); err != nil {
			return nil, fmt.Errorf("parse tool load preferences: %w", err)
		}
	}
	return us, nil
}

// UpdateUserSettings updates the singleton user settings row.
func (s *Store) UpdateUserSettings(ctx context.Context, us *UserSettings) error {
	chain := us.ProviderFallbackChain
	if chain == nil {
		chain = []string{}
	}
	chainJSON, err := json.Marshal(chain)
	if err != nil {
		return fmt.Errorf("marshal fallback chain: %w", err)
	}
	extJSON := []byte("{}")
	if us.ExtSettings != nil {
		extJSON, err = json.Marshal(us.ExtSettings)
		if err != nil {
			return fmt.Errorf("marshal ext settings: %w", err)
		}
	}
	toolPrefsJSON := []byte("{}")
	if us.ToolLoadPreferences != nil {
		toolPrefsJSON, err = json.Marshal(us.ToolLoadPreferences)
		if err != nil {
			return fmt.Errorf("marshal tool load preferences: %w", err)
		}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	taskBackend := us.TaskBackend
	if taskBackend == "" {
		taskBackend = "local"
	}
	embeddingMode := us.EmbeddingMode
	if embeddingMode == "" {
		embeddingMode = "disabled"
	}
	contextWindowTokens := us.ContextWindowTokens
	if contextWindowTokens <= 0 {
		contextWindowTokens = 200000
	}
	contextBudgetPct := us.ContextBudgetPct
	if contextBudgetPct <= 0 {
		contextBudgetPct = 0.80
	}
	if contextBudgetPct > 1.0 {
		contextBudgetPct = 1.0
	}
	compactionStrategy := us.CompactionStrategy
	if compactionStrategy == "" {
		compactionStrategy = "default"
	}
	switch compactionStrategy {
	case "default", "broker":
		// valid
	default:
		return fmt.Errorf("update user settings: unknown compaction_strategy %q (must be \"default\" or \"broker\")", compactionStrategy)
	}
	toolPerTurnCap := us.ToolPerTurnCap
	if toolPerTurnCap < 0 {
		// CW-20260519-0115: raised from 10 → 150. See the ToolPerTurnCap
		// field doc and migration 066 for the audit rationale.
		toolPerTurnCap = 150
	}
	// 0 = no cap (disabled), positive = cap value.
	toolResultCacheTTL := us.ToolResultCacheTTLSeconds
	if toolResultCacheTTL <= 0 {
		toolResultCacheTTL = 3600
	}
	toolResultSoftTrunc := us.ToolResultSoftTruncBytes
	if toolResultSoftTrunc <= 0 {
		// Matches tool.DefaultSoftTruncBytes — inlined to avoid a store→tool
		// import (store is the lower layer). CW-20260419-0018: lowered from
		// 64 KiB to 2 KiB after c17 UAT showed 64 KiB let large MCP list
		// results (89 KiB clockwork_task_list) bypass the cache-pointer gate
		// and blow the rate budget.
		toolResultSoftTrunc = 2048
	}
	toolResultHardCap := us.ToolResultHardCapBytes
	if toolResultHardCap <= 0 {
		toolResultHardCap = 1048576
	}
	toolClassifierMode := us.ToolClassifierMode
	if toolClassifierMode == "" {
		toolClassifierMode = "broker"
	}
	switch toolClassifierMode {
	case "rules", "llm", "broker":
		// valid
	default:
		return fmt.Errorf("update user settings: unknown tool_classifier_mode %q (must be \"rules\", \"llm\", or \"broker\")", toolClassifierMode)
	}
	toolClassifierTimeoutMS := us.ToolClassifierTimeoutMS
	if toolClassifierTimeoutMS <= 0 {
		toolClassifierTimeoutMS = 500
	}
	// C2 (CW-20260429-0008): validate auto_repair_pref. Empty is the
	// unset sentinel (treated as "always" by the runtime gate); otherwise
	// must be "always" or "never".
	autoRepairPref := us.AutoRepairPref
	switch autoRepairPref {
	case "", "always", "never":
		// valid
	default:
		return fmt.Errorf("update user settings: unknown auto_repair_pref %q (must be \"\", \"always\", or \"never\")", autoRepairPref)
	}
	_, err = s.DB.ExecContext(ctx,
		`UPDATE user_settings SET
			provider_fallback_chain = ?,
			default_provider = ?,
			default_model = ?,
			default_agent = ?,
			utility_provider = ?,
			utility_model = ?,
			tool_call_display_mode = ?,
			settings = ?,
			developer_mode = ?,
			recover_mode = ?,
			tool_stream_behavior = ?,
			tool_drawer_retention = ?,
			tool_load_preferences = ?,
			task_backend = ?,
			allow_unsigned_plugins = ?,
			embedding_provider = ?,
			embedding_model = ?,
			embedding_mode = ?,
			context_window_tokens = ?,
			context_budget_pct = ?,
			summarizer_provider = ?,
			summarizer_model = ?,
			compaction_strategy = ?,
			tool_per_turn_cap = ?,
			tool_result_cache_ttl_seconds = ?,
			tool_result_soft_truncate_bytes = ?,
			tool_result_hard_cap_bytes = ?,
			tool_cache_enabled = ?,
			tool_classifier_mode = ?,
			tool_classifier_provider = ?,
			tool_classifier_model = ?,
			tool_classifier_timeout_ms = ?,
			context_overflow_recovery = ?,
			subagent_approval_required = ?,
			subagent_approval_timeout_seconds = ?,
			auto_repair_pref = ?,
			updated_at = ?
		 WHERE id = 1`,
		string(chainJSON), us.DefaultProvider, us.DefaultModel,
		us.DefaultAgent, us.UtilityProvider, us.UtilityModel, us.ToolCallDisplayMode,
		string(extJSON), us.DeveloperMode, us.RecoverMode,
		us.ToolStreamBehavior, us.ToolDrawerRetention, string(toolPrefsJSON),
		taskBackend, us.AllowUnsignedPlugins,
		us.EmbeddingProvider, us.EmbeddingModel, embeddingMode,
		contextWindowTokens, contextBudgetPct,
		us.SummarizerProvider, us.SummarizerModel, compactionStrategy,
		toolPerTurnCap, toolResultCacheTTL, toolResultSoftTrunc, toolResultHardCap,
		us.ToolCacheEnabled, toolClassifierMode,
		us.ToolClassifierProvider, us.ToolClassifierModel,
		toolClassifierTimeoutMS, us.ContextOverflowRecovery,
		us.SubagentApprovalRequired, us.SubagentApprovalTimeoutSeconds,
		autoRepairPref,
		now,
	)
	if err != nil {
		return fmt.Errorf("update user settings: %w", err)
	}
	return nil
}
