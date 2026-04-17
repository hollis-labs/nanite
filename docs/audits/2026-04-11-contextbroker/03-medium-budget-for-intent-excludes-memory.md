# [Medium] BudgetForIntent excludes memory source — zero budget if ever wired

**Scope:** contextbroker
**Topic:** Correctness / Latent Bug
**Date:** 2026-04-11

## Problem

`IntentSourcePriority` in `intent.go` lists only 4 sources per intent type: `conduit`, `pcc`, `engine`, and `session`. The `memory` source is absent from every priority list. `BudgetForIntent` distributes weights 0.40/0.30/0.20/0.10 across these 4 sources, totaling 1.0. When `allocateBudgets` processes the result, the memory source falls into the "unweighted" path and receives `remaining / unweighted` = `0 / 1` = 0 tokens.

Currently harmless: production wiring in `container.go:L342` uses `DefaultBudget()` (which correctly includes all 5 sources). But `BudgetForIntent` is an exported function designed for intent-aware budget tuning, and if any caller switches to it, the memory source silently gets zero budget.

## Evidence

```go
// internal/contextbroker/intent.go:L40-L49
var IntentSourcePriority = map[string][]string{
    IntentResumeTask:     {"engine", "conduit", "session", "pcc"},
    IntentBootProject:    {"pcc", "conduit", "engine", "session"},
    IntentReviewSession:  {"session", "conduit", "pcc", "engine"},
    IntentWriteCode:      {"pcc", "conduit", "session", "engine"},
    IntentDebugIssue:     {"conduit", "pcc", "session", "engine"},
    IntentPlanFeature:    {"engine", "conduit", "pcc", "session"},
    IntentRecallDecision: {"conduit", "pcc", "session", "engine"},
    IntentCustom:         {"conduit", "pcc", "engine", "session"},
}
```

No entry in any list contains `"memory"`.

```go
// internal/contextbroker/intent.go:L64-L72
weights := []float64{0.40, 0.30, 0.20, 0.10}
sourceWeights := make(map[string]float64)
for i, src := range priorities {
    if i < len(weights) {
        sourceWeights[src] = weights[i]
    } else {
        sourceWeights[src] = 0.05
    }
}
```

Sum of weights = 1.0, leaving 0 remaining for unweighted sources.

## Impact

Latent. If `BudgetForIntent` is used in a future refactor (e.g., to enable intent-aware budget tuning as the function's purpose suggests), memory recall silently receives zero tokens and never contributes context. Combined with finding 01 (similarity ranking already broken), this doubles down on memory being invisible.

## Recommendation

Add `"memory"` to each `IntentSourcePriority` list and expand the weights array to 5 entries:

```go
IntentResumeTask: {"engine", "conduit", "memory", "session", "pcc"},
// ... (adjust per intent)
```

Or restructure `BudgetForIntent` to include all registered source names rather than a hardcoded list.

## References

- `internal/contextbroker/intent.go:L40-L49` — `IntentSourcePriority` (no "memory")
- `internal/contextbroker/intent.go:L64-L72` — weight distribution
- `internal/contextbroker/broker.go:L189-L223` — `allocateBudgets` unweighted path
- `internal/service/container.go:L342` — production uses `DefaultBudget()` (safe)
