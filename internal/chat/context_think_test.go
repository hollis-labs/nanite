package chat

import (
	"strings"
	"testing"
)

// TestThinkBlockV1Content verifies that thinkToolBlockV1 mentions all four
// required affordances: scratchpad, memory, playbooks, and peer-query.
func TestThinkBlockV1Content(t *testing.T) {
	block := thinkToolBlockV1

	checks := []struct {
		label    string
		contains string
	}{
		{"scratchpad affordance", "scratchpad"},
		{"memory affordance", "memory"},
		{"playbooks affordance", "playbooks"},
		{"peer-query affordance", "peer-query"},
	}

	lower := strings.ToLower(block)
	for _, c := range checks {
		if !strings.Contains(lower, c.contains) {
			t.Errorf("thinkToolBlockV1 missing %s: no %q found in block", c.label, c.contains)
		}
	}
}

// TestThinkBlockV1TokenBudget asserts that the v1 hint block stays within
// the 200-token budget (CW-20260420-0021). Uses EstimateTokens (chars/4)
// as the proxy tokenizer — the same function used everywhere in the chat package.
func TestThinkBlockV1TokenBudget(t *testing.T) {
	const maxTokens = 200

	// Strip the leading newline that the block constant intentionally carries
	// so the estimate reflects the rendered content only.
	block := strings.TrimLeft(thinkToolBlockV1, "\n")
	tokens := EstimateTokens(block)
	if tokens > maxTokens {
		t.Errorf("thinkToolBlockV1 exceeds 200-token budget: estimated %d tokens (%d chars)",
			tokens, len(block))
	}
	t.Logf("thinkToolBlockV1 token estimate: %d tokens (%d chars)", tokens, len(block))
}

// TestThinkBlockV1F5TODO verifies that thinkToolBlockV1 carries a source-level
// reference to F5 (CW-20260420-0022), signaling the seam for dynamic
// hint selection.
func TestThinkBlockV1F5TODO(t *testing.T) {
	// The TODO lives in the Go source comment above the const, not in the
	// runtime string. Test the const-body mention of the forthcoming ticket.
	if !strings.Contains(thinkToolBlockV1, "CW-20260420-0022") {
		t.Error("thinkToolBlockV1 body should reference CW-20260420-0022 for F5 seam visibility")
	}
}

// TestIsThinkBlockV1Enabled_DefaultOn verifies the feature flag defaults to ON
// when NANITE_THINK_BLOCK_V1 is unset.
func TestIsThinkBlockV1Enabled_DefaultOn(t *testing.T) {
	t.Setenv("NANITE_THINK_BLOCK_V1", "")
	if !IsThinkBlockV1Enabled() {
		t.Error("IsThinkBlockV1Enabled() should return true when env var is unset (default ON)")
	}
}

// TestIsThinkBlockV1Enabled_ExplicitTrue verifies the flag is ON when set to "true".
func TestIsThinkBlockV1Enabled_ExplicitTrue(t *testing.T) {
	t.Setenv("NANITE_THINK_BLOCK_V1", "true")
	if !IsThinkBlockV1Enabled() {
		t.Error("IsThinkBlockV1Enabled() should return true when NANITE_THINK_BLOCK_V1=true")
	}
}

// TestIsThinkBlockV1Enabled_OptOut_False verifies opt-back-to-v0 via "false".
func TestIsThinkBlockV1Enabled_OptOut_False(t *testing.T) {
	t.Setenv("NANITE_THINK_BLOCK_V1", "false")
	if IsThinkBlockV1Enabled() {
		t.Error("IsThinkBlockV1Enabled() should return false when NANITE_THINK_BLOCK_V1=false")
	}
}

// TestIsThinkBlockV1Enabled_OptOut_Zero verifies opt-back-to-v0 via "0".
func TestIsThinkBlockV1Enabled_OptOut_Zero(t *testing.T) {
	t.Setenv("NANITE_THINK_BLOCK_V1", "0")
	if IsThinkBlockV1Enabled() {
		t.Error("IsThinkBlockV1Enabled() should return false when NANITE_THINK_BLOCK_V1=0")
	}
}

// TestIsThinkBlockV1Enabled_OptOut_No verifies opt-back-to-v0 via "no".
func TestIsThinkBlockV1Enabled_OptOut_No(t *testing.T) {
	t.Setenv("NANITE_THINK_BLOCK_V1", "no")
	if IsThinkBlockV1Enabled() {
		t.Error("IsThinkBlockV1Enabled() should return false when NANITE_THINK_BLOCK_V1=no")
	}
}

// TestThinkToolBlock_V1 verifies ThinkToolBlock() returns the v1 block when the flag is on.
func TestThinkToolBlock_V1(t *testing.T) {
	t.Setenv("NANITE_THINK_BLOCK_V1", "true")
	got := ThinkToolBlock()
	if got != thinkToolBlockV1 {
		t.Error("ThinkToolBlock() should return thinkToolBlockV1 when flag is true")
	}
}

// TestThinkToolBlock_V0 verifies ThinkToolBlock() returns the v0 block when the flag is off.
func TestThinkToolBlock_V0(t *testing.T) {
	t.Setenv("NANITE_THINK_BLOCK_V1", "false")
	got := ThinkToolBlock()
	if got != thinkToolBlock {
		t.Error("ThinkToolBlock() should return thinkToolBlock (v0) when flag is false")
	}
}
