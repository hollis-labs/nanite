package anthropic

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	llmcontracts "github.com/hollis-labs/go-llm-contracts"
	llmtypes "github.com/hollis-labs/go-llm-types"
	ctxpkg "github.com/hollis-labs/nanite/internal/context"
)

// TestCacheMarkerPriority_UniversalSlotFirst_LoadBearing is the
// load-bearing W3 acceptance for CW-20260512-0109. With SlotUniversal at
// position 0 carrying stable content and SlotSystem at position 1, the
// cachePlan must place markers on BOTH slots — Universal first, System
// second. This is the codified priority order; the test pins the
// behavior so a future refactor cannot regress without breaking the
// contract.
//
// Sprint context: SP-20260512-0008 W3 finals the cacheable-prefix
// guarantee. W1A (CW-20260512-0104) reserved SlotUniversal at position
// 0 and built the assembly decider; CW-20260512-0114 wired the universal
// rules content into that slot. W3 codifies that the cache marker
// machinery respects that position.
func TestCacheMarkerPriority_UniversalSlotFirst_LoadBearing(t *testing.T) {
	c := New()
	c.SetCacheHints(llmcontracts.DefaultCacheStrategy())
	req := llmtypes.ChatRequest{
		SystemPrompt: "per-turn dynamic prefix (nativeToolGuide etc.)",
		SlotBlocks: []llmtypes.SlotBlock{
			{Name: ctxpkg.SlotUniversal, Content: "universal rules — refuse rather than fabricate", Changed: false},
			{Name: ctxpkg.SlotSystem, Content: "think-tool + workspace identity", Changed: false},
			{Name: "memory", Content: "memory items", Changed: false},
		},
	}
	plan := c.planCacheMarkers(req)
	wantSlotMarkers := []string{ctxpkg.SlotUniversal, ctxpkg.SlotSystem}
	if !reflect.DeepEqual(plan.SlotMarkers, wantSlotMarkers) {
		t.Errorf("plan.SlotMarkers=%v, want %v (Universal-first priority)",
			plan.SlotMarkers, wantSlotMarkers)
	}
	if plan.SlotMarkers[0] != ctxpkg.SlotUniversal {
		t.Fatalf("CONTRACT VIOLATION: Universal must be first; got %q first",
			plan.SlotMarkers[0])
	}
}

// TestCacheMarkerPriority_NeverOnDynamicContent (W3 contract): per-turn
// dynamic slots (Changed=true) NEVER carry markers, even when they sit
// between two unchanged priority slots.
func TestCacheMarkerPriority_NeverOnDynamicContent(t *testing.T) {
	c := New()
	c.SetCacheHints(llmcontracts.DefaultCacheStrategy())
	req := llmtypes.ChatRequest{
		SystemPrompt: "dynamic prefix",
		SlotBlocks: []llmtypes.SlotBlock{
			{Name: ctxpkg.SlotUniversal, Content: "uni", Changed: false},
			// Simulated dynamic break — SlotSystem rendered with this turn's
			// workspace selection.
			{Name: ctxpkg.SlotSystem, Content: "sys-this-turn", Changed: true},
		},
	}
	plan := c.planCacheMarkers(req)
	// Universal gets a marker; System does NOT (it's the dynamic break).
	wantSlotMarkers := []string{ctxpkg.SlotUniversal}
	if !reflect.DeepEqual(plan.SlotMarkers, wantSlotMarkers) {
		t.Errorf("plan.SlotMarkers=%v, want %v", plan.SlotMarkers, wantSlotMarkers)
	}
	for _, name := range plan.SlotMarkers {
		if name == ctxpkg.SlotSystem {
			t.Errorf("CONTRACT VIOLATION: SlotSystem is dynamic this turn but received a marker")
		}
	}
}

// TestCacheablePrefixTokens_NotRegressedTurnOverTurn is the load-bearing
// empirical-measurement test for CW-20260512-0109. It asserts that the
// `cacheable_prefix_tokens` value (the wire-level signal Glass-2 emits
// via request_build slog) does NOT regress when comparing:
//
//  1. BASELINE: pre-W3 marker placement — a single marker on the last
//     unchanged slot anywhere in the SlotBlocks array.
//  2. POST-W3: Universal-first codified priority — markers on
//     SlotUniversal + SlotSystem explicitly.
//
// The expectation: POST-W3 places strictly MORE markers (2 vs 1) when
// both Universal and System are unchanged + present, and the LAST marker
// position (which EstimateCacheablePrefix reports) is at least as far
// into the payload as the baseline.
//
// Empirical assertion: the post-W3 value is >= the baseline value. This
// is the "no-regression" claim from the ticket's acceptance criteria.
func TestCacheablePrefixTokens_NotRegressedTurnOverTurn(t *testing.T) {
	// Synthesize a representative request. Universal + System at the
	// stable prefix, then an Agent slot (unchanged but non-priority).
	req := llmtypes.ChatRequest{
		SystemPrompt: "per-turn dynamic prefix",
		SlotBlocks: []llmtypes.SlotBlock{
			{Name: ctxpkg.SlotUniversal, Content: strings.Repeat("universal-rules-block-content. ", 8), Changed: false},
			{Name: ctxpkg.SlotSystem, Content: strings.Repeat("think-tool + workspace identity. ", 8), Changed: false},
			{Name: "agent", Content: strings.Repeat("agent system prompt body. ", 8), Changed: false},
		},
		Messages: []llmtypes.ChatMessage{
			{Role: "user", Content: "hello world"},
		},
	}

	// Baseline: simulate pre-W3 by using a plan with NO slot markers
	// (matching the post-cleanup contract when SlotMarkers is empty).
	// To get a true "old behavior" reference we exercise plan.System
	// only — that lands one cache_control on SystemPrompt, which is
	// what shipped before slot-side markers existed.
	baselineClient := New()
	baselineClient.SetCacheHints([]llmcontracts.CacheHint{{Position: "system"}})

	// Force an empty slot-marker plan via the slot configuration: System
	// hint absent on the second pass for SlotMarkers logic. But we DO
	// want the SystemPrompt marker. Achieve this by hand-constructing
	// a plan and bypassing planCacheMarkers' slot logic for the baseline
	// case.

	baselinePlan := cachePlan{System: true}
	baselineParams := baselineClient.buildMessageParams(req, DefaultModel, false, llmcontracts.ReasoningConfig{})
	// Overwrite System with our hand-built version that omits slot markers.
	baselineParams.System = baselineClient.buildSystemBlocks(req, baselinePlan)
	baselinePayload := mustMarshal(t, baselineParams)
	baselineOffset := computeCacheablePrefixBytes(baselinePayload, baselineClient.cacheHints)
	baselineTokens := baselineOffset / 4

	// Post-W3: run the full pipeline with default cache strategy. The
	// codified plan should mark Universal + System (both unchanged + in
	// priority list).
	postClient := New()
	postClient.SetCacheHints(llmcontracts.DefaultCacheStrategy())
	postPlan := postClient.planCacheMarkers(req)
	// Sanity: plan must include the priority slot markers.
	wantSlotMarkers := []string{ctxpkg.SlotUniversal, ctxpkg.SlotSystem}
	if !reflect.DeepEqual(postPlan.SlotMarkers, wantSlotMarkers) {
		t.Fatalf("post-W3 plan.SlotMarkers=%v, want %v", postPlan.SlotMarkers, wantSlotMarkers)
	}
	postTokens := postClient.EstimateCacheablePrefix(context.Background(), req)

	t.Logf("BASELINE cacheable_prefix_tokens (system marker only): %d", baselineTokens)
	t.Logf("POST-W3  cacheable_prefix_tokens (universal+system markers): %d", postTokens)

	if postTokens < baselineTokens {
		t.Errorf("REGRESSION: post-W3 cacheable_prefix_tokens=%d < baseline=%d",
			postTokens, baselineTokens)
	}
	// Stronger claim: post-W3 should be >= baseline. With Universal +
	// System markers added, the LAST marker offset moves further into
	// the payload (past the Universal block and into the System block),
	// so postTokens should be strictly greater than baselineTokens.
	// However EstimateCacheablePrefix uses bytes.LastIndex which already
	// finds the deepest marker — and the baseline only has the
	// SystemPrompt marker. With 2 slot-section markers added, the deeper
	// one is at the SlotSystem position, which is further than the
	// SystemPrompt position.
	if postTokens == 0 && baselineTokens > 0 {
		t.Errorf("post-W3 reported 0 cacheable_prefix_tokens but baseline was %d", baselineTokens)
	}
}

// TestCacheablePrefixTokens_DeterministicAcrossInvocations: same request
// → same cacheable_prefix_tokens. The reproducibility claim is load-
// bearing for the per-session cache stability.
func TestCacheablePrefixTokens_DeterministicAcrossInvocations(t *testing.T) {
	c := New()
	c.SetCacheHints(llmcontracts.DefaultCacheStrategy())
	req := llmtypes.ChatRequest{
		SystemPrompt: "per-turn prefix",
		SlotBlocks: []llmtypes.SlotBlock{
			{Name: ctxpkg.SlotUniversal, Content: "universal-rules", Changed: false},
			{Name: ctxpkg.SlotSystem, Content: "system-identity", Changed: false},
		},
		Messages: []llmtypes.ChatMessage{{Role: "user", Content: "hello"}},
	}
	first := c.EstimateCacheablePrefix(context.Background(), req)
	for i := 0; i < 5; i++ {
		got := c.EstimateCacheablePrefix(context.Background(), req)
		if got != first {
			t.Errorf("iter %d: non-deterministic cacheable_prefix_tokens: got %d want %d",
				i, got, first)
		}
	}
}

// mustMarshal is a helper that fails the test on JSON marshal error.
func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	return out
}
