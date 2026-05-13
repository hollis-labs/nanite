// Package context defines the named slot substrate the Context Broker
// assembles into each dispatched LLM turn: slot identities, ordering
// (`SlotOrder`), default budgets, default compactability, and the
// `ContextWindow` that holds the per-turn slot store.
//
// Pre-launch contract per `feedback_no_compat_shims`: adding a new
// slot requires explicitly appending it to `SlotOrder`,
// `DefaultBudgets`, `DefaultCompactable`, AND (when applicable)
// `internal/llm/anthropic/cache_plan.go::stablePrefixSlotPriority`.
// There is no contiguous-walk fallback past the priority list.
//
// # Invariants
//
// Six load-bearing invariants describe what every dispatch flavor
// (chat, sync subagent, async subagent, background_agent) sees on the
// wire. Documented in INVARIANTS.md alongside this package and
// enforced by `internal/service/slot_invariants_test.go`
// (`TestSlotInvariants_AcrossDispatchTypes` +
// `TestSlotInvariants_IdenticalShapeAcrossCallerTypes` +
// deliberate-violation pins). Update INVARIANTS.md in lock-step with
// any test change.
//
// Provenance: CW-20260512-0124 (SP-20260512-0011 Wave 4).
package context
