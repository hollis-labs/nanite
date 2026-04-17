# [High] Plugin host_test.go missing Shutdown deadlock and panic recovery tests

**Scope:** internal/plugin/
**Topic:** Test Quality — concurrency-critical gaps
**Date:** 2026-04-11

## Problem

`internal/plugin/host_test.go` (319 lines) tests plugin loading, event hook registration, CRUD handler registration, UI component registration, service registration, and the event catalog. It does not test two known concurrency hazards: the `Host.Shutdown()` deadlock when a plugin's `Unload()` re-enters the host, and panic propagation from event hooks.

## Evidence

`internal/plugin/host_test.go` test functions:
- `TestNewHost` — constructor
- `TestSetRouter` — router setter
- `TestLoadPlugin` — load + retrieve
- `TestRegisterEventHook` — register + emit + verify call count
- `TestRegisterCRUDHandler` — register + retrieve
- `TestRegisterUIComponent` — register + retrieve
- `TestRegisterService` — register + retrieve + error for non-existent
- `TestEventCatalog` — verify event constants are non-empty

Missing tests:

1. **Shutdown deadlock:** The reviewer-backend context documents a known bug: "Host.Shutdown() holds the mutex across p.Unload() calls — any plugin Unload that re-enters the host will deadlock." No test exercises this path. A test with a plugin whose `Unload()` calls `host.GetPlugin()` or `host.EmitEvent()` would expose the deadlock under `-race` or timeout.

2. **Panic in event hooks:** `internal/plugin/events.go` has 35+ typed Emit helpers. The reviewer-backend context notes "any panic in an event hook currently propagates." No test verifies whether `EmitEvent` recovers from a panicking hook or propagates it to the caller. A test with a hook that panics would determine the current behavior and lock it in.

3. **Concurrent LoadPlugin:** No test loads multiple plugins concurrently to exercise mutex contention on the plugin map.

4. **EmitEvent with no hooks:** No test verifies EmitEvent is safe when no hooks are registered for the event type. (Likely fine, but worth a one-line test.)

The `events_test.go` file is only 34 lines and tests `NormalizeEventType` — it does not test event emission behavior at all.

## Impact

The Shutdown deadlock is a known bug with no regression test. If a fix is applied, there is no way to verify it works. If a fix regresses, there is no test to catch it. Panic propagation from event hooks can crash the entire process if an external plugin's hook panics during a chat session.

## Recommendation

1. Add `TestHost_ShutdownWithReentrantPlugin`:
```go
// Plugin whose Unload() calls host.GetPlugin()
func (p *ReentrantPlugin) Unload() error {
    p.host.GetPlugin("other")
    return nil
}
// Test: load two plugins, call Shutdown(), verify no deadlock (use timeout)
```

2. Add `TestHost_EventHookPanic`:
```go
// Hook that panics
func (h *PanicHook) Handle(ctx context.Context, event plugin.Event) error {
    panic("hook panic")
}
// Test: register hook, emit event, verify host survives
```

3. Add `TestHost_ConcurrentLoad`: load N plugins from N goroutines under `-race`.

## References

- Reviewer-backend context: "Host.Shutdown() holds the mutex across p.Unload() calls — any plugin Unload that re-enters the host will deadlock. Flag as Critical if this lands on a real re-entry path."
- Reviewer-backend context: "any panic in an event hook currently propagates"
- `2026-04-11-panic-recovery-sweep` — recover() placement gaps
