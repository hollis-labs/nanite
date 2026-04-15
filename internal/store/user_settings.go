package store

import (
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
	// verified Ed25519 signature. The setting is ONLY honoured in builds
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
}

// GetUserSettings returns the singleton user settings row.
func (s *Store) GetUserSettings() (*UserSettings, error) {
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
	err := s.DB.QueryRow(
		`SELECT provider_fallback_chain, default_provider, default_model,
		        default_agent, utility_provider, utility_model, tool_call_display_mode, settings,
		        developer_mode, recover_mode, tool_stream_behavior, tool_drawer_retention,
		        tool_load_preferences, task_backend, allow_unsigned_plugins,
		        embedding_provider, embedding_model, embedding_mode,
		        context_window_tokens, context_budget_pct,
		        summarizer_provider, summarizer_model, compaction_strategy
		 FROM user_settings WHERE id = 1`,
	).Scan(&chainJSON, &provider, &model,
		&agent, &utilProvider, &utilModel, &toolMode, &settingsJSON,
		&devMode, &recoverMode, &toolStreamBehavior, &toolDrawerRetention,
		&toolLoadPrefsJSON, &taskBackend, &allowUnsigned,
		&embeddingProvider, &embeddingModel, &embeddingMode,
		&contextWindowTokens, &contextBudgetPct,
		&summarizerProvider, &summarizerModel, &compactionStrategy)
	if err != nil {
		return nil, fmt.Errorf("get user settings: %w", err)
	}

	us := &UserSettings{
		DefaultProvider:     provider,
		DefaultModel:        model,
		DefaultAgent:        agent,
		UtilityProvider:     utilProvider,
		UtilityModel:        utilModel,
		ToolCallDisplayMode: toolMode,
		ToolStreamBehavior:  toolStreamBehavior,
		ToolDrawerRetention: toolDrawerRetention,
		DeveloperMode:        devMode,
		RecoverMode:          recoverMode,
		TaskBackend:          taskBackend,
		AllowUnsignedPlugins: allowUnsigned,
		EmbeddingProvider:    embeddingProvider,
		EmbeddingModel:       embeddingModel,
		EmbeddingMode:        embeddingMode,
		ContextWindowTokens:  contextWindowTokens,
		ContextBudgetPct:     contextBudgetPct,
		SummarizerProvider:   summarizerProvider,
		SummarizerModel:      summarizerModel,
		CompactionStrategy:   compactionStrategy,
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
func (s *Store) UpdateUserSettings(us *UserSettings) error {
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
	compactionStrategy := us.CompactionStrategy
	if compactionStrategy == "" {
		compactionStrategy = "default"
	}
	_, err = s.DB.Exec(
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
		now,
	)
	if err != nil {
		return fmt.Errorf("update user settings: %w", err)
	}
	return nil
}
