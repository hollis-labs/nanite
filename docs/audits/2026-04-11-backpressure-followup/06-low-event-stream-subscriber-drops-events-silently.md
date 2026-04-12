# [Low] Event stream subscriber drops events silently on buffer full

**Scope:** Plugin event bus / SSE event stream
**Topic:** Backpressure
**Date:** 2026-04-11

## Problem

`Host.broadcastEvent()` uses a non-blocking channel send to push events to SSE subscribers. When the subscriber's 64-slot buffer is full, the event is silently dropped. There is no metric, no counter, and no notification to the subscriber that events were lost.

## Evidence

`internal/plugin/event_stream.go:L35-L46`:

```go
func (h *Host) broadcastEvent(event pluginsdk.Event) {
    h.mu.RLock()
    defer h.mu.RUnlock()

    for _, ch := range h.eventSubs {
        select {
        case ch <- event:
        default:
            // Subscriber buffer full -- drop event to avoid blocking.
        }
    }
}
```

The `SubscribeEvents()` channel is created with a 64-slot buffer (`make(chan pluginsdk.Event, 64)`) at `event_stream.go:10`.

## Impact

A slow SSE consumer (e.g., a browser tab on a congested network, or a paused JavaScript thread) will silently miss events. For the current use case (dead endpoint with no frontend consumer, per reviewer context), this is cosmetic. If the event stream becomes a production feature, silent event loss will cause debugging confusion.

This is Low rather than Medium because: (a) the drop-on-full design is intentional and clearly commented, (b) the endpoint is currently a dead path with no consumer, and (c) the alternative (blocking) would create a worse problem (one slow client blocks all event emission).

## Recommendation

Add a dropped-event counter (per subscriber or global) that can be queried for observability. Alternatively, add a synthetic "events_dropped" event periodically to alert subscribers.

The same non-blocking-drop pattern is used in `StreamManager.BroadcastPresence()` (`internal/service/stream.go:L129-L138`), which is a production path. That site already logs the drop, which is better than the event stream's silent drop.

## References

- `internal/plugin/event_stream.go:L10-L46`
- `internal/service/stream.go:L129-L138` (presence broadcast, same pattern but with logging)
