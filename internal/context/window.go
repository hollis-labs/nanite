package context

import (
	"log/slog"
	"unicode/utf8"
)

// BudgetFraction is the fraction of the provider context window used as the
// total slot budget. The remaining 20% is safety margin for the provider.
const BudgetFraction = 0.80

// DefaultContextWindowSize is the fallback context window when the provider
// doesn't report one.
const DefaultContextWindowSize = 200_000

// ContextWindow manages a set of named slots, allocates token budgets, tracks
// cache state across turns, and assembles the final payload as a pipeline of
// SlotBlocks that provider adapters can interpret.
type ContextWindow struct {
	slots     map[string]*Slot
	estimator TokenEstimator

	// TotalBudget is the usable token budget (provider window × BudgetFraction).
	TotalBudget int

	// PrevHashes stores the cache keys from the previous turn, keyed by slot name.
	PrevHashes map[string]string

	// CacheHits records which slots matched their previous cache key this turn.
	CacheHits map[string]bool
}

// NewContextWindow creates a window with the given provider context window size.
// Pass 0 to use DefaultContextWindowSize.
func NewContextWindow(providerWindowSize int, estimator TokenEstimator) *ContextWindow {
	if providerWindowSize <= 0 {
		providerWindowSize = DefaultContextWindowSize
	}
	if estimator == nil {
		estimator = DefaultEstimator{}
	}
	budget := int(float64(providerWindowSize) * BudgetFraction)

	budgets := DefaultBudgets()
	compactable := DefaultCompactable()
	slots := make(map[string]*Slot, len(SlotOrder))
	for i, name := range SlotOrder {
		slots[name] = &Slot{
			Name:        name,
			Priority:    i, // lower index = lower priority number = keep longer
			MaxTokens:   budgets[name],
			Compactable: compactable[name],
		}
	}

	return &ContextWindow{
		slots:       slots,
		estimator:   estimator,
		TotalBudget: budget,
		PrevHashes:  make(map[string]string),
		CacheHits:   make(map[string]bool),
	}
}

// SetContent updates a slot's content and recomputes its token count and cache key.
func (cw *ContextWindow) SetContent(slotName, content string) {
	s, ok := cw.slots[slotName]
	if !ok {
		slog.Warn("context: unknown slot, ignoring SetContent", "slot", slotName)
		return
	}
	s.Content = content
	s.TokenCount = cw.estimator.Estimate(content)
	s.CacheKey = ComputeCacheKey(content)
	s.Flags.Stale = false
}

// SetFlags updates the flags on a slot.
func (cw *ContextWindow) SetFlags(slotName string, flags SlotFlags) {
	if s, ok := cw.slots[slotName]; ok {
		s.Flags = flags
	}
}

// Slot returns the named slot, or nil if not found.
func (cw *ContextWindow) Slot(name string) *Slot {
	return cw.slots[name]
}

// UsedTokens returns the sum of all slot token counts.
func (cw *ContextWindow) UsedTokens() int {
	total := 0
	for _, s := range cw.slots {
		total += s.TokenCount
	}
	return total
}

// ConversationBudget returns the token budget available for the conversation
// slot after all static slots are accounted for.
func (cw *ContextWindow) ConversationBudget() int {
	staticUsed := 0
	for _, name := range SlotOrder {
		if name == SlotConversation {
			continue
		}
		s := cw.slots[name]
		staticUsed += s.TokenCount
	}
	remaining := cw.TotalBudget - staticUsed
	if remaining < 0 {
		remaining = 0
	}
	return remaining
}

// NeedsCompaction returns true when the conversation slot exceeds its budget.
func (cw *ContextWindow) NeedsCompaction() bool {
	conv := cw.slots[SlotConversation]
	if conv == nil {
		return false
	}
	return conv.TokenCount > cw.ConversationBudget()
}

// Assemble produces an ordered list of SlotBlocks. Each block carries the
// slot content, its cache key, and whether it changed since the previous turn.
// After assembly, PrevHashes is updated for the next turn.
//
// This is a pipeline step — the caller passes these blocks to the provider
// adapter which decides how to format them (e.g., system prompt blocks with
// cache_control markers for Anthropic, plain concatenation for others).
func (cw *ContextWindow) Assemble() []SlotBlock {
	cw.CacheHits = make(map[string]bool, len(SlotOrder))
	blocks := make([]SlotBlock, 0, len(SlotOrder))

	for _, name := range SlotOrder {
		s := cw.slots[name]
		if s.Content == "" && !(s.Flags.LazyLoad && s.Flags.LoadHint != "") {
			continue
		}

		// Agent instructions must survive final assembly intact, just as
		// they survive the broker's stash decision. Enforce the per-slot
		// budget ceiling for other slots. LazyLoad is a
		// pointer-sized payload by definition; ceiling is checked against
		// the underlying full content (which the consumer would have to
		// load to materialize), not the pointer.
		if name != SlotAgent && s.MaxTokens > 0 && s.TokenCount > s.MaxTokens {
			cw.truncateSlot(s)
		}

		// Glass-5 (CW-20260502-0012): when LazyLoad+LoadHint is set, ship
		// the pointer instead of the full payload. Cache key tracks the
		// rendered pointer so toggling LazyLoad invalidates the slot
		// cache cleanly.
		content, cacheKey := s.EffectiveContent()

		prev, hasPrev := cw.PrevHashes[name]
		changed := !hasPrev || prev != cacheKey
		cw.CacheHits[name] = !changed

		blocks = append(blocks, SlotBlock{
			SlotName: name,
			Content:  content,
			CacheKey: cacheKey,
			Changed:  changed,
		})
	}

	// Update PrevHashes for next turn. Track the effective (rendered)
	// cache key so a LazyLoad↔full toggle is visible to the cache layer.
	newHashes := make(map[string]string, len(cw.slots))
	for name, s := range cw.slots {
		_, cacheKey := s.EffectiveContent()
		if cacheKey != "" {
			newHashes[name] = cacheKey
		}
	}
	cw.PrevHashes = newHashes

	slog.Debug("context: assembled slot blocks",
		"blocks", len(blocks), "used_tokens", cw.UsedTokens(),
		"budget", cw.TotalBudget, "cache_hits", cw.countCacheHits())

	return blocks
}

// truncateSlot trims a slot's content to fit within its MaxTokens ceiling.
// Uses a rough byte-based truncation (4 bytes ≈ 1 token).
func (cw *ContextWindow) truncateSlot(s *Slot) {
	maxBytes := s.MaxTokens * 4
	if len(s.Content) <= maxBytes {
		return
	}
	// Walk back to a valid UTF-8 rune boundary to avoid splitting multi-byte chars.
	for maxBytes > 0 && !utf8.RuneStart(s.Content[maxBytes]) {
		maxBytes--
	}
	s.Content = s.Content[:maxBytes] + "\n[truncated]"
	s.TokenCount = cw.estimator.Estimate(s.Content)
	s.CacheKey = ComputeCacheKey(s.Content)
}

func (cw *ContextWindow) countCacheHits() int {
	n := 0
	for _, hit := range cw.CacheHits {
		if hit {
			n++
		}
	}
	return n
}
