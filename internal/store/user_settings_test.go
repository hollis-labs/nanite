package store

import (
	"path/filepath"
	"strings"
	"testing"
)

func newSeededStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(filepath.Join(t.TempDir(), "test.db"))
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

	us := &UserSettings{
		EmbeddingProvider: "ollama",
		EmbeddingModel:    "nomic-embed-text",
		EmbeddingMode:     "explicit",
	}
	if err := s.UpdateUserSettings(us); err != nil {
		t.Fatalf("UpdateUserSettings: %v", err)
	}

	got, err := s.GetUserSettings()
	if err != nil {
		t.Fatalf("GetUserSettings: %v", err)
	}
	if got.EmbeddingProvider != "ollama" {
		t.Errorf("provider: got %q, want %q", got.EmbeddingProvider, "ollama")
	}
	if got.EmbeddingModel != "nomic-embed-text" {
		t.Errorf("model: got %q, want %q", got.EmbeddingModel, "nomic-embed-text")
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
