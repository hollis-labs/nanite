# [High] Event hooks cannot be cleaned up on plugin unload

**Scope:** Plugin capability model
**Topic:** Security — stale handler execution
**Date:** 2026-04-11

## Problem

When a plugin is unloaded, `UnloadPlugin` attempts to clean up event hooks but cannot identify which hooks belong to which plugin because `plugin.EventHook` has no `PluginID()` method. The cleanup code explicitly acknowledges this:

```go
// EventHook interface doesn't expose plugin ID, so we can't selectively
// remove per-plugin hooks here without extending the interface. This is
// a known limitation — tracked for future cleanup.
```

As a result, all event hooks from unloaded plugins remain registered and continue to execute.

## Evidence

`internal/plugin/host.go:L1042-1052`:

```go
// Remove event hooks owned by this plugin.
for eventType, hooks := range h.eventHooks {
    filtered := hooks[:0]
    for _, hook := range hooks {
        // EventHook interface doesn't expose plugin ID, so we can't selectively
        // remove per-plugin hooks here without extending the interface. This is
        // a known limitation — tracked for future cleanup.
        filtered = append(filtered, hook)
    }
    h.eventHooks[eventType] = filtered
}
```

The loop rebuilds the slice with ALL hooks — it removes nothing.

## Impact

1. Unloaded plugins' event hooks continue to fire on every matching event.
2. If the unloaded plugin's hook references state that was cleaned up during `Unload()` (freed resources, closed connections, nil pointers), the hook will panic — and per finding 04, there is no panic recovery.
3. A malicious plugin that registers persistent hooks cannot be cleanly removed without restarting the process.
4. Hooks accumulate if a plugin is loaded/unloaded/reloaded — each load cycle adds new hooks without removing old ones.

## Recommendation

Extend the `EventHook` interface with a `PluginID() string` method, or wrap hooks in a struct that tracks the registering plugin. Then filter by plugin ID during unload.

Alternatively, use the same pattern as `FilterRegistry.RemoveByPlugin(id)` which already works correctly for filters.

## References

- `internal/plugin/host.go:L1042-1052` — broken cleanup
- `internal/plugin/host.go:L1039` — `filters.RemoveByPlugin(id)` — working filter cleanup (contrasting pattern)
- Known issue BLG-20260410-004: "Plugin host per-plugin event hook cleanup (P3)"
