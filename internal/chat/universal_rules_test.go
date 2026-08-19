package chat

import (
	"context"
	"strings"
	"testing"

	nctx "github.com/hollis-labs/nanite/internal/context"
	"github.com/hollis-labs/nanite/internal/contextbroker"
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
		// CW-20260512-0122 (SP-20260512-0011 W2): subagent failure envelope rule.
		// LLM-side counterpart to internal/subagent/envelope.go — the parent
		// reads success=false and acknowledges, instead of narrating the
		// child's last assistant text as authoritative (c160 turn-18 class).
		{"refusal-subagent-envelope", "Acknowledge subagent failure", "c160 turn-18: parent narrates fake success"},
		{"refusal-subagent-envelope-flag", "success flag is the source of truth", "envelope contract: do not narrate success on success=false"},
		// CW-20260519-0068: silent multi-minute turn problem (session c256,
		// turn 6b55d90a — 14+ min, 4 subagent dispatches, zero chat output).
		// This is the prompt-level narration half of the fix; the
		// harness-level half is subagent.Service's heartbeat ping
		// (internal/subagent/service.go).
		{"narration-long-waits", "Narrate long waits", "CW-20260519-0068: silent multi-minute turn"},
		{"narration-still-working", "still working on X", "CW-20260519-0068: heartbeat guidance for long silent stretches"},
	}

	for _, r := range required {
		if !strings.Contains(block, r.needle) {
			t.Errorf("UniversalRulesBlock missing %q (purpose: %s)", r.needle, r.purpose)
		}
	}
}

// TestAssembleSlotSources_UniversalRulesInSlotUniversal is the
// acceptance test from CW-20260512-0114: an agent profile with an EMPTY
// SystemPrompt and NO prompt-template assignment must still receive the
// universal rules via SlotSources.Universal (which the Context Broker
// emits at position 0 of SlotOrder). This is the c160 regression target —
// the researcher subagent that fabricated 8.1KB of analysis had an empty
// SystemPrompt; under the universal-rules slot it would have seen the
// refusal/grounding rules and (per prompt design) returned a failure
// rather than synthesize.
//
// Post-CW-20260512-0114: the block lives in SlotSources.Universal, NOT
// in SlotSources.System. The legacy universalRulesPrefix helper and its
// in-SlotSystem prepend are removed (feedback_no_compat_shims).
func TestAssembleSlotSources_UniversalRulesInSlotUniversal(t *testing.T) {
	st := newTestStoreForChat(t)

	// Empty-body profile simulating the c160 researcher: no SystemPrompt,
	// no template assignment. Slug doesn't have to match a reflex resolution
	// since we exercise AssembleSlotSources directly.
	emptyProfile := &store.AgentProfile{
		ID:           "test-empty-profile",
		Name:         "Test Empty Profile",
		Slug:         "test-empty-profile",
		Description:  "Profile with empty SystemPrompt and no template — universal rules slot must still cover it",
		SystemPrompt: "",
	}

	session := &store.Session{}
	// Persist the session so ListMessages doesn't trip on a missing FK.
	if err := st.CreateSession(session); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	cb := &ContextClient{Store: st}
	sources, err := cb.AssembleSlotSources(context.Background(), session, emptyProfile, nil)
	if err != nil {
		t.Fatalf("AssembleSlotSources: %v", err)
	}

	// SlotUniversal must carry the universal rules block VERBATIM — no
	// prefix, no suffix, no separator. The slot ships standalone at
	// position 0 (the Context Broker emits it for every dispatch type).
	if sources.Universal != universalRulesBlock {
		head := sources.Universal
		if len(head) > 400 {
			head = head[:400]
		}
		t.Errorf("SlotUniversal did not match universalRulesBlock exactly.\nFirst 400 chars: %q", head)
	}

	// SlotSystem MUST NOT carry the universal-rules block. The whole point
	// of the CW-20260512-0114 refactor is that the block lives at position
	// 0 of SlotOrder in its own slot, not duplicated inside SlotSystem.
	// Asserting absence here pins the no-double-ship contract closed —
	// drift would balloon the cacheable prefix with duplicate content.
	if strings.Contains(sources.System, universalRulesBlock) {
		t.Error("SlotSystem must NOT contain the universal-rules block — it lives in SlotUniversal post-CW-20260512-0114")
	}

	// Empty profile = empty Agent slot (no template, no SystemPrompt) — this
	// is the case the universal-rules slot is designed to back-stop.
	if sources.Agent != "" {
		t.Logf("note: Agent slot non-empty for empty profile (likely skill list or compaction disclosure): %q", sources.Agent)
	}

	// Specific clauses must be present in SlotUniversal (asserted via the
	// block being included).
	requiredClauses := []string{
		"Use what tools return",
		"Refuse rather than fabricate",
		"Acknowledge honestly when you fail",
		"return an explicit failure",
	}
	for _, c := range requiredClauses {
		if !strings.Contains(sources.Universal, c) {
			t.Errorf("SlotUniversal missing universal clause %q for empty profile", c)
		}
	}
}

// TestAssembleSlotSources_UniversalSlotEmittedForSubagentDispatch is the
// CW-20260512-0114 smoke test for subagent inheritance: a subagent-style
// session with an empty SystemPrompt produces SlotSources.Universal with
// the rules block. This proves the slot reaches subagent dispatches the
// same way it reaches chat dispatches — the Context Broker emits the slot
// unconditionally at position 0 regardless of profile or dispatch type.
//
// The c160 fabrication chain was a researcher SUBAGENT with no template
// assignment. Pre-CW-20260512-0100, that subagent got zero universal rules.
// Post-CW-20260512-0100, the rules rode on SlotSystem via universalRulesPrefix.
// Post-CW-20260512-0114, the rules ride on SlotUniversal directly — this
// test pins the slot population for the subagent-dispatch case.
func TestAssembleSlotSources_UniversalSlotEmittedForSubagentDispatch(t *testing.T) {
	st := newTestStoreForChat(t)

	// Researcher-shaped profile mirroring the c160 fabrication target.
	researcherProfile := &store.AgentProfile{
		ID:           "test-researcher-subagent",
		Name:         "Test Researcher",
		Slug:         "test-researcher",
		Description:  "Researcher subagent with empty SystemPrompt — must still inherit universal rules via SlotUniversal",
		SystemPrompt: "",
	}

	// Sub-session shape — workspace optional; the slot must populate even
	// without one.
	session := &store.Session{}
	if err := st.CreateSession(session); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	cb := &ContextClient{Store: st}
	sources, err := cb.AssembleSlotSources(context.Background(), session, researcherProfile, nil)
	if err != nil {
		t.Fatalf("AssembleSlotSources: %v", err)
	}

	if sources.Universal == "" {
		t.Fatal("SlotUniversal is empty for subagent dispatch — universal rules failed to reach the slot")
	}
	if sources.Universal != universalRulesBlock {
		t.Error("SlotUniversal content drifted from canonical UniversalRulesBlock — content must be sourced verbatim")
	}
}

// TestUniversalRulesBlock_FitsSlotUniversalBudget is the CW-20260513-0036
// review-round-1 regression guard: pin UniversalRulesBlock()'s token count
// under DefaultBudgets()[SlotUniversal] so future prompt edits cannot push
// the block over the ceiling and silently reintroduce the stash/fallback
// warning (assembly.go emits this when EstimateTokens(content) > budget).
//
// The existing TestUniversalRulesBlock_NonEmpty / TestAssembleSlotSources_*
// tests assert sentinel text and verbatim slot population — they do NOT
// constrain size. Without this test, an editor can add a clause that
// pushes the block past 550 tokens; runtime then logs
// "contextbroker: slot stash failed, falling back to inline ship" every
// dispatch, but CI stays green. This test fails loud and points at where
// to fix.
//
// Uses contextbroker.EstimateTokens — the SAME helper the assembly path
// uses to compare against per-slot budgets (see assembly.go ~L262, where
// `tokens := EstimateTokens(content)` gates the oversized branch). Asserts
// only the `<= budget` relation, not an exact count, so the block can
// grow within budget without churning this test.
func TestUniversalRulesBlock_FitsSlotUniversalBudget(t *testing.T) {
	block := UniversalRulesBlock()
	budget := nctx.DefaultBudgets()[nctx.SlotUniversal]
	tokens := contextbroker.EstimateTokens(block)
	if tokens > budget {
		t.Fatalf("UniversalRulesBlock is %d tokens, exceeds SlotUniversal budget of %d "+
			"(would trigger contextbroker stash/fallback warning at runtime every dispatch). "+
			"Either trim the block in internal/chat/universal_rules.go or bump the budget "+
			"in internal/context/slot.go:208.",
			tokens, budget)
	}
}
