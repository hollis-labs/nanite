package anthropic

import (
	"reflect"
	"testing"

	llmcontracts "github.com/hollis-labs/go-llm-contracts"
	llmtypes "github.com/hollis-labs/go-llm-types"
	ctxpkg "github.com/hollis-labs/nanite/internal/context"
)

// TestCachePlan_AllSourcesWithinBudget: system + tools + 2x recent_message,
// no slot blocks. Total = 1 + 1 + 2 + 0 = 4. All hints honored,
// SlotMarkers stays empty (no slots to mark).
func TestCachePlan_AllSourcesWithinBudget(t *testing.T) {
	c := New()
	c.SetCacheHints([]llmcontracts.CacheHint{
		{Position: "system"},
		{Position: "tools"},
		{Position: "recent_message", Index: 0},
		{Position: "recent_message", Index: 1},
	})
	plan := c.planCacheMarkers(llmtypes.ChatRequest{})
	if !plan.System {
		t.Errorf("System=false, want true")
	}
	if !plan.Tools {
		t.Errorf("Tools=false, want true")
	}
	if len(plan.SlotMarkers) != 0 {
		t.Errorf("SlotMarkers=%v, want empty (no slot blocks)", plan.SlotMarkers)
	}
	if plan.RecentMessages != 2 {
		t.Errorf("RecentMessages=%d, want 2", plan.RecentMessages)
	}
}

// TestCachePlan_UniversalSlotFirstMarker (CW-20260512-0109 W3 contract):
// when SlotUniversal is the only stable-prefix slot present + unchanged,
// it gets the first slot marker.
func TestCachePlan_UniversalSlotFirstMarker(t *testing.T) {
	c := New()
	c.SetCacheHints([]llmcontracts.CacheHint{{Position: "system"}})
	req := llmtypes.ChatRequest{
		SlotBlocks: []llmtypes.SlotBlock{
			{Name: ctxpkg.SlotUniversal, Content: "universal", Changed: false},
		},
	}
	plan := c.planCacheMarkers(req)
	if !reflect.DeepEqual(plan.SlotMarkers, []string{ctxpkg.SlotUniversal}) {
		t.Errorf("SlotMarkers=%v, want [%q]", plan.SlotMarkers, ctxpkg.SlotUniversal)
	}
}

// TestCachePlan_UniversalThenSystem (CW-20260512-0109 W3 contract):
// Universal at position 0 + System at position 1, both unchanged → BOTH
// receive markers. Universal is first in the list.
func TestCachePlan_UniversalThenSystem(t *testing.T) {
	c := New()
	c.SetCacheHints([]llmcontracts.CacheHint{{Position: "system"}})
	req := llmtypes.ChatRequest{
		SlotBlocks: []llmtypes.SlotBlock{
			{Name: ctxpkg.SlotUniversal, Content: "uni", Changed: false},
			{Name: ctxpkg.SlotSystem, Content: "sys", Changed: false},
		},
	}
	plan := c.planCacheMarkers(req)
	want := []string{ctxpkg.SlotUniversal, ctxpkg.SlotSystem}
	if !reflect.DeepEqual(plan.SlotMarkers, want) {
		t.Errorf("SlotMarkers=%v, want %v", plan.SlotMarkers, want)
	}
}

// TestCachePlan_DynamicBreakStopsPrefixRun (CW-20260512-0109 W3 contract):
// a Changed slot between Universal and System breaks the prefix run.
// Only Universal gets a marker — System is past a dynamic break.
func TestCachePlan_DynamicBreakStopsPrefixRun(t *testing.T) {
	c := New()
	c.SetCacheHints([]llmcontracts.CacheHint{{Position: "system"}})
	req := llmtypes.ChatRequest{
		SlotBlocks: []llmtypes.SlotBlock{
			{Name: ctxpkg.SlotUniversal, Content: "uni", Changed: false},
			{Name: "memory", Content: "mem", Changed: true},
			{Name: ctxpkg.SlotSystem, Content: "sys", Changed: false},
		},
	}
	plan := c.planCacheMarkers(req)
	want := []string{ctxpkg.SlotUniversal}
	if !reflect.DeepEqual(plan.SlotMarkers, want) {
		t.Errorf("SlotMarkers=%v, want %v (dynamic break stops run)", plan.SlotMarkers, want)
	}
}

// TestCachePlan_NonPriorityFirstSlotEmpty (CW-20260512-0109 W3 contract):
// a non-priority unchanged slot at position 0 stops the prefix run —
// markers are never planted past an undeclared-stability slot.
func TestCachePlan_NonPriorityFirstSlotStopsRun(t *testing.T) {
	c := New()
	c.SetCacheHints([]llmcontracts.CacheHint{{Position: "system"}})
	req := llmtypes.ChatRequest{
		SlotBlocks: []llmtypes.SlotBlock{
			// "agent" is not in stablePrefixSlotPriority — even unchanged,
			// it breaks the run.
			{Name: "agent", Content: "agent", Changed: false},
			{Name: ctxpkg.SlotUniversal, Content: "uni", Changed: false},
		},
	}
	plan := c.planCacheMarkers(req)
	if len(plan.SlotMarkers) != 0 {
		t.Errorf("SlotMarkers=%v, want empty (non-priority first slot stops run)", plan.SlotMarkers)
	}
}

// TestCachePlan_OverBudgetDropsRecentMessages: 1 system + 1 tools + 3 rm +
// TestCachePlan_OverBudgetDropsExcessRecentMessages: 2 slot markers (universal + system) +
// 3 rm = 5 wanted, cap = 4. Drops the 1 excess rm (3 -> 2). Both slot markers and
// 2 recent messages survive.
func TestCachePlan_OverBudgetDropsExcessRecentMessages(t *testing.T) {
	c := New()
	c.SetCacheHints([]llmcontracts.CacheHint{
		{Position: "system"},
		{Position: "recent_message", Index: 0},
		{Position: "recent_message", Index: 1},
		{Position: "recent_message", Index: 2},
	})
	req := llmtypes.ChatRequest{
		SlotBlocks: []llmtypes.SlotBlock{
			{Name: ctxpkg.SlotUniversal, Content: "uni", Changed: false},
			{Name: ctxpkg.SlotSystem, Content: "sys", Changed: false},
		},
	}
	plan := c.planCacheMarkers(req)
	if !plan.System {
		t.Errorf("System must not drop")
	}
	if len(plan.SlotMarkers) != 2 {
		t.Errorf("SlotMarkers=%v, want [universal, system] preserved", plan.SlotMarkers)
	}
	if plan.RecentMessages != 2 {
		t.Errorf("RecentMessages=%d, want 2 (excess 1 dropped to fit cap)", plan.RecentMessages)
	}
	total := plan.RecentMessages + len(plan.SlotMarkers)
	if total > maxCacheControlMarkers {
		t.Errorf("total markers %d > cap %d", total, maxCacheControlMarkers)
	}
}

// TestCachePlan_DropsSlotMarkersFromTail: when tools are present alongside slot
// blocks and 2 recent messages, total demand = 2 slots + 1 tools + 2 rm = 5.
// Slot markers drop from the TAIL (system yields because tools already caches it)
// so SlotUniversal (position 0 anchor) + Tools + 2 RecentMessages fit within cap 4.
func TestCachePlan_DropsSlotMarkersFromTail(t *testing.T) {
	c := New()
	c.SetCacheHints([]llmcontracts.CacheHint{
		{Position: "system"},
		{Position: "tools"},
		{Position: "recent_message", Index: 0},
		{Position: "recent_message", Index: 1},
	})
	req := llmtypes.ChatRequest{
		Tools: []llmtypes.ToolDefinition{
			{Name: "dev_read"},
		},
		SlotBlocks: []llmtypes.SlotBlock{
			{Name: ctxpkg.SlotUniversal, Content: "uni", Changed: false},
			{Name: ctxpkg.SlotSystem, Content: "sys", Changed: false},
		},
	}
	plan := c.planCacheMarkers(req)
	// Expect: SlotUniversal (1) + Tools (1) + 2 rm = 4 — fits.
	if plan.RecentMessages != 2 {
		t.Errorf("RecentMessages=%d, want 2 (preserved)", plan.RecentMessages)
	}
	if len(plan.SlotMarkers) != 1 || plan.SlotMarkers[0] != ctxpkg.SlotUniversal {
		t.Fatalf("SlotMarkers=%v, want [universal] (tail slot dropped, Universal anchor preserved)", plan.SlotMarkers)
	}
	if !plan.Tools {
		t.Errorf("Tools must be true")
	}
}

// TestCachePlan_AllSlotsChanged_NoSlotMarker: even with system hint set,
// if every slot is Changed=true, no slot is unchanged so SlotMarkers
// stays empty.
func TestCachePlan_AllSlotsChanged_NoSlotMarker(t *testing.T) {
	c := New()
	c.SetCacheHints([]llmcontracts.CacheHint{{Position: "system"}})
	req := llmtypes.ChatRequest{
		SlotBlocks: []llmtypes.SlotBlock{
			{Name: ctxpkg.SlotUniversal, Content: "a", Changed: true},
			{Name: ctxpkg.SlotSystem, Content: "b", Changed: true},
		},
	}
	plan := c.planCacheMarkers(req)
	if !plan.System {
		t.Errorf("System=false, want true")
	}
	if len(plan.SlotMarkers) != 0 {
		t.Errorf("SlotMarkers=%v, want empty (no unchanged slots)", plan.SlotMarkers)
	}
}

// TestCachePlan_NoSystemHint_NoSlotMarker: slots unchanged but system hint
// absent — SlotMarkers stays empty (mutual gate; the slot section is part
// of the system context and we don't emit a disconnected marker).
func TestCachePlan_NoSystemHint_NoSlotMarker(t *testing.T) {
	c := New()
	// No "system" hint; only "tools".
	c.SetCacheHints([]llmcontracts.CacheHint{{Position: "tools"}})
	req := llmtypes.ChatRequest{
		SlotBlocks: []llmtypes.SlotBlock{
			{Name: ctxpkg.SlotUniversal, Content: "stable", Changed: false},
		},
	}
	plan := c.planCacheMarkers(req)
	if plan.System {
		t.Errorf("System=true, want false")
	}
	if len(plan.SlotMarkers) != 0 {
		t.Errorf("SlotMarkers=%v, want empty (no system hint to anchor)", plan.SlotMarkers)
	}
}

// TestCachePlan_EmptyHints: no hints, no slots → plan is all-zero.
func TestCachePlan_EmptyHints(t *testing.T) {
	c := New()
	plan := c.planCacheMarkers(llmtypes.ChatRequest{})
	if plan.System || plan.Tools || len(plan.SlotMarkers) != 0 || plan.RecentMessages != 0 {
		t.Errorf("expected zero plan, got %+v", plan)
	}
}

// TestCachePlan_EmptySlotsContentIgnored: a slot with empty Content is
// skipped during the prefix walk but does not break the run — the next
// slot in the array is considered for the marker.
func TestCachePlan_EmptySlotsContentSkippedNotBreak(t *testing.T) {
	c := New()
	c.SetCacheHints([]llmcontracts.CacheHint{{Position: "system"}})
	req := llmtypes.ChatRequest{
		SlotBlocks: []llmtypes.SlotBlock{
			{Name: ctxpkg.SlotUniversal, Content: "", Changed: false},
			{Name: ctxpkg.SlotSystem, Content: "sys", Changed: false},
		},
	}
	plan := c.planCacheMarkers(req)
	// Empty Universal is skipped; System still eligible.
	want := []string{ctxpkg.SlotSystem}
	if !reflect.DeepEqual(plan.SlotMarkers, want) {
		t.Errorf("SlotMarkers=%v, want %v (empty Universal skipped, System still eligible)",
			plan.SlotMarkers, want)
	}
}

// TestCollectStablePrefixSlots_Determinism (CW-20260512-0109 W3 contract):
// same input → same output, always. Idempotent marker placement is the
// load-bearing claim of W3.
func TestCollectStablePrefixSlots_Determinism(t *testing.T) {
	blocks := []llmtypes.SlotBlock{
		{Name: ctxpkg.SlotUniversal, Content: "uni", Changed: false},
		{Name: ctxpkg.SlotSystem, Content: "sys", Changed: false},
	}
	first := collectStablePrefixSlots(blocks)
	for i := 0; i < 5; i++ {
		got := collectStablePrefixSlots(blocks)
		if !reflect.DeepEqual(got, first) {
			t.Errorf("iter %d: non-deterministic output: got %v want %v", i, got, first)
		}
	}
}
