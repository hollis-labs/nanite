# [Medium] MemorySource ignores Conduit recall scores -- uses confidence as relevance proxy

**Scope:** contextbroker / memory integration
**Topic:** Correctness
**Date:** 2026-04-11

## Problem

`source_memory.go` converts recalled memories to `ContextItem` objects and sets `Relevance` based on the memory's `Confidence` field (a static author-assigned value), ignoring the `RecallResult.Score` computed by Conduit's ranking system. This means the context broker's cross-source relevance sorting uses a static confidence score instead of the dynamic ranking score (which incorporates activation level, recency, origin weight, access frequency, or similarity distance).

## Evidence

`source_memory.go` builds relevance from confidence:

```go
// internal/contextbroker/source_memory.go:L95-L101
relevance := m.Confidence
if relevance == 0 {
    relevance = 0.6
}
// Boost user and feedback memories slightly.
if m.Origin == "feedback" || m.Origin == "user" {
    relevance = min(relevance+0.1, 1.0)
}
```

The Conduit `RecallResult` includes a `Score` field that reflects the actual ranking computation:

```go
// vanta-conduit/internal/memory/recall.go:L59-L63
type RecallResult struct {
    Revision Revision
    Score    float64
    State    State
}
```

But `Service.Recall` only returns `[]Memory`, stripping the score:

```go
// internal/memory/service.go:L144-L148
memories := make([]Memory, 0, len(results))
for _, r := range results {
    memories = append(memories, revisionToMemory(r.Revision))
}
return memories, nil
```

The `Score` field is never propagated through the Nanite memory layer.

## Impact

When the context broker has items from multiple sources and sorts them by `Relevance` to trim to budget, memory items compete using static confidence (typically 0.7-0.9 for all memories) rather than dynamic scores. A recently-accessed, highly-activated memory scores the same as a stale one with identical confidence. This defeats the purpose of activation ranking.

When similarity ranking is fixed, this problem will be worse: highly relevant memories (cosine similarity 0.95) and barely relevant ones (cosine similarity 0.2) would both appear with the same `Relevance` based only on their confidence field.

## Recommendation

1. Extend `memory.Memory` to include a `Score` field:

```go
type Memory struct {
    // ... existing fields ...
    Score float64 `json:"score,omitempty"` // ranking score from recall
}
```

2. Propagate it from `RecallResult`:

```go
func revisionToMemory(rev conduitMemory.Revision) Memory { ... }

// New helper:
func recallResultToMemory(r conduitMemory.RecallResult) Memory {
    m := revisionToMemory(r.Revision)
    m.Score = r.Score
    return m
}
```

3. Use `Score` as the primary relevance signal in `source_memory.go`, with confidence as a tiebreaker or minimum floor.

## References

- `internal/contextbroker/source_memory.go:L95-L101` -- relevance from confidence
- `internal/memory/service.go:L144-L148` -- score stripped in conversion
- `vanta-conduit/internal/memory/recall.go:L59-L63` -- RecallResult.Score
- `internal/contextbroker/broker.go:L172-L175` -- cross-source relevance sort
