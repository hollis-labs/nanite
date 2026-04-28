package context

import (
	"crypto/sha256"
	"fmt"
)

// Slot names. Ordered by assembly priority.
const (
	SlotSystem      = "system"
	SlotMemory      = "memory"
	SlotAgent       = "agent"
	SlotRules       = "rules"
	SlotTools       = "tools"
	SlotSession     = "session"
	SlotContext     = "context"     // dynamic enrichment (plugins, context broker)
	SlotUserContext = "user_context" // J10 (CW-20260426-0008): user-authored session context prompt.
	// Not compactable — survives compaction like SlotAgent/SlotRules.
	// Populated from sessions.context_prompt. Composes with HandoffStash
	// (CW-20260420-0024) — both are pinned slots that survive compaction.
	// J11 (CW-20260426-0009) pin tool will also use this same pattern.
	SlotConversation = "conversation" // messages — subject to compaction
)

// SlotOrder defines the assembly order. Slots are composed into the
// provider payload in this sequence. Earlier slots are cached more
// aggressively (they change less often).
var SlotOrder = []string{
	SlotSystem,
	SlotMemory,
	SlotAgent,
	SlotRules,
	SlotTools,
	SlotSession,
	SlotContext,
	SlotUserContext,
	SlotConversation,
}

// Slot holds the content, budget, and cache state for a single named
// region of the context window. Slots are the unit of caching,
// compaction, and budget accounting.
type Slot struct {
	Name        string
	Content     string
	TokenCount  int
	CacheKey    string // SHA-256 hex of Content
	Priority    int    // compaction priority: lower = keep longer
	MaxTokens   int    // budget ceiling (0 = dynamic)
	Compactable bool   // false = pipeline never modifies this slot (system, agent, rules)
	Flags       SlotFlags
}

// DefaultCompactable returns the default compactability per slot. System,
// Agent, Rules, and UserContext survive compaction so the model never loses
// identity, instructions, policy, or the user's session-scoped context mid-conversation.
func DefaultCompactable() map[string]bool {
	return map[string]bool{
		SlotSystem:       false,
		SlotMemory:       true,
		SlotAgent:        false,
		SlotRules:        false,
		SlotTools:        true,
		SlotSession:      true,
		SlotContext:      true,
		SlotUserContext:  false, // J10: pinned — survives compaction.
		SlotConversation: true,
	}
}

// SlotFlags carry per-slot signals consumed by the compaction and
// assembly pipelines.
type SlotFlags struct {
	UsingTools       bool // tool call/result blocks present in conversation span
	EnrichmentActive bool // context slot has dynamic content
	Stale            bool // content needs refresh before next assembly
}

// SlotBlock is the output unit from ContextWindow.Assemble(). Provider
// adapters translate these into API-specific payloads and apply cache
// markers where supported.
type SlotBlock struct {
	SlotName string
	Content  string
	CacheKey string
	Changed  bool // true when CacheKey differs from previous turn
}

// ComputeCacheKey returns the SHA-256 hex digest of content.
func ComputeCacheKey(content string) string {
	h := sha256.Sum256([]byte(content))
	return fmt.Sprintf("%x", h)
}

// DefaultBudgets returns the default per-slot token ceilings.
// A ceiling of 0 means "dynamic — allocated from remaining budget".
func DefaultBudgets() map[string]int {
	return map[string]int{
		SlotSystem:       2000,
		SlotMemory:       2000,
		SlotAgent:        1000,
		SlotRules:        500,
		SlotTools:        0,    // proportional to selected tool count
		SlotSession:      1000,
		SlotContext:      0,    // dynamic
		SlotUserContext:  2000, // J10: user context prompt; thin by design.
		SlotConversation: 0,    // gets remainder
	}
}
