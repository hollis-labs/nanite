# [Low] Broker fetches sources sequentially — no parallelism

**Scope:** contextbroker
**Topic:** Performance
**Date:** 2026-04-11

## Problem

`Broker.Fetch` queries each source in a sequential `for` loop:

```go
// internal/contextbroker/broker.go:L151-L169
for _, src := range b.sources {
    srcBudget := budgets[src.Name()]
    if srcBudget <= 0 {
        continue
    }

    start := time.Now()
    items, err := src.Fetch(ctx, intent, srcBudget)
    // ...
}
```

With 5 sources (2 of which make MCP network calls to Conduit and Engine), the total fetch time is the sum of all source latencies. Parallel fetching would reduce it to the maximum of any single source.

## Evidence

The code at `broker.go:L151-L169` as quoted above. Each `src.Fetch` blocks until completion before the next source is queried.

The concurrency-cancellation-sweep confirmed "contextbroker has no goroutines" — this is the reason.

## Impact

Low in practice. MCP calls typically complete in <100ms for local services. PCC and Session sources are filesystem/DB reads. Total sequential time is likely <500ms. But if Conduit or Engine are remote or slow, the sequential design multiplies latency.

No concurrency correctness concern if parallelized — each source's Fetch is independent, writes to its own slice, and the merge step (`trimToBudget`) runs after all fetches complete.

## Recommendation

Parallel fetch with a WaitGroup:

```go
type sourceResult struct {
    items []ContextItem
    name  string
    dur   time.Duration
}

results := make(chan sourceResult, len(b.sources))
for _, src := range b.sources {
    go func(s ContextSource, budget int) {
        start := time.Now()
        items, err := s.Fetch(ctx, intent, budget)
        // ... handle err ...
        results <- sourceResult{items: items, name: s.Name(), dur: time.Since(start)}
    }(src, budgets[src.Name()])
}
// collect len(b.sources) results
```

This is a performance optimization, not a correctness fix. Defer if not on the critical path.

## References

- `internal/contextbroker/broker.go:L151-L169` — sequential fetch loop
- `2026-04-11-concurrency-cancellation-sweep/index.md:L161` — "contextbroker has no goroutines"
