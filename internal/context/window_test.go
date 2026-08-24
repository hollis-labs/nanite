package context

import (
	"strings"
	"testing"
)

func TestNewContextWindow_defaults(t *testing.T) {
	cw := NewContextWindow(0, nil)
	if cw.TotalBudget != int(float64(DefaultContextWindowSize)*BudgetFraction) {
		t.Errorf("expected budget %d, got %d",
			int(float64(DefaultContextWindowSize)*BudgetFraction), cw.TotalBudget)
	}
	// All slots should exist.
	for _, name := range SlotOrder {
		if cw.Slot(name) == nil {
			t.Errorf("slot %q missing", name)
		}
	}
}

func TestNewContextWindow_customSize(t *testing.T) {
	cw := NewContextWindow(100_000, nil)
	want := int(float64(100_000) * BudgetFraction)
	if cw.TotalBudget != want {
		t.Errorf("expected budget %d, got %d", want, cw.TotalBudget)
	}
}

func TestSetContent_updatesTokensAndHash(t *testing.T) {
	cw := NewContextWindow(100_000, nil)
	cw.SetContent(SlotSystem, "You are a helpful assistant.")

	s := cw.Slot(SlotSystem)
	if s.TokenCount == 0 {
		t.Error("expected non-zero token count")
	}
	if s.CacheKey == "" {
		t.Error("expected non-empty cache key")
	}
	if s.Content != "You are a helpful assistant." {
		t.Errorf("content mismatch: %q", s.Content)
	}
}

func TestAssemble_ordering(t *testing.T) {
	cw := NewContextWindow(100_000, nil)
	cw.SetContent(SlotSystem, "system prompt")
	cw.SetContent(SlotAgent, "agent instructions")
	cw.SetContent(SlotConversation, "user: hello")

	blocks := cw.Assemble()
	if len(blocks) != 3 {
		t.Fatalf("expected 3 blocks, got %d", len(blocks))
	}
	// Order: system, agent, conversation.
	if blocks[0].SlotName != SlotSystem {
		t.Errorf("first block should be system, got %s", blocks[0].SlotName)
	}
	if blocks[1].SlotName != SlotAgent {
		t.Errorf("second block should be agent, got %s", blocks[1].SlotName)
	}
	if blocks[2].SlotName != SlotConversation {
		t.Errorf("third block should be conversation, got %s", blocks[2].SlotName)
	}
}

func TestAssemble_emptySlots_skipped(t *testing.T) {
	cw := NewContextWindow(100_000, nil)
	cw.SetContent(SlotSystem, "system")
	// All others empty.

	blocks := cw.Assemble()
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
}

func TestAssemble_cacheHits(t *testing.T) {
	cw := NewContextWindow(100_000, nil)
	cw.SetContent(SlotSystem, "system prompt")
	cw.SetContent(SlotConversation, "turn 1")

	// First assemble — all changed.
	blocks := cw.Assemble()
	for _, b := range blocks {
		if !b.Changed {
			t.Errorf("slot %s should be changed on first assemble", b.SlotName)
		}
	}

	// Second assemble — system unchanged, conversation changed.
	cw.SetContent(SlotConversation, "turn 2")
	blocks = cw.Assemble()
	for _, b := range blocks {
		switch b.SlotName {
		case SlotSystem:
			if b.Changed {
				t.Error("system slot should be unchanged (cache hit)")
			}
		case SlotConversation:
			if !b.Changed {
				t.Error("conversation slot should be changed")
			}
		}
	}

	if !cw.CacheHits[SlotSystem] {
		t.Error("system should be a cache hit")
	}
}

func TestConversationBudget(t *testing.T) {
	cw := NewContextWindow(100_000, nil)
	// Set static slots.
	cw.SetContent(SlotSystem, strings.Repeat("x", 4000)) // ~1000 tokens
	cw.SetContent(SlotAgent, strings.Repeat("x", 2000))  // ~500 tokens
	cw.SetContent(SlotRules, strings.Repeat("x", 1000))  // ~250 tokens

	budget := cw.ConversationBudget()
	staticUsed := cw.Slot(SlotSystem).TokenCount +
		cw.Slot(SlotAgent).TokenCount +
		cw.Slot(SlotRules).TokenCount
	want := cw.TotalBudget - staticUsed
	if budget != want {
		t.Errorf("conversation budget: got %d, want %d", budget, want)
	}
}

func TestNeedsCompaction(t *testing.T) {
	// Tiny window to force compaction.
	cw := NewContextWindow(1000, nil)                          // budget = 800 tokens
	cw.SetContent(SlotSystem, strings.Repeat("x", 400))        // ~100 tokens
	cw.SetContent(SlotConversation, strings.Repeat("x", 4000)) // ~1000 tokens

	if !cw.NeedsCompaction() {
		t.Error("should need compaction: conversation exceeds remaining budget")
	}
}

func TestTruncateSlot(t *testing.T) {
	cw := NewContextWindow(100_000, nil)
	// System slot has 2000 token ceiling → 8000 bytes.
	bigContent := strings.Repeat("x", 20000) // ~5000 tokens, way over
	cw.SetContent(SlotSystem, bigContent)

	blocks := cw.Assemble()
	for _, b := range blocks {
		if b.SlotName == SlotSystem {
			if !strings.HasSuffix(b.Content, "[truncated]") {
				t.Error("expected truncation marker")
			}
		}
	}
}

func TestComputeCacheKey_deterministic(t *testing.T) {
	k1 := ComputeCacheKey("hello world")
	k2 := ComputeCacheKey("hello world")
	if k1 != k2 {
		t.Error("same input should produce same key")
	}
	k3 := ComputeCacheKey("hello world!")
	if k1 == k3 {
		t.Error("different input should produce different key")
	}
}

func TestDefaultEstimator(t *testing.T) {
	e := DefaultEstimator{}
	if e.Estimate("") != 0 {
		t.Error("empty string should be 0 tokens")
	}
	if e.Estimate("hi") != 1 {
		t.Error("short string should be at least 1 token")
	}
	if e.Estimate(strings.Repeat("x", 400)) != 100 {
		t.Errorf("400 chars should be 100 tokens, got %d", e.Estimate(strings.Repeat("x", 400)))
	}
}
