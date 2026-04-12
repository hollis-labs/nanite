# [Critical] Memory embeddings are never generated -- NoopQueue silently discards all embed jobs

**Scope:** memory / embedding pipeline
**Topic:** Correctness
**Date:** 2026-04-11

## Problem

When a memory revision is written, the Conduit memory store enqueues an `embed` job to generate and persist the embedding vector asynchronously. However, Nanite opens its embedded Conduit instance without passing a `queue` option (`conduit.WithQueue`), so Conduit falls back to `NoopQueue` -- a stub that silently discards every job. No memory revision ever gets an embedding vector, which means similarity ranking returns zero results for every query.

## Evidence

Nanite opens Conduit without a queue:

```go
// internal/service/container.go:L273-L283
var conduitOpts []conduit.Option
if embedder != nil {
    conduitOpts = append(conduitOpts, conduit.WithEmbedder(embedder))
    conduitOpts = append(conduitOpts, conduit.WithEmbeddingModel(embeddingModel))
}
conduitOpts = append(conduitOpts, conduit.WithLogger(log.Printf))

conduitInstance, conduitErr = conduit.Open(context.Background(), conduit.Config{
    RootDir: conduitRoot,
}, conduitOpts...)
```

No `conduit.WithQueue(...)` is passed. Grep for `WithQueue`, `NoopQueue`, `QueueAdapter`, and `go-queue` in the Nanite codebase returns zero hits.

Conduit defaults to `NoopQueue` when no queue is provided:

```go
// vanta-conduit/conduit.go:L103-L106
var jobQueue memory.JobQueue = memory.NoopQueue{}
if o.queue != nil {
    jobQueue = memory.NewQueueAdapter(o.queue, "conduit")
}
```

`NoopQueue` drops every job:

```go
// vanta-conduit/internal/memory/queue.go:L29
func (NoopQueue) Enqueue(_ context.Context, _ Job) error { return nil }
```

The write path enqueues an embed job after every write:

```go
// vanta-conduit/internal/memory/write.go:L195-L198
_ = s.queue.Enqueue(ctx, Job{
    Kind:    "embed",
    Payload: []byte(fmt.Sprintf(`{"revision_id":%q}`, revisionID)),
})
```

The embed handler that would process this job exists (`vanta-conduit/embed_handler.go`) and calls `memStore.EmbedRevision()`, but with `NoopQueue`, it is never invoked.

## Impact

Every memory revision stored through Nanite has a `NULL` embedding vector. When similarity ranking is attempted, `recall.go:L169-L177` filters out all revisions without embeddings, returning an empty result set. This makes similarity-based memory recall completely non-functional.

This is the root cause of the cross-audit finding from `contextbroker` audit 01 (`01-high-similarity-ranking-always-fails.md`): even if the missing `Query` field were fixed (that audit's recommendation), similarity ranking would still return zero results because no revision has an embedding to compare against.

The chain of failures:
1. NoopQueue discards embed jobs (this finding)
2. No embeddings exist in the database
3. `source_memory.go` requests similarity ranking but doesn't pass a Query (contextbroker audit 01)
4. Conduit rejects the query for missing Query field
5. Even if Query were provided, zero results would return because all revisions lack embeddings

## Recommendation

Wire a real queue into the Conduit instance. The `go-queue` library is already a dependency of `vanta-conduit`:

```go
import (
    queue "github.com/hollis-labs/go-queue"
    memdriver "github.com/hollis-labs/go-queue/driver/memory"
)

// In container.go, before conduit.Open:
queueDriver := memdriver.New()
conduitOpts = append(conduitOpts, conduit.WithQueue(queue.New(queueDriver)))
```

This starts Conduit's built-in worker goroutine (`conduit.go:L118-L128`) which processes embed jobs using the `embed_handler.go` handler. The in-memory driver is sufficient for single-process Nanite; persistence across restarts is not required since embeddings are idempotent and can be backfilled.

Alternative: call `EmbedRevision` synchronously in the Nanite memory service's `Store()` method. This is simpler but adds latency to every memory write (one embedding API call per write).

## References

- `internal/service/container.go:L273-L283` -- Conduit init, no WithQueue
- `vanta-conduit/conduit.go:L103-L106` -- NoopQueue fallback
- `vanta-conduit/internal/memory/queue.go:L29` -- NoopQueue.Enqueue discards
- `vanta-conduit/internal/memory/write.go:L195-L198` -- embed job enqueue
- `vanta-conduit/embed_handler.go` -- the handler that never fires
- Cross-audit: `docs/audits/2026-04-11-contextbroker/01-high-similarity-ranking-always-fails.md`
