# [High] MemorySource similarity ranking always fails — missing Query field

**Scope:** contextbroker / memory
**Topic:** Error Handling / Correctness
**Date:** 2026-04-11

## Problem

`source_memory.go` requests similarity ranking from the memory service, but the Nanite `memory.RecallOpts` struct does not expose a `Query` field, so the underlying Conduit `RecallInput.Query` is always empty. Conduit's `recall.go:103` returns `ErrInvalidInput` when `Query == ""` with similarity ranking. Every call to `MemorySource.Fetch` that reaches the Conduit store will fail, and the broker will log the error and drop the memory source entirely.

## Evidence

`source_memory.go` sets ranking to "similarity":

```go
// internal/contextbroker/source_memory.go:L65-L70
opts := memory.RecallOpts{
    Namespaces:    namespaces,
    Ranking:       "similarity",
    Limit:         30,
    MinConfidence: 0.4,
}
```

`memory.RecallOpts` has no Query field:

```go
// internal/memory/service.go:L30-L37
type RecallOpts struct {
    Namespaces    []string
    Ranking       string
    Limit         int
    MinConfidence float64
    Origins       []string
    Tags          []string
}
```

`memory.Service.Recall` maps to `conduitMemory.RecallInput` without setting Query:

```go
// internal/memory/service.go:L132-L137
in := conduitMemory.RecallInput{
    Namespaces: opts.Namespaces,
    Ranking:    ranking,
    Limit:      limit,
    Filters:    filters,
}
```

Conduit rejects similarity ranking without a query:

```go
// vanta-conduit/internal/memory/recall.go:L99-L104
if in.Ranking == RankingSimilarity {
    if s.embedder == nil {
        return nil, ErrEmbedderUnavailable
    }
    if in.Query == "" {
        return nil, fmt.Errorf("%w: query is required for similarity ranking", ErrInvalidInput)
    }
}
```

## Impact

Memory-based context is silently dropped on every broker fetch. The broker logs the error and continues with remaining sources (`broker.go:L162-L164`), so there is no crash, but the memory source (15% of the budget) contributes nothing. Users who stored memories expecting them to appear in context will see no memory recall.

This was enabled 2026-04-08 (per reviewer-backend context). Since then, the memory source has been non-functional in similarity mode.

## Recommendation

Two changes needed:

1. Add a `Query` field to `memory.RecallOpts` and wire it through to `conduitMemory.RecallInput.Query` in `Service.Recall`.

2. In `source_memory.go`, build a query string from the intent keywords:

```go
opts := memory.RecallOpts{
    Namespaces:    namespaces,
    Ranking:       "similarity",
    Query:         strings.Join(intent.Keywords, " "),
    Limit:         30,
    MinConfidence: 0.4,
}

// Fall back to activation ranking if no keywords available.
if len(intent.Keywords) == 0 {
    opts.Ranking = "activation"
}
```

## References

- `internal/contextbroker/source_memory.go:L65-L70` — similarity ranking request
- `internal/memory/service.go:L30-L37` — `RecallOpts` struct (no Query field)
- `internal/memory/service.go:L132-L137` — `RecallInput` mapping (no Query set)
- `vanta-conduit/internal/memory/recall.go:L99-L104` — Query validation check
- `.nanite/agents/reviewer-backend.md:L152` — "enabled 2026-04-08"
