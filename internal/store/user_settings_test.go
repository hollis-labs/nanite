package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func newSeededStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Seed(); err != nil {
		t.Fatalf("seed: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestGetUserSettings_Default(t *testing.T) {
	s := newSeededStore(t)

	us, err := s.GetUserSettings()
	if err != nil {
		t.Fatalf("GetUserSettings: %v", err)
	}
	if len(us.ProviderFallbackChain) != 0 {
		t.Errorf("expected empty fallback chain, got %v", us.ProviderFallbackChain)
	}
	if us.DefaultProvider != "" {
		t.Errorf("expected empty default provider, got %q", us.DefaultProvider)
	}
	if us.DefaultModel != "" {
		t.Errorf("expected empty default model, got %q", us.DefaultModel)
	}
}

func TestUpdateUserSettings(t *testing.T) {
	s := newSeededStore(t)

	us := &UserSettings{
		ProviderFallbackChain: []string{"anthropic", "ollama", "pty-claude"},
		DefaultProvider:       "anthropic",
		DefaultModel:          "claude-sonnet-4-20250514",
	}
	if err := s.UpdateUserSettings(us); err != nil {
		t.Fatalf("UpdateUserSettings: %v", err)
	}

	got, err := s.GetUserSettings()
	if err != nil {
		t.Fatalf("GetUserSettings after update: %v", err)
	}
	if len(got.ProviderFallbackChain) != 3 {
		t.Fatalf("expected 3 providers in chain, got %d", len(got.ProviderFallbackChain))
	}
	if got.ProviderFallbackChain[0] != "anthropic" {
		t.Errorf("expected first in chain to be anthropic, got %q", got.ProviderFallbackChain[0])
	}
	if got.ProviderFallbackChain[2] != "pty-claude" {
		t.Errorf("expected third in chain to be pty-claude, got %q", got.ProviderFallbackChain[2])
	}
	if got.DefaultProvider != "anthropic" {
		t.Errorf("expected default provider anthropic, got %q", got.DefaultProvider)
	}
	if got.DefaultModel != "claude-sonnet-4-20250514" {
		t.Errorf("expected default model claude-sonnet-4-20250514, got %q", got.DefaultModel)
	}
}

func TestUserSettings_EmbeddingDefaults(t *testing.T) {
	s := newSeededStore(t)

	us, err := s.GetUserSettings()
	if err != nil {
		t.Fatalf("GetUserSettings: %v", err)
	}
	if us.EmbeddingMode != "disabled" {
		t.Errorf("expected embedding_mode 'disabled', got %q", us.EmbeddingMode)
	}
	if us.EmbeddingProvider != "" {
		t.Errorf("expected empty embedding_provider, got %q", us.EmbeddingProvider)
	}
	if us.EmbeddingModel != "" {
		t.Errorf("expected empty embedding_model, got %q", us.EmbeddingModel)
	}
}

func TestUserSettings_EmbeddingRoundTrip(t *testing.T) {
	s := newSeededStore(t)

	// Step 6.5 (SP-20260508-0001) reduced the embedder catalog to OpenAI;
	// previously this exercised the Ollama+nomic round-trip. The persistence
	// path is provider-agnostic — any string round-trips identically.
	us := &UserSettings{
		EmbeddingProvider: "openai",
		EmbeddingModel:    "text-embedding-3-small",
		EmbeddingMode:     "explicit",
	}
	if err := s.UpdateUserSettings(us); err != nil {
		t.Fatalf("UpdateUserSettings: %v", err)
	}

	got, err := s.GetUserSettings()
	if err != nil {
		t.Fatalf("GetUserSettings: %v", err)
	}
	if got.EmbeddingProvider != "openai" {
		t.Errorf("provider: got %q, want %q", got.EmbeddingProvider, "openai")
	}
	if got.EmbeddingModel != "text-embedding-3-small" {
		t.Errorf("model: got %q, want %q", got.EmbeddingModel, "text-embedding-3-small")
	}
	if got.EmbeddingMode != "explicit" {
		t.Errorf("mode: got %q, want %q", got.EmbeddingMode, "explicit")
	}
}

func TestUpdateUserSettings_EmptyChain(t *testing.T) {
	s := newSeededStore(t)

	// Set a chain, then clear it.
	us := &UserSettings{
		ProviderFallbackChain: []string{"anthropic", "ollama"},
	}
	if err := s.UpdateUserSettings(us); err != nil {
		t.Fatalf("set chain: %v", err)
	}

	us.ProviderFallbackChain = nil
	if err := s.UpdateUserSettings(us); err != nil {
		t.Fatalf("clear chain: %v", err)
	}

	got, err := s.GetUserSettings()
	if err != nil {
		t.Fatalf("get after clear: %v", err)
	}
	if len(got.ProviderFallbackChain) != 0 {
		t.Errorf("expected empty chain after clear, got %v", got.ProviderFallbackChain)
	}
}

func TestUserSettings_ContextWindowRoundTrip(t *testing.T) {
	s := newSeededStore(t)

	us := &UserSettings{
		ContextWindowTokens: 128000,
		ContextBudgetPct:    0.75,
		SummarizerProvider:  "anthropic",
		SummarizerModel:     "claude-haiku",
		CompactionStrategy:  "default",
	}
	if err := s.UpdateUserSettings(us); err != nil {
		t.Fatalf("UpdateUserSettings: %v", err)
	}

	got, err := s.GetUserSettings()
	if err != nil {
		t.Fatalf("GetUserSettings: %v", err)
	}
	if got.ContextWindowTokens != 128000 {
		t.Errorf("ContextWindowTokens: got %d, want 128000", got.ContextWindowTokens)
	}
	if got.ContextBudgetPct != 0.75 {
		t.Errorf("ContextBudgetPct: got %f, want 0.75", got.ContextBudgetPct)
	}
	if got.SummarizerProvider != "anthropic" {
		t.Errorf("SummarizerProvider: got %q, want %q", got.SummarizerProvider, "anthropic")
	}
	if got.SummarizerModel != "claude-haiku" {
		t.Errorf("SummarizerModel: got %q, want %q", got.SummarizerModel, "claude-haiku")
	}
	if got.CompactionStrategy != "default" {
		t.Errorf("CompactionStrategy: got %q, want %q", got.CompactionStrategy, "default")
	}
}

func TestUserSettings_ContextWindowDefaults(t *testing.T) {
	s := newSeededStore(t)

	us, err := s.GetUserSettings()
	if err != nil {
		t.Fatalf("GetUserSettings: %v", err)
	}
	if us.ContextWindowTokens != 200000 {
		t.Errorf("expected default context_window_tokens 200000, got %d", us.ContextWindowTokens)
	}
	if us.ContextBudgetPct != 0.80 {
		t.Errorf("expected default context_budget_pct 0.80, got %f", us.ContextBudgetPct)
	}
	if us.CompactionStrategy != "default" {
		t.Errorf("expected default compaction_strategy 'default', got %q", us.CompactionStrategy)
	}
}

func TestUpdateUserSettings_InvalidCompactionStrategy(t *testing.T) {
	s := newSeededStore(t)

	us := &UserSettings{CompactionStrategy: "unknown-strategy"}
	err := s.UpdateUserSettings(us)
	if err == nil {
		t.Fatal("expected error for unknown compaction_strategy, got nil")
	}
	if !strings.Contains(err.Error(), "unknown-strategy") {
		t.Errorf("error should mention the invalid value; got: %v", err)
	}
}

func TestUserSettings_ToolCacheDefaults(t *testing.T) {
	s := newSeededStore(t)

	us, err := s.GetUserSettings()
	if err != nil {
		t.Fatalf("GetUserSettings: %v", err)
	}
	if !us.ToolCacheEnabled {
		t.Errorf("expected tool_cache_enabled default true, got %v", us.ToolCacheEnabled)
	}
	if us.ToolClassifierMode != "broker" {
		t.Errorf("expected tool_classifier_mode default %q, got %q", "broker", us.ToolClassifierMode)
	}
	if us.ToolClassifierTimeoutMS != 500 {
		t.Errorf("expected tool_classifier_timeout_ms default 500, got %d", us.ToolClassifierTimeoutMS)
	}
	if !us.ContextOverflowRecovery {
		t.Errorf("expected context_overflow_recovery default true, got %v", us.ContextOverflowRecovery)
	}
}

func TestUserSettings_ToolCacheRoundTrip(t *testing.T) {
	s := newSeededStore(t)

	us := &UserSettings{
		ToolCacheEnabled:        false,
		ToolClassifierMode:      "rules",
		ToolClassifierProvider:  "anthropic",
		ToolClassifierModel:     "claude-haiku-4-5",
		ToolClassifierTimeoutMS: 750,
		ContextOverflowRecovery: false,
	}
	if err := s.UpdateUserSettings(us); err != nil {
		t.Fatalf("UpdateUserSettings: %v", err)
	}

	got, err := s.GetUserSettings()
	if err != nil {
		t.Fatalf("GetUserSettings: %v", err)
	}
	if got.ToolCacheEnabled {
		t.Errorf("ToolCacheEnabled: got true, want false")
	}
	if got.ToolClassifierMode != "rules" {
		t.Errorf("ToolClassifierMode: got %q, want %q", got.ToolClassifierMode, "rules")
	}
	if got.ToolClassifierProvider != "anthropic" {
		t.Errorf("ToolClassifierProvider: got %q, want %q", got.ToolClassifierProvider, "anthropic")
	}
	if got.ToolClassifierModel != "claude-haiku-4-5" {
		t.Errorf("ToolClassifierModel: got %q, want %q", got.ToolClassifierModel, "claude-haiku-4-5")
	}
	if got.ToolClassifierTimeoutMS != 750 {
		t.Errorf("ToolClassifierTimeoutMS: got %d, want 750", got.ToolClassifierTimeoutMS)
	}
	if got.ContextOverflowRecovery {
		t.Errorf("ContextOverflowRecovery: got true, want false")
	}
}

func TestUpdateUserSettings_InvalidToolClassifierMode(t *testing.T) {
	s := newSeededStore(t)

	us := &UserSettings{ToolClassifierMode: "bogus"}
	err := s.UpdateUserSettings(us)
	if err == nil {
		t.Fatal("expected error for unknown tool_classifier_mode, got nil")
	}
	if !strings.Contains(err.Error(), "bogus") {
		t.Errorf("error should mention the invalid value; got: %v", err)
	}
}

func TestUpdateUserSettings_ToolClassifierTimeoutDefault(t *testing.T) {
	s := newSeededStore(t)

	// Zero / negative should default to 500.
	us := &UserSettings{ToolClassifierMode: "broker", ToolClassifierTimeoutMS: 0}
	if err := s.UpdateUserSettings(us); err != nil {
		t.Fatalf("UpdateUserSettings: %v", err)
	}
	got, err := s.GetUserSettings()
	if err != nil {
		t.Fatalf("GetUserSettings: %v", err)
	}
	if got.ToolClassifierTimeoutMS != 500 {
		t.Errorf("expected timeout defaulted to 500, got %d", got.ToolClassifierTimeoutMS)
	}
}

func TestUserSettings_G4_DefaultsApplied(t *testing.T) {
	s := newSeededStore(t)
	us, err := s.GetUserSettings()
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !us.SubagentApprovalRequired {
		t.Errorf("SubagentApprovalRequired default = false, want true")
	}
	if us.SubagentApprovalTimeoutSeconds != 86400 {
		t.Errorf("SubagentApprovalTimeoutSeconds default = %d, want 86400", us.SubagentApprovalTimeoutSeconds)
	}
}

func TestUserSettings_G4_UpdateRoundTrips(t *testing.T) {
	s := newSeededStore(t)
	us, _ := s.GetUserSettings()
	us.SubagentApprovalRequired = false
	us.SubagentApprovalTimeoutSeconds = 600
	if err := s.UpdateUserSettings(us); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ := s.GetUserSettings()
	if got.SubagentApprovalRequired {
		t.Errorf("SubagentApprovalRequired not persisted")
	}
	if got.SubagentApprovalTimeoutSeconds != 600 {
		t.Errorf("timeout not persisted: %d", got.SubagentApprovalTimeoutSeconds)
	}
}

// B3 (CW-20260428-0011): mode_auto_switch_pref defaults to "" (unset).
func TestUserSettings_ModeAutoSwitchPref_Default(t *testing.T) {
	s := newSeededStore(t)
	us, err := s.GetUserSettings()
	if err != nil {
		t.Fatalf("GetUserSettings: %v", err)
	}
	if us.ModeAutoSwitchPref != "" {
		t.Errorf("expected ModeAutoSwitchPref default \"\" (unset), got %q", us.ModeAutoSwitchPref)
	}
}

func TestUserSettings_ModeAutoSwitchPref_RoundTrip(t *testing.T) {
	s := newSeededStore(t)
	for _, pref := range []string{"always", "ask", "never", ""} {
		us, _ := s.GetUserSettings()
		us.ModeAutoSwitchPref = pref
		if err := s.UpdateUserSettings(us); err != nil {
			t.Fatalf("UpdateUserSettings(%q): %v", pref, err)
		}
		got, err := s.GetUserSettings()
		if err != nil {
			t.Fatalf("GetUserSettings after %q: %v", pref, err)
		}
		if got.ModeAutoSwitchPref != pref {
			t.Errorf("ModeAutoSwitchPref round-trip: got %q, want %q", got.ModeAutoSwitchPref, pref)
		}
	}
}

func TestUpdateUserSettings_InvalidModeAutoSwitchPref(t *testing.T) {
	s := newSeededStore(t)
	us := &UserSettings{ModeAutoSwitchPref: "sometimes"}
	if err := s.UpdateUserSettings(us); err == nil {
		t.Fatal("expected error for unknown mode_auto_switch_pref, got nil")
	} else if !strings.Contains(err.Error(), "sometimes") {
		t.Errorf("error should mention the invalid value; got: %v", err)
	}
}

func TestUpdateUserSettings_BudgetPctClamp(t *testing.T) {
	s := newSeededStore(t)

	// Over 1.0 should be clamped to 1.0.
	us := &UserSettings{ContextBudgetPct: 1.5}
	if err := s.UpdateUserSettings(us); err != nil {
		t.Fatalf("UpdateUserSettings: %v", err)
	}
	got, err := s.GetUserSettings()
	if err != nil {
		t.Fatalf("GetUserSettings: %v", err)
	}
	if got.ContextBudgetPct != 1.0 {
		t.Errorf("expected budget_pct clamped to 1.0, got %f", got.ContextBudgetPct)
	}

	// Below 0 should default to 0.80.
	us = &UserSettings{ContextBudgetPct: -0.5}
	if err := s.UpdateUserSettings(us); err != nil {
		t.Fatalf("UpdateUserSettings negative: %v", err)
	}
	got, err = s.GetUserSettings()
	if err != nil {
		t.Fatalf("GetUserSettings after negative: %v", err)
	}
	if got.ContextBudgetPct != 0.80 {
		t.Errorf("expected budget_pct defaulted to 0.80 for negative input, got %f", got.ContextBudgetPct)
	}
}
