# [High] No hybrid ranking or graceful fallback -- similarity failure silently drops all memory context

**Scope:** memory / contextbroker
**Topic:** Error Handling
**Date:** 2026-04-11

## Problem

`source_memory.go` hardcodes `Ranking: "similarity"` with no fallback. When similarity ranking fails (currently always -- see findings 01 and 02), the error propagates to the broker, which logs it and drops the memory source entirely. There is no degradation path to activation ranking, and no hybrid combination of similarity + activation scores.

## Evidence

`source_memory.go` unconditionally requests similarity:

```go
// internal/contextbroker/source_memory.go:L65-L70
opts := memory.RecallOpts{
    Namespaces:    namespaces,
    Ranking:       "similarity",
    Limit:         30,
    MinConfidence: 0.4,
}
```

When the recall fails, the broker catches but discards:

```go
// internal/contextbroker/broker.go:L161-L164
if err != nil {
    log.Printf("contextbroker: source %s error: %v", src.Name(), err)
    continue
}
```

The Conduit recall implementation supports three ranking modes independently (`activation`, `chronological`, `similarity`) but has no built-in hybrid mode. There is no rank-fusion, weighted-sum, or fallback logic anywhere in the codebase.

## Impact

Memory is a 15% budget allocation in the context broker. When similarity fails, that entire 15% is wasted -- no memories are surfaced at all. Even when similarity is eventually fixed, memories that lack embeddings (newly written, or before the queue is wired) will be invisible to similarity ranking. A fallback to activation ranking would surface these memories based on recency, access frequency, and origin weight.

## Recommendation

Implement a two-tier recall strategy in `source_memory.go`:

```go
func (s *MemorySource) Fetch(ctx context.Context, intent Intent, budget int) ([]ContextItem, error) {
    // Try similarity if keywords available.
    if len(intent.Keywords) > 0 {
        simOpts := memory.RecallOpts{
            Namespaces:    namespaces,
            Ranking:       "similarity",
            Query:         strings.Join(intent.Keywords, " "),
            Limit:         20,
            MinConfidence: 0.4,
        }
        simResults, simErr := s.Memory.Recall(ctx, simOpts)
        if simErr == nil && len(simResults) > 0 {
            // Supplement with activation-ranked to catch unembedded memories.
            actOpts := memory.RecallOpts{
                Namespaces:    namespaces,
                Ranking:       "activation",
                Limit:         10,
                MinConfidence: 0.4,
            }
            actResults, _ := s.Memory.Recall(ctx, actOpts)
            return s.merge(simResults, actResults, budget), nil
        }
        // Fall through to activation if similarity returned nothing.
    }

    // Fallback: activation ranking (always works).
    actOpts := memory.RecallOpts{
        Namespaces:    namespaces,
        Ranking:       "activation",
        Limit:         30,
        MinConfidence: 0.4,
    }
    return s.recallToItems(ctx, actOpts, budget)
}
```

This ensures memory context is always populated, with the best results when embeddings are available and a reasonable default otherwise.

## References

- `internal/contextbroker/source_memory.go:L65-L70` -- hardcoded similarity
- `internal/contextbroker/broker.go:L161-L164` -- error discard
- `internal/contextbroker/broker.go:L40-L47` -- memory budget 15%
- `vanta-conduit/internal/memory/recall.go:L147-L158` -- score switch (no hybrid)
