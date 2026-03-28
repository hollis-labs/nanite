package store

import (
	"path/filepath"
	"testing"
)

func TestGetUserSettings_Default(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

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
	s, err := New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

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

func TestUpdateUserSettings_EmptyChain(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

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
