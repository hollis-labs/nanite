package chat

import (
	"context"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// TestUniversalRulesBlock_NonEmpty asserts the universal rules block is
// non-empty and carries the load-bearing clauses extracted from the
// chat-role-harness body (CW-20260512-0100). The clauses listed here are
// the c160 fabrication-chain regression target — if any of them is missing
// from the block, the layer has drifted and subagents may regress to
// fabrication.
func TestUniversalRulesBlock_NonEmpty(t *testing.T) {
	block := UniversalRulesBlock()
	if block == "" {
		t.Fatal("UniversalRulesBlock returned empty string — layer must be non-empty")
	}

	// Load-bearing clauses — every one is referenced by the deep-dive's
	// recommendations (R1+R2 supersedure rationale) as the rules that must
	// reach every agent regardless of template assignment.
	required := []struct {
		name    string
		needle  string
		purpose string
	}{
		{"grounding-tool-result", "Use what tools return", "deep-dive §1: real vs synthesized"},
		{"refusal-ask-first", "Ask before fabricating", "deep-dive §4: refusal affordance gap"},
		{"count-dont-estimate", "Count, do not estimate", "deep-dive §1: real-data discipline"},
		{"refusal-acknowledge", "Acknowledge honestly when you fail", "deep-dive §4: c160 regression target"},
		{"refusal-explicit-failure", "return an explicit failure", "deep-dive §4 + §6: subagent refusal"},
		{"refusal-no-fabrication", "Refuse rather than fabricate", "c160 regression target"},
		{"verification-peer-draft", "treat the reply as a draft to verify", "deep-dive §2: peer over-trust mitigation"},
	}

	for _, r := range required {
		if !strings.Contains(block, r.needle) {
			t.Errorf("UniversalRulesBlock missing %q (purpose: %s)", r.needle, r.purpose)
		}
	}
}

// TestUniversalRulesBlock_PrefixHelper asserts universalRulesPrefix returns
// the block as a prefix on the existing content, with the existing content
// preserved verbatim after a two-newline separator. This is the contract
// AssembleSlotSources depends on for SlotSystem composition.
func TestUniversalRulesBlock_PrefixHelper(t *testing.T) {
	t.Run("empty existing returns block alone", func(t *testing.T) {
		got := universalRulesPrefix("")
		if got != universalRulesBlock {
			t.Errorf("expected block alone for empty existing; got %q", got)
		}
	})

	t.Run("non-empty existing preserved after block", func(t *testing.T) {
		existing := "Workspace: foo\nThink-tool guidance here."
		got := universalRulesPrefix(existing)
		if !strings.HasPrefix(got, universalRulesBlock) {
			t.Error("universalRulesPrefix did not prepend the block")
		}
		if !strings.HasSuffix(got, existing) {
			t.Error("universalRulesPrefix did not preserve existing content suffix")
		}
		// Two-newline separator is required so the block forms a Markdown
		// section boundary against the subsequent content.
		sep := "\n\n" + existing
		if !strings.HasSuffix(got, sep) {
			t.Errorf("expected two-newline separator before existing content; got %q", got[len(got)-len(sep)-10:])
		}
	})
}

// TestAssembleSlotSources_UniversalRulesInjectedForEmptyProfile is the
// acceptance test from CW-20260512-0100: an agent profile with an EMPTY
// SystemPrompt and NO prompt-template assignment must still receive the
// universal rules via SlotSystem. This is the c160 regression target —
// the researcher subagent that fabricated 8.1KB of analysis had an empty
// SystemPrompt; under the universal-rules layer it would have seen the
// refusal/grounding rules and (per prompt design) returned a failure
// rather than synthesize.
func TestAssembleSlotSources_UniversalRulesInjectedForEmptyProfile(t *testing.T) {
	st := newTestStoreForChat(t)

	// Empty-body profile simulating the c160 researcher: no SystemPrompt,
	// no template assignment. Slug doesn't have to match a reflex resolution
	// since we exercise AssembleSlotSources directly.
	emptyProfile := &store.AgentProfile{
		ID:           "test-empty-profile",
		Name:         "Test Empty Profile",
		Slug:         "test-empty-profile",
		Description:  "Profile with empty SystemPrompt and no template — universal rules layer must still cover it",
		SystemPrompt: "",
	}

	session := &store.Session{}
	// Persist the session so ListMessages doesn't trip on a missing FK.
	if err := st.CreateSession(session); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	cb := &ContextClient{Store: st}
	sources, err := cb.AssembleSlotSources(context.Background(), session, emptyProfile, nil, nil, nil)
	if err != nil {
		t.Fatalf("AssembleSlotSources: %v", err)
	}

	// SlotSystem must carry the universal rules block as the leading prefix.
	// Position is load-bearing — it keeps Anthropic's cacheable_prefix_tokens
	// stable across agents that share the block.
	if !strings.HasPrefix(sources.System, universalRulesBlock) {
		t.Errorf("SlotSystem did not start with universal rules block.\nFirst 400 chars: %q",
			sources.System[:min(400, len(sources.System))])
	}

	// Empty profile = empty Agent slot (no template, no SystemPrompt) — this
	// is the case the universal-rules layer is designed to back-stop.
	if sources.Agent != "" {
		t.Logf("note: Agent slot non-empty for empty profile (likely skill list or compaction disclosure): %q", sources.Agent)
	}

	// Specific clauses must be present in SlotSystem (asserted via the
	// block being included).
	requiredClauses := []string{
		"Use what tools return",
		"Refuse rather than fabricate",
		"Acknowledge honestly when you fail",
		"return an explicit failure",
	}
	for _, c := range requiredClauses {
		if !strings.Contains(sources.System, c) {
			t.Errorf("SlotSystem missing universal clause %q for empty profile", c)
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
