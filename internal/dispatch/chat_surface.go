package dispatch

// ChatToolSurface defines the tool catalog the chat-role agent sees.
//
// The filter applies ONLY to the chat-role profile (slug "default"). Other
// profiles — Worker, Planner, executor sessions, hint-selector, mux-orchestrator,
// etc. — bypass this filter entirely and use the unfiltered tool catalog.
//
// Phase 2 graduation per executor-handoff design (CW-20260429-0030 / B1):
//
//	The four lens primitives — tool_describe, tool_validate, lesson_capture,
//	card_show — are excluded from the chat surface. The chat agent reaches
//	those flows by dispatching to a registered executor (B3 pilot:
//	internal/executor/envelope_render). The executor's profile retains the
//	lens primitives because that's where the multi-step recovery loop lives.
//
// This is surface partitioning (chat does X, executor does Y), NOT surface
// gating in the c114/c117 sense. Trust model is unchanged: agents are trusted
// (decisions.nanite.permission.trust_agent_model — H1); capability layers gate.
// See B1 §"Decision rules pass" for the 8/8 rule check.
//
// History: A previous symbol called dispatch.ChatSurfaceWorkerOnlyTools (a
// 4-entry deny-list of subagent-spawn primitives) was emptied and removed
// in chat_surface_v2 (Phase 3 Stage 1, 2026-05-02). That symbol is gone;
// this is a different cluster (lens primitives) with a separate symbol.
// See `decisions.nanite.architecture.chat_surface_v2` for the prior arc.
type ChatToolSurface struct {
	// ExcludedTools names tools that DO NOT reach the chat agent. Lookup
	// is by uniform agent-facing tool name (post-rename — no nanite_*
	// prefix; rename arc landed 2026-05-08).
	ExcludedTools map[string]struct{}

	// IncludedAdditions names tools that ARE on the chat surface even if
	// they would otherwise be filtered out by some upstream layer. Reserved
	// for future use; empty by default. (Today the dispatch_executor self-tool
	// is not yet wired — B2 dependency — so no overrides are needed.)
	IncludedAdditions map[string]struct{}
}

// DefaultChatToolSurface returns the canonical Phase 2 chat surface filter:
// the four lens primitives are excluded; nothing is force-included.
//
// The exclusion list is intentionally small and mirrors B1 §3 ("Lens
// placement") + B6's measurement reduction (~7,467 chars / ~1,867 tokens
// removed from the chat agent's per-turn prefix, as measured at B6 —
// card_show's own description has since been trimmed (CW-20260918-0042),
// so the live number is smaller; the exclusion itself is unaffected).
func DefaultChatToolSurface() *ChatToolSurface {
	return &ChatToolSurface{
		ExcludedTools: map[string]struct{}{
			"tool_describe":  {},
			"tool_validate":  {},
			"lesson_capture": {},
			"card_show":      {},
		},
		IncludedAdditions: map[string]struct{}{},
	}
}

// Filter reports whether toolName is on the chat surface. Returns true
// when the tool is allowed (the caller should keep it); false when the
// tool is excluded (the caller should drop it).
//
// Resolution order:
//  1. IncludedAdditions wins — if present, the tool is on the surface
//     even when ExcludedTools also lists it. (Today the two maps are
//     disjoint, but the override semantics are documented so future
//     callers can rely on them.)
//  2. ExcludedTools — present means dropped.
//  3. Default — tool is on the surface.
//
// Nil-safe: a nil receiver is treated as "no filtering" (every tool
// passes through). Callers building a one-off surface can pass nil to
// disable the filter without a conditional.
func (s *ChatToolSurface) Filter(toolName string) bool {
	if s == nil {
		return true
	}
	if _, included := s.IncludedAdditions[toolName]; included {
		return true
	}
	if _, excluded := s.ExcludedTools[toolName]; excluded {
		return false
	}
	return true
}
