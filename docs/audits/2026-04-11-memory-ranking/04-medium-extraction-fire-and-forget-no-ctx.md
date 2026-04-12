# [Medium] Memory extraction goroutines use background context -- no cancellation or lifecycle tracking

**Scope:** memory / extraction
**Topic:** Concurrency / Memory & Resources
**Date:** 2026-04-11

## Problem

Both `extractPerTurn` and `extractPostCompact` are launched as fire-and-forget goroutines with `go h.extractor.extractPerTurn(...)`. Inside, they create their own `context.Background()` with a timeout, but the parent goroutine has no way to cancel them or wait for completion. On shutdown, these goroutines may outlive the process and either: (a) leak if the LLM call hangs past the timeout, or (b) attempt to write to a closed Conduit store.

## Evidence

Fire-and-forget goroutine spawn in `perTurnHook.Handle`:

```go
// internal/memory/extraction.go:L105
go h.extractor.extractPerTurn(sessionID, content)
```

And in `postCompactHook.Handle`:

```go
// internal/memory/extraction.go:L136 (inferred from pattern)
go h.extractor.extractPostCompact(sessionID, tokensSaved)
```

Both extraction methods create their own timeout contexts:

```go
// internal/memory/extraction.go:L156-L157
ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
defer cancel()
```

```go
// internal/memory/extraction.go:L233-L234
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()
```

After the LLM call, they write to the store using `context.Background()`:

```go
// internal/memory/extraction.go:L223
if err := e.service.Store(context.Background(), m); err != nil {
```

## Impact

During shutdown, if the LLM call is in-flight, the goroutine blocks for up to 15/30 seconds. The subsequent `Store()` call uses a fresh `context.Background()` that ignores shutdown, potentially writing to a store that is being closed. In practice this is a graceful-shutdown issue (Medium) rather than a correctness issue, because the timeout provides a bounded lifetime. The contextbroker audit (finding 05) flagged the same pattern from the broker side.

## Recommendation

Accept the parent context in the hook and derive child contexts from it:

```go
func (h *perTurnHook) Handle(ctx context.Context, event pluginsdk.Event) error {
    // ...
    go h.extractor.extractPerTurn(ctx, sessionID, content)
    return nil
}

func (e *Extractor) extractPerTurn(parent context.Context, sessionID, content string) {
    ctx, cancel := context.WithTimeout(parent, 15*time.Second)
    defer cancel()
    // ...
    if err := e.service.Store(ctx, m); err != nil {
```

This ensures shutdown cancellation propagates to in-flight extraction goroutines.

## References

- `internal/memory/extraction.go:L105` -- fire-and-forget goroutine
- `internal/memory/extraction.go:L156-L157` -- background context with timeout
- `internal/memory/extraction.go:L223` -- store with background context
- Cross-audit: `docs/audits/2026-04-11-contextbroker/05-medium-extraction-fire-and-forget-goroutines.md`
