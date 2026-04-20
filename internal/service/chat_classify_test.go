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

// TestClassifyAndAttach_AttachesClassification asserts that the pre-loop
// classify helper (called from generateResponse) feeds Classify with the
// expected signals AND writes the result to loopState. The classifyFn
// indirection lets us install a recording fake to capture the inputs.
// This is the E2E gate for CW-20260420-0013 — exercising the same helper
// generateResponse calls, not reconstructing it.
func TestClassifyAndAttach_AttachesClassification(t *testing.T) {
	var captured classify.IntentSignals
	originalFn := classifyFn
	classifyFn = func(i classify.IntentSignals) (classify.ScopeTier, classify.ExecutionPattern) {
		captured = i
		return classify.TierMedium, classify.PatternSubagent
	}
	t.Cleanup(func() { classifyFn = originalFn })

	ls := newLoopState(chat.AgentConstraints{}, nil, false)
	classifyAndAttach(ls, "session-123", "investigate why the export is missing rows", []string{"edit", "grep"})

	tier, pattern := ls.Classification()
	if tier != classify.TierMedium {
		t.Errorf("tier = %v, want TierMedium", tier)
	}
	if pattern != classify.PatternSubagent {
		t.Errorf("pattern = %v, want PatternSubagent", pattern)
	}
	if captured.Message != "investigate why the export is missing rows" {
		t.Errorf("Classify called with wrong Message: %q", captured.Message)
	}
	if captured.ToolsAvailable != 2 {
		t.Errorf("Classify called with wrong ToolsAvailable: %d, want 2", captured.ToolsAvailable)
	}
	if captured.HasAttachments {
		t.Errorf("Classify called with HasAttachments=true, want false")
	}
}

// TestBuildIntentSignals_ShortNonEmptyMessage guards against regression of
// the floor-of-1 MessageTokenEst behavior. A 2-char message like "hi"
// must yield a positive estimate so classifyTier's est>0 branches can
// fire. Addresses Copilot review on PR #74.
func TestBuildIntentSignals_ShortNonEmptyMessage(t *testing.T) {
	got := buildIntentSignals("hi", nil, false)
	if got.MessageTokenEst <= 0 {
		t.Errorf("short non-empty message: MessageTokenEst = %d, want > 0 (floor-of-1 contract)", got.MessageTokenEst)
	}
}
