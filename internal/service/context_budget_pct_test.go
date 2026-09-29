package service

import (
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

func TestContextBudgetPct_ReadsUserSetting(t *testing.T) {
	if got := (&contextServiceImpl{}).contextBudgetPct(); got != 0 {
		t.Errorf("no settings wired: %v, want 0 (window default)", got)
	}
	nilSettings := &contextServiceImpl{settingsFunc: func() *store.UserSettings { return nil }}
	if got := nilSettings.contextBudgetPct(); got != 0 {
		t.Errorf("nil settings: %v, want 0", got)
	}
	s := &contextServiceImpl{settingsFunc: func() *store.UserSettings { return &store.UserSettings{ContextBudgetPct: 0.65} }}
	if got := s.contextBudgetPct(); got != 0.65 {
		t.Errorf("configured: %v, want 0.65", got)
	}
}
