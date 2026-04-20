package service

import (
	"testing"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/classify"
)

func TestLoopState_Classification(t *testing.T) {
	ls := newLoopState(chat.AgentConstraints{}, nil, false)

	// Default (unset) must return zero values but the accessor must not panic.
	tier, pattern := ls.Classification()
	if tier != classify.TierInvalid {
		t.Fatalf("default tier = %v, want TierInvalid", tier)
	}
	if pattern != classify.PatternInvalid {
		t.Fatalf("default pattern = %v, want PatternInvalid", pattern)
	}

	// Setter stores the pair.
	ls.SetClassification(classify.TierMedium, classify.PatternSubagent)
	tier, pattern = ls.Classification()
	if tier != classify.TierMedium {
		t.Errorf("tier = %v, want TierMedium", tier)
	}
	if pattern != classify.PatternSubagent {
		t.Errorf("pattern = %v, want PatternSubagent", pattern)
	}
}

func TestBuildIntentSignals(t *testing.T) {
	got := buildIntentSignals("fix the typo in README", []string{"edit", "grep"}, false)
	if got.Message != "fix the typo in README" {
		t.Errorf("Message round-trip failed: %q", got.Message)
	}
	if got.MessageTokenEst <= 0 {
		t.Errorf("token estimate should be positive, got %d", got.MessageTokenEst)
	}
	if got.ToolsAvailable != 2 {
		t.Errorf("ToolsAvailable = %d, want 2", got.ToolsAvailable)
	}
	if got.HasAttachments {
		t.Errorf("HasAttachments = true, want false")
	}
}
