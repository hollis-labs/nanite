# [Medium] PTY and subprocess bridge channel sends block on slow consumer

**Scope:** PTY bridge, subprocess bridge
**Topic:** Concurrency / Backpressure
**Date:** 2026-04-11

## Problem

Both `PTYBridge.streamCLI()` and `SubprocessBridge.streamCLI()` write parsed events to a buffered channel (`make(chan StreamEvent, 64)`) with a blocking send (`ch <- ev`). When the consumer (the SSE handler or `generateResponse`) is slower than the producer (the CLI subprocess), the channel fills up and the producer goroutine blocks. While blocked, the CLI subprocess's stdout buffer fills, and eventually the CLI itself blocks on its write. This is "accidental backpressure" -- it works, but the behavior is implicit and the failure mode is a cascading stall.

## Evidence

`pkg/provider/pty.go:L125-L161`:

```go
ch := make(chan StreamEvent, 64)
// ...
go func() {
    // ...
    for scanner.Scan() {
        // ...
        for _, ev := range events {
            ch <- ev   // blocking send — no select with ctx.Done()
        }
    }
    // ...
}()
```

`pkg/provider/subprocess.go:L108-L147` has the identical pattern.

The context check is done via a `select { case <-ctx.Done(): ... default: }` block before parsing each line, but the channel send itself is not wrapped in a select. If the channel is full, the goroutine blocks on `ch <- ev` indefinitely, ignoring context cancellation until the send completes.

## Impact

- **Latent context-cancellation delay.** If a user cancels a PTY session while the channel is full, the producer goroutine does not notice until the consumer drains one slot. In practice, the 64-slot buffer means this is a brief delay (the consumer is an HTTP handler that reads as fast as it can flush), but under pathological conditions (e.g., a stalled SSE connection where TCP window is full), the producer can block for the full HTTP timeout.

- **No explicit backpressure signal.** The CLI subprocess has no way to know nanite is slow. The backpressure is purely via OS pipe buffering and Go channel blocking. This is adequate for correctness but means there is no "slow consumer" metric or adaptive behavior.

This is Medium rather than High because: (a) the 64-slot buffer handles normal burst patterns, (b) the consumer (SSE handler) reads eagerly with Flush(), (c) the blocking behavior is ultimately correct (no data loss), and (d) the `EventReactionPipeline.monitorStream` adds another 64-slot buffered channel in the path, doubling the buffer to 128 events before blocking.

## Recommendation

Wrap the channel send in a select with `ctx.Done()`:

```go
for _, ev := range events {
    select {
    case ch <- ev:
    case <-ctx.Done():
        ch <- StreamEvent{Type: "error", Error: "context cancelled"}
        p.killProcess(cmd)
        return
    }
}
```

This ensures context cancellation is honored even when the channel is full. Both `pty.go` and `subprocess.go` need this change.

## References

- `pkg/provider/pty.go:L125-L161`
- `pkg/provider/subprocess.go:L108-L147`
- `pkg/provider/event_pipeline.go:L142-L174` (monitorStream adds a second 64-slot buffer)
