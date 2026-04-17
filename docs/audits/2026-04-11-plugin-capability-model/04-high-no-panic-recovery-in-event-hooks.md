# [High] No panic recovery in event hook dispatch

**Scope:** Plugin capability model
**Topic:** Error handling — blast radius
**Date:** 2026-04-11

## Problem

`Host.EmitEvent` dispatches event hooks in goroutines but does not wrap them in `recover()`. A panic in any event hook propagates to the goroutine boundary and crashes the entire process.

## Evidence

`internal/plugin/host.go:L1080-1100`:

```go
func (h *Host) EmitEvent(event plugin.Event) {
    h.mu.RLock()
    hooks := h.eventHooks[event.Type]
    h.mu.RUnlock()

    if len(hooks) > 0 {
        var wg sync.WaitGroup
        for _, hook := range hooks {
            wg.Add(1)
            go func(hook plugin.EventHook) {
                defer wg.Done()
                ctx, cancel := context.WithTimeout(h.ctx, 5*time.Second)
                defer cancel()

                if err := hook.Handle(ctx, event); err != nil {
                    h.logger.Error("event hook failed", "eventType", event.Type, "error", err)
                }
            }(hook)
        }
```

The goroutine has `defer wg.Done()` but no `defer recover()`. If `hook.Handle` panics, the `wg.Done()` runs (defers are LIFO) but the panic propagates and kills the goroutine, which in Go means the entire process crashes.

Similarly, `EmitPreHook` at `events.go:L552-592` runs hooks synchronously with no recover:

```go
for _, hook := range hooks {
    ctx, cancel := context.WithTimeout(h.ctx, 5*time.Second)
    err := hook.Handle(ctx, event)
    cancel()
```

A panic here crashes the calling goroutine (typically the HTTP handler or chat engine goroutine).

## Impact

- Any plugin that registers an event hook and has a nil-pointer, index-out-of-bounds, or any other panic-inducing bug in its handler kills the entire nanite process.
- With 35+ event types and hooks from multiple plugins, the surface area is large.
- A malicious plugin can trivially crash the host by registering a hook that panics.

## Recommendation

Add `defer func() { if r := recover(); r != nil { ... } }()` in both `EmitEvent` goroutines and `EmitPreHook`'s synchronous loop. Log the panic with the plugin ID and event type. For `EmitPreHook`, treat a panic as a non-cancellation (allow the action to proceed) to prevent a malicious plugin from blocking operations via panic.

```go
go func(hook plugin.EventHook) {
    defer wg.Done()
    defer func() {
        if r := recover(); r != nil {
            h.logger.Error("event hook panicked", "eventType", event.Type, "panic", r)
        }
    }()
    // ...
}(hook)
```

## References

- `internal/plugin/host.go:L1080-1100` — EmitEvent
- `internal/plugin/events.go:L552-592` — EmitPreHook
- Prior audit: `concurrency-cancellation-sweep` — panic recovery gaps noted as systemic
- Known issue BLG-20260410-004: "Plugin host per-plugin event hook cleanup (P3)" — adjacent but not the same issue
