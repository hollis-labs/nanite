# [High] RecallOpts lacks Query field -- similarity ranking fails at the Nanite adapter layer

**Scope:** memory / contextbroker integration
**Topic:** Correctness
**Date:** 2026-04-11

## Problem

The Nanite `memory.RecallOpts` struct does not expose a `Query` field, so the underlying Conduit `RecallInput.Query` is always empty. Conduit requires a non-empty query for similarity ranking and returns `ErrInvalidInput` when `Query == ""` with `RankingSimilarity`. This is the Nanite-side confirmation of the contextbroker audit finding 01.

## Evidence

`RecallOpts` has no Query field:

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

`Service.Recall` maps opts to `conduitMemory.RecallInput` without setting Query:

```go
// internal/memory/service.go:L132-L137
in := conduitMemory.RecallInput{
    Namespaces: opts.Namespaces,
    Ranking:    ranking,
    Limit:      limit,
    Filters:    filters,
}
```

`source_memory.go` requests similarity ranking without any query content:

```go
// internal/contextbroker/source_memory.go:L65-L70
opts := memory.RecallOpts{
    Namespaces:    namespaces,
    Ranking:       "similarity",
    Limit:         30,
    MinConfidence: 0.4,
}
```

Conduit validates and rejects:

```go
// vanta-conduit/internal/memory/recall.go:L99-L106
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

Every `MemorySource.Fetch` call fails with `ErrInvalidInput`. The broker catches the error, logs it, and drops the memory source. Memory contributes nothing to context assembly. This is independent of (and compounding with) finding 01 -- even if embeddings existed, the query path is broken.

## Recommendation

1. Add `Query string` to `RecallOpts`:

```go
type RecallOpts struct {
    Namespaces    []string
    Ranking       string
    Query         string   // required for similarity ranking
    Limit         int
    MinConfidence float64
    Origins       []string
    Tags          []string
}
```

2. Wire it through in `Service.Recall`:

```go
in := conduitMemory.RecallInput{
    Namespaces: opts.Namespaces,
    Ranking:    ranking,
    Query:      opts.Query,
    Limit:      limit,
    Filters:    filters,
}
```

3. In `source_memory.go`, build the query from intent keywords and fall back to activation ranking when no keywords are available:

```go
query := strings.Join(intent.Keywords, " ")
ranking := "similarity"
if query == "" {
    ranking = "activation"
}
opts := memory.RecallOpts{
    Namespaces:    namespaces,
    Ranking:       ranking,
    Query:         query,
    Limit:         30,
    MinConfidence: 0.4,
}
```

## References

- `internal/memory/service.go:L30-L37` -- RecallOpts struct
- `internal/memory/service.go:L132-L137` -- RecallInput mapping
- `internal/contextbroker/source_memory.go:L65-L70` -- similarity ranking request
- `vanta-conduit/internal/memory/recall.go:L99-L106` -- query validation
- Cross-audit: `docs/audits/2026-04-11-contextbroker/01-high-similarity-ranking-always-fails.md`
