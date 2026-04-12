# [Low] Activation ranking formula observations -- correct but with edge-case behaviors

**Scope:** memory / ranking
**Topic:** Correctness
**Date:** 2026-04-11

## Problem

The activation ranking formula is functionally correct but has several edge-case behaviors worth documenting for maintainers.

## Evidence

### 8a. Initial activation is always 1.0

New memories start with `activation = 1.0`:

```go
// vanta-conduit/internal/memory/write.go:L247-L249
_, err := tx.ExecContext(ctx, `
INSERT INTO memory_state (memory_id, namespace, memory_key, activation, access_count)
VALUES (?, ?, ?, 1.0, 0)`,
```

This means a brand-new memory has the same activation as one that has been accessed once. The `access_count` starts at 0 and is only incremented on recall. Initial activation should arguably differ from reinforced activation.

### 8b. Decay runs globally, not per-memory elapsed time

`applyActivationDecay` scans all `memory_state` rows and applies exponential decay relative to `last_accessed_at` (or `created_at`). This is correct in that it's idempotent and relative, but the decay is applied on a 1-hour tick:

```go
// vanta-conduit/internal/memory/decay.go:L28-L29
if j.Interval <= 0 {
    j.Interval = 1 * time.Hour
}
```

Between decay ticks, a memory's effective activation is stale. A memory accessed 59 minutes ago has the same activation as one accessed 1 second ago until the next tick. This is acceptable for the current use case but creates a "stepped" rather than continuous decay curve.

### 8c. Reinforcement formula approaches but never reaches 2.0

The reinforcement formula `new = old + 0.1 * (2.0 - old)` converges asymptotically to 2.0:

```go
// vanta-conduit/internal/memory/activation.go:L24-L27
UPDATE memory_state
SET activation = activation + 0.1 * (2.0 - activation),
    access_count = access_count + 1,
    last_accessed_at = ?
WHERE memory_id = ?
```

After 100 recalls, activation reaches ~1.99 (confirmed by `TestReinforcementDiminishingReturns`). This is good design -- bounded growth prevents runaway activation.

### 8d. Recency factor has a sharp cliff at day 0 vs day 1

```go
// vanta-conduit/internal/memory/ranking.go:L20-L32
func recencyFactor(lastAccessed *time.Time, now time.Time) float64 {
    if lastAccessed == nil {
        return 0.75
    }
    days := now.Sub(*lastAccessed).Hours() / 24
    if days <= 0 {
        return 1.0
    }
    if days >= 30 {
        return 0.5
    }
    return 1.0 - 0.5*(days/30)
}
```

The linear decay from 1.0 to 0.5 over 30 days is reasonable, but the nil-access default of 0.75 means a never-accessed memory ranks between a 15-day-old access and a fresh access. This seems intentional (benefit of the doubt for unaccessed memories) but could surprise maintainers.

## Impact

These are design observations, not bugs. The activation ranking system is well-structured with appropriate bounds (floor at 0.05, ceiling approaching 2.0, 14-day half-life decay, diminishing reinforcement). Test coverage is good (`TestReinforceAccessIncrementsActivation`, `TestReinforcementDiminishingReturns`).

## Recommendation

No changes required. Document these behaviors in a package-level comment on `ranking.go` so future maintainers understand the design choices.

## References

- `vanta-conduit/internal/memory/ranking.go` -- full scoring formula
- `vanta-conduit/internal/memory/activation.go:L24-L27` -- reinforcement
- `vanta-conduit/internal/memory/decay.go:L56-L66` -- exponential decay
- `vanta-conduit/internal/memory/write.go:L247-L249` -- initial activation
- `vanta-conduit/internal/memory/activation_test.go` -- reinforcement tests
