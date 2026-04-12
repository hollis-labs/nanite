# [Medium] Trigger dispatch spawns unbounded goroutines per event

**Scope:** Plugin event emission
**Topic:** Concurrency / Memory & Resources
**Date:** 2026-04-11

## Problem

`TriggerDispatcher.Dispatch()` spawns one goroutine per matching trigger rule with no concurrency limit. `Host.EmitEvent()` calls `go h.triggers.Dispatch(event)` fire-and-forget. Each dispatched goroutine runs `sendWithRetry`, which can sleep up to 30s * 3 retries = 90s per attempt. Under a burst of events with many matching rules, goroutine count grows linearly and unboundedly.

## Evidence

`internal/plugin/triggers.go:L44-L82`:

```go
func (td *TriggerDispatcher) Dispatch(event pluginsdk.Event) {
    // ...
    for _, rule := range rules {
        // ...
        go td.sendWithRetry(connector, payload, rule.ID)
    }
}
```

`internal/plugin/host.go:L1104-L1106`:

```go
if h.triggers != nil {
    go h.triggers.Dispatch(event)
}
```

No semaphore, no worker pool, no goroutine budget. Each `sendWithRetry` goroutine lives for up to 90 seconds (3 retries * 30s timeout each, plus backoff sleeps).

## Impact

Under normal operation, trigger rules are few (0-5 per event type) and events fire at human-interaction rate, so the goroutine count stays low. The concern is pathological scenarios:

- A plugin registers 100 trigger rules for `message.received`.
- An automated workflow fires 50 messages in rapid succession.
- Result: 100 * 50 = 5,000 goroutines, each potentially sleeping for 90s.

Goroutines are cheap in Go (~4KB stack), so 5,000 goroutines is ~20MB. Not an OOM risk by itself, but each goroutine holds a 30s HTTP timeout context, and the connector's `Send` may hold network connections. Under failure conditions (connector down), all goroutines back off and retry, maintaining pressure.

This is Medium rather than High because: (a) realistic trigger rule counts are small, (b) the `host.ctx` cancellation propagates to `sendWithRetry` and stops retries on shutdown, and (c) goroutines are lightweight in Go.

## Recommendation

Add a semaphore to limit concurrent dispatch goroutines:

```go
type TriggerDispatcher struct {
    // ...
    sem chan struct{} // concurrency limiter
}

func NewTriggerDispatcher(host *Host) *TriggerDispatcher {
    return &TriggerDispatcher{
        // ...
        sem: make(chan struct{}, 32), // max 32 concurrent dispatches
    }
}

func (td *TriggerDispatcher) Dispatch(event pluginsdk.Event) {
    // ...
    for _, rule := range rules {
        // ...
        td.sem <- struct{}{}
        go func() {
            defer func() { <-td.sem }()
            td.sendWithRetry(connector, payload, rule.ID)
        }()
    }
}
```

## References

- `internal/plugin/triggers.go:L44-L82`
- `internal/plugin/host.go:L1080-L1110`
