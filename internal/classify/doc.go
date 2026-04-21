// Package classify provides the P3 ScopeTier + ExecutionPattern primitive
// for the Agent Platform (EP-20260419-0001, Arc 4 — Strategy & Orchestration).
//
// # What it does
//
// A single call site — service.generateResponse, pre-loop — feeds an
// IntentSignals struct into Classify and receives two orthogonal classifiers:
//
//   - ScopeTier   ∈ {Trivial, Small, Medium, Large, Open} — "how big"
//   - ExecutionPattern ∈ {Inline, Subagent, Background}   — "how to run"
//
// The pair is stored on loopState (internal/service) for the duration of the
// generation; consumers read it via loopState.Classification().
//
// # Consumer contract
//
// Downstream tickets consume this pair and map it to their own concern:
//
//   - CW-20260419-0020 (budgets)         — ScopeTier → Anthropic task_budget.tokens.
//                                          TierOpen ⇒ advisory only; workspace-policy
//                                          hard_ceiling wins.
//   - CW-20260419-0026 (strategy loop)   — both axes gate strategy-loop entry.
//   - CW-20260419-0027 (playbook)        — both axes inform playbook selection.
//   - CW-20260420-0016 (BackgroundJob)   — PatternBackground gates detached dispatch.
//
// Consumers MUST NOT assume:
//   - A "Mode" value is derived from ScopeTier. It is not. Mode (CW-20260420-0014,
//     §6 of the design doc) is an orthogonal concept; P3 neither produces nor
//     consumes it.
//   - The classification is reliable for per-turn decisions. It is pre-loop only
//     (D2) and may be stale if a generation pivots scope mid-flight.
//   - A confidence score is available. The return is two values only (D6).
//     Low-confidence manifests as the conservative default (TierSmall, PatternInline).
//
// # Consumer-side validation
//
// loopState.Classification() returns (TierInvalid, PatternInvalid) when
// Classify has not yet run for the current generation. Consumers that feed
// these values into metrics, budget math, or dispatch logic MUST check
// ScopeTier.IsValid() and ExecutionPattern.IsValid() before trusting them —
// an "invalid" string escaping into a log or budget computation is the most
// likely consumer-side bug shape.
//
// # Rubric stability
//
// The keyword sets in classify.go are exported (ScopeTierOpenKeywords, etc.) so
// downstream eval work (CW-20260420-0028, 7.1 eval suite) can tune the rubric
// without rewriting the classifier. Keyword matching is case-insensitive and
// substring-based. The tier/pattern enums are stable; the rubric is not.
//
// # Reachability matrix
//
// Of the 5 × 3 = 15 possible (ScopeTier, ExecutionPattern) combinations, 13
// are reachable under the MVP rubric. The two unreachable combinations are:
//
//   - TierLarge × PatternInline    — large work always routes Subagent or Background
//   - TierOpen  × PatternInline    — open work always routes Subagent or Background
//
// This is intentional: inline dispatch at those sizes cuts the agent off
// mid-thought. Consumers should not plan for those shapes. If a future product
// direction argues for inline dispatch at those tiers, loosen the tier-driven
// Subagent escalation via an ADR.
//
// # What this package does NOT do
//
//   - It does not enforce budgets (that's CW-20260419-0020).
//   - It does not dispatch background jobs (that's CW-20260420-0016).
//   - It does not produce a Mode (that's CW-20260420-0014, §6 DISCUSS-FIRST).
//   - It does not call an LLM (D5 — rules-based MVP; LLM-judge deferred).
//
// # Design references
//
//   - Design doc: planning/agent-platform/2026-04-19-initial-decomposition.md (§4.4, §6)
//   - Plan:       planning/agent-platform/2026-04-19-p3-scopetier-plan.md
//   - Ticket:     CW-20260420-0013
//   - Research:   Anthropic task-budgets-2026-03-13; multi-agent research scaling.
package classify
