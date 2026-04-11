# [High] `DelegateAndAggregate` spawns one goroutine per sub-task with no bounded pool and no panic recovery

**Scope:** chat-engine
**Topic:** Concurrency / Resource management
**Date:** 2026-04-10

## Problem

`DelegateAndAggregate` spawns an unbounded fan of goroutines — one per decomposed sub-task — and waits for all of them via a channel collector. The sub-task count is whatever the decomposer LLM returns, capped only by a prompt instruction ("Keep sub-tasks to 2-6 items"), which is unenforced. Each spawned goroutine calls `workers.SpawnFull`, which in turn runs a full `generateResponse`. No worker-pool limit, no `errgroup` semaphore, no panic recovery on the spawned goroutines. If one worker panics, the panic kills the whole server; if the decomposer returns a large list, resource use scales linearly with no ceiling.

## Evidence

```go
// internal/service/delegation.go:L253-L290
var results []chat.SubTaskResult
if s.workers != nil {
    // Concurrent execution via worker manager.
    type indexedResult struct {
        idx    int
        result chat.SubTaskResult
    }
    ch := make(chan indexedResult, len(decomposition.SubTasks))

    for i, st := range decomposition.SubTasks {
        go func(idx int, st chat.SubTask) {
            log.Printf("delegation: spawning worker %d/%d: %s", idx+1, len(decomposition.SubTasks), st.Title)
            wr, err := s.workers.SpawnFull(ctx, worker.SpawnRequest{
                ParentSessionID: parentSessionID,
                Title:           st.Title,
                Description:     st.Description,
                Model:           model,
            })
            r := chat.SubTaskResult{Title: st.Title}
            if err != nil {
                r.Error = err.Error()
            } else {
                r.Output = wr.Content
                if !wr.Success {
                    r.Error = wr.Error
                }
            }
            ch <- indexedResult{idx: idx, result: r}
        }(i, st)
    }

    // Collect results in order.
    results = make([]chat.SubTaskResult, len(decomposition.SubTasks))
    for range decomposition.SubTasks {
        ir := <-ch
        results[ir.idx] = ir.result
    }
}
```

The buffer on `ch` is `len(decomposition.SubTasks)` — fine for bounded collection — but the goroutine count is also `len(decomposition.SubTasks)`, unbounded from the outside.

The decomposer prompt caps sub-tasks at 6:

```go
// internal/chat/decomposer.go:L47
- Keep sub-tasks to 2-6 items. More than 6 means you should consolidate.
```

This is a soft cap enforced by the LLM. A model that ignores the instruction (a jailbroken prompt, a fine-tuned variant, or simply an unusual user request) returns as many as the output token budget allows. There is no post-LLM validation of `len(decomposition.SubTasks)` before the spawn loop.

### Panic exposure

Each spawned goroutine has no defer recover. If `s.workers.SpawnFull` panics (or any helper it calls — the worker package spawns its own chain of `generateResponse` calls, all of which lack recover per finding 02), the panic crashes the server. The parent `DelegateAndAggregate` call is nominally running inside an HTTP handler goroutine, so if `DelegateAndAggregate` itself panics it would be caught by the router's `recoverMiddleware`. But the goroutines spawned at L263-L283 are detached from the request goroutine — the same problem as finding 02.

### Related byte-slice bug

```go
// internal/service/delegation.go:L229-L233
parentTask = &task.Task{
    SessionID:   parentSessionID,
    Title:       "Orchestration: " + userMessage[:min(60, len(userMessage))],
    Description: userMessage,
}
```

`userMessage[:min(60, len(userMessage))]` is a **byte** slice. If `userMessage` is UTF-8 with multi-byte characters and byte 60 lands mid-codepoint, the resulting title contains an invalid UTF-8 sequence. SQLite will accept it, but downstream consumers (JSON marshalling, logs, frontend) may behave inconsistently. Low-impact but part of a pattern across the codebase (see the Low-severity observations finding).

### No timeout on per-sub-task worker

The outer `DelegateAndAggregate` has no per-sub-task timeout. If one worker hangs (e.g., due to the channel backpressure in finding 04), the collection loop `for range decomposition.SubTasks { ir := <-ch }` blocks indefinitely. The sequential fallback path at L291-L319 has the same property — `DelegateTask` has an internal 5-minute timeout, but if the worker never sends anything to its SSE channel, the parent loop waits for the full 5 minutes per sub-task.

## Impact

- **Unbounded fan-out.** Realistically limited by the decomposer's 6-item cap, but not technically bounded. Each sub-task spawns a full `generateResponse` inside a worker session, each of which loads tools, calls providers, and runs tool loops. Resource usage scales linearly with whatever the LLM returns. For a "first beta for developer friends" with single-tenant assumptions this is probably fine most of the time, but it is not robust.
- **Panic propagation.** Any worker panic crashes the parent server. The decomposition flow is invoked from the API (`handleDelegateAndAggregate` at `internal/api/messages.go:L92`) — a crafted message could intentionally trigger a panic path in one of the workers.
- **Hang propagation.** One stuck worker blocks the aggregation for the max timeout budget. The result is a request that eventually returns partial results or errors out after 5 minutes while holding the HTTP handler goroutine for the whole duration.
- **No metrics.** There is no counter for "workers in flight" — nothing to alert if the orchestration path spawns more than expected. Observability of resource exhaustion is zero.

Severity **High**: "goroutine leaks with unbounded growth" + "concurrency bug that causes rare but reproducible hangs".

## Recommendation

Three concrete fixes, all small:

**1. Cap the sub-task count.** After decomposition returns, enforce a hard limit before spawning:

```go
const maxSubTasks = 8
if len(decomposition.SubTasks) > maxSubTasks {
    log.Printf("delegation: decomposer returned %d sub-tasks, truncating to %d",
        len(decomposition.SubTasks), maxSubTasks)
    decomposition.SubTasks = decomposition.SubTasks[:maxSubTasks]
}
```

**2. Use a bounded semaphore or `errgroup` with a limit.** Replace the raw `go func()` fan with `golang.org/x/sync/errgroup`:

```go
g, gctx := errgroup.WithContext(ctx)
g.SetLimit(4)  // max 4 concurrent workers
results := make([]chat.SubTaskResult, len(decomposition.SubTasks))

for i, st := range decomposition.SubTasks {
    i, st := i, st
    g.Go(func() error {
        defer func() {
            if r := recover(); r != nil {
                results[i] = chat.SubTaskResult{
                    Title: st.Title,
                    Error: fmt.Sprintf("worker panic: %v", r),
                }
            }
        }()
        wr, err := s.workers.SpawnFull(gctx, worker.SpawnRequest{...})
        ...
        results[i] = r
        return nil
    })
}
if err := g.Wait(); err != nil {
    return nil, err
}
```

This gives bounded concurrency, per-goroutine recover, and context propagation with cancellation. `x/sync/errgroup` is already in many Go projects' dep trees; check `go.mod` first to avoid a new dep if the project is dep-averse.

**3. Add a per-sub-task timeout.** Wrap the `gctx` with `context.WithTimeout(gctx, subTaskTimeout)` inside the goroutine. A stuck worker no longer holds the orchestration indefinitely.

**4. Fix the byte-slice.** Use `truncate.Runes(userMessage, 60)` or the existing `chat.TruncateStr` (which has the same byte-slice bug — see the Low-severity observations finding for the systemic fix). At minimum:

```go
title := "Orchestration: "
if n := len(userMessage); n > 0 {
    runes := []rune(userMessage)
    if len(runes) > 60 {
        title += string(runes[:60])
    } else {
        title += userMessage
    }
}
```

## References

- `internal/service/delegation.go:L253-L290` — unbounded goroutine spawn
- `internal/service/delegation.go:L229-L233` — byte-slice title truncation
- `internal/chat/decomposer.go:L47` — the unenforced "2-6 items" instruction
- Finding 02 (this audit) — the panic-recovery gap that compounds this issue
- Finding 04 (this audit) — the channel-backpressure goroutine leak that can hang a worker
- `worker-lifecycle-regression` queued audit (INDEX.md §8) — the other side of this coin; verify `workers.SpawnFull` itself has safe concurrent spawn semantics
