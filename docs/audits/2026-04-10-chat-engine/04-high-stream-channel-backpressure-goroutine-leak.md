# [High] `generateResponse` goroutine can block on `ch <-` when the SSE client disconnects

**Scope:** chat-engine
**Topic:** Concurrency / Memory & Resources
**Date:** 2026-04-10

## Problem

Every `ch <- chat.StreamEvent{...}` inside `generateResponse` is a blocking send on a 128-buffer channel. If the SSE client disconnects (browser closes, network drops), the consumer goroutine in `handleStream` exits on `ctx.Done()` and stops draining the channel. Sends from the production side then block until the buffer drains — which it never will — or until the chat-level context times out at 5 minutes. While blocked, the goroutine holds its context, its provider stream, its tool loop state, and any in-flight tool subprocesses.

Worse: the blocking send is not context-aware. A blocked `ch <- event` ignores `ctx.Done()`. The 5-minute timeout only fires when the next explicit `ctx.Err()` check runs after the block clears, not while the block is active.

## Evidence

The stream channel is created with a fixed buffer of 128 and returned to the SSE consumer:

```go
// internal/service/stream.go:L42-L47
func (sm *StreamManager) CreateStream(messageID, sessionID string) chan chat.StreamEvent {
    ch := make(chan chat.StreamEvent, 128)
    sm.streams.Store(messageID, ch)
    sm.msgToSession.Store(messageID, sessionID)
    return ch
}
```

The consumer in `handleStream` exits on `ctx.Done()` and does not drain the channel before returning:

```go
// internal/api/messages.go:L172-L192
ctx := r.Context()
for {
    select {
    case <-ctx.Done():
        return                            // ← leaves without draining ch
    case <-sseDone:
        ...
        return                            // ← same, takeover path
    case evt, ok := <-ch:
        if !ok {
            return
        }
        data, _ := json.Marshal(evt)
        fmt.Fprintf(w, "event: %s\ndata: %s\n\n", evt.Type, data)
        flusher.Flush()
    }
}
```

The producer uses plain blocking sends everywhere — no `select { case ch <- evt: case <-ctx.Done(): return }` guard:

```go
// internal/service/chat_generate.go — representative blocking sends
// L207   ch <- chat.StreamEvent{Type: "stream_start", ...}
// L244   ch <- chat.StreamEvent{Type: "status", Content: fmt.Sprintf("Stopped: %s", reason)}
// L250   ch <- chat.ErrorEnvelopeDelta(...)
// L391   ch <- chat.StreamEvent{Type: "delta", Content: evt.Content}
// L437   ch <- chat.ErrorEnvelopeDelta(chat.ClassifyError(fmt.Errorf("%s", evt.Error)), ...)
// L594   ch <- chat.StreamEvent{Type: "delta", Content: envelopeBlock}
// L743   ch <- chat.StreamEvent{Type: "stream_end", ...}
```

Same in `chat_tool_executor.go` (L97-L98, L145, L167, L198-L199, L298, L430, L448) and in the progressive-tools helper `handleRequestTools` (L782, L790, L820). None of them guard the send.

The chat-level timeout is wall-clock only and only helps if the loop advances to an explicit `ctx.Err()` check:

```go
// internal/service/chat_generate.go:L49-L51
ctx, cancel := context.WithTimeout(ctx, generateResponseTimeout)
defer cancel()
```

```go
// internal/service/chat_generate.go:L247-L259
// Deadline check.
if ctx.Err() != nil {
    log.Printf("[WARN] generateResponse context cancelled: %v (session=%s)", ctx.Err(), sessionID)
    ...
    return
}
```

This check only runs at the top of each loop iteration. If the goroutine is stuck in `ch <- event` on iteration 3 of a tool-use loop, the check never fires; the deadline triggers the cancel function but the blocked send doesn't see it.

### Scenario

1. User sends a message that produces ~200 delta events (a long LLM response) across several tool iterations.
2. User closes the browser tab halfway through. `handleStream` hits `ctx.Done()` and returns. `ch` is no longer drained.
3. `generateResponse` continues streaming. After 128 unread events, the next `ch <- evt.Content` inside the provider loop (L391) blocks.
4. The provider's upstream stream (`provCh`) also stops being drained — the `for evt := range provCh` loop at L379 can't advance. Whatever goroutine produces `provCh` is now blocked sending to it.
5. The tool loop can't reach L247 to check `ctx.Err()`. Nothing unblocks except the 5-minute timeout — which only takes effect when the blocked send finishes, which it won't.
6. The goroutine, the provider stream, and any in-flight tool subprocesses all hold their resources for the full 5 minutes or forever.

### Additional complication — presence broadcast

`BroadcastPresence` is non-blocking (drops on slow clients). But `chat_tool_executor.go:L292-L301` uses a `sync.Mutex` to serialize presence events during concurrent tool execution:

```go
// internal/service/chat_tool_executor.go:L254-L266
if len(concurrent) > 0 {
    var wg sync.WaitGroup
    var mu sync.Mutex // protects ch sends ordering (presence events)
    for _, ip := range concurrent {
        wg.Add(1)
        go func(ip indexedPlan) {
            defer wg.Done()
            result := s.executeSingleTool(ctx, ip.plan.tu, agentID, sessionID, ch, &mu)
            results[ip.planIdx] = result
        }(ip)
    }
    wg.Wait()
}
```

`executeSingleTool` takes `mu.Lock()` around `ch <- chat.StreamEvent{Type: "tool_call", ...}` (L292-L301 and L344-L360). If `ch` is full, the mutex is held for the duration of the block. Every other concurrent tool goroutine is stuck waiting for the mutex. The `wg.Wait()` never returns. The tool batch deadlocks.

## Impact

- Any disconnected client creates a goroutine leak for 5 minutes minimum (and longer if the tool batch deadlocks via the mutex path above).
- Each leaked goroutine holds references to the provider stream, the tool execution context, and any spawned subprocesses. On CLI providers (`pty-*`), that includes a PTY'd child process that is not being read from — likely filling its output buffer and blocking, which cascades further.
- Under load, this is a classic accumulating-resource-hog bug. Kill-all-on-shutdown (`processTracker.KillAll`) works for hard shutdown but doesn't help a running server that accrues stuck goroutines one client disconnect at a time.
- The concurrent-tool deadlock path (mutex held during blocked send) is a correctness bug, not just a leak. Every concurrent tool in the batch stalls until the blocked send clears.

The rubric: "Goroutine leaks with unbounded growth" = High. "Concurrency bug that causes rare but reproducible hangs or corrupt state" = High. This finding hits both.

## Recommendation

Make every `ch <- event` context-aware via a small helper, and drop events rather than block when the client is gone:

```go
// internal/service/chat_generate.go — helper
func sendOrDrop(ctx context.Context, ch chan chat.StreamEvent, evt chat.StreamEvent) bool {
    select {
    case ch <- evt:
        return true
    case <-ctx.Done():
        return false
    default:
        // Slow consumer. Log once and drop or block with a short timeout.
        select {
        case ch <- evt:
            return true
        case <-time.After(2 * time.Second):
            log.Printf("chat-service: dropping event for slow consumer (type=%s)", evt.Type)
            return false
        case <-ctx.Done():
            return false
        }
    }
}
```

Then replace every `ch <- evt` with `if !sendOrDrop(ctx, ch, evt) { return }` (or `continue` / `break` depending on the site). A dropped event is better than a leaked goroutine.

**Alternative / complementary:** Have `handleStream` drain the channel after `ctx.Done()` instead of returning immediately:

```go
// internal/api/messages.go — after ctx.Done() case
case <-ctx.Done():
    // Drain-and-discard so the producer can finish.
    go func() {
        for range ch {
        }
    }()
    return
```

This still leaks the drain goroutine until `generateResponse` closes `ch`, but it's bounded by `generateResponseTimeout` and doesn't hold provider/tool resources for no reason. It's a lighter-touch fix but doesn't address the mutex-deadlock in concurrent tool execution — for that, the `sendOrDrop` pattern is needed.

**Recommended:** `sendOrDrop` in the chat service plus the drain goroutine in the SSE handler. Both are small, local changes. The mutex deadlock in `executeToolBatch` is fixed as a side effect once `sendOrDrop` replaces the blocking send inside `executeSingleTool`.

## References

- `internal/service/stream.go:L42-L47` — buffer size
- `internal/api/messages.go:L172-L192` — consumer that exits without draining
- `internal/service/chat_generate.go:L207, L244, L250, L391, L437, L594, L743` — blocking sends
- `internal/service/chat_tool_executor.go:L254-L301` — concurrent tool batch with mutex-protected blocking send
- `internal/service/chat_generate.go:L49-L51` and L247-L259 — deadline check that cannot fire while a send is blocked
- Sandbox audit `docs/audits/2026-04-10-sandbox-hardening/` — not directly related, but the sandbox's PTY leaks interact with this finding: a disconnected SSE combined with a long-running PTY session means the PTY output buffer also fills and creates a further block cascade.
