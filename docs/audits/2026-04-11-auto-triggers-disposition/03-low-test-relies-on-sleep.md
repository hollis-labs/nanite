# [Low] Tests rely on time.Sleep for synchronization

**Scope:** internal/plugin/auto_triggers_test.go
**Topic:** Test Quality
**Date:** 2026-04-11

## Problem

All three test functions in `auto_triggers_test.go` use `time.Sleep(100 * time.Millisecond)` to wait for asynchronous event hook processing. This is a non-deterministic synchronization mechanism that can produce flaky tests under load.

## Evidence

```go
// internal/plugin/auto_triggers_test.go:46
time.Sleep(100 * time.Millisecond)

// internal/plugin/auto_triggers_test.go:95
time.Sleep(100 * time.Millisecond)

// internal/plugin/auto_triggers_test.go:131
time.Sleep(100 * time.Millisecond)
```

Three occurrences across the three test functions: `TestAutoTriggerHandler_FiresOnMatchingEvent` (line 46), `TestAutoTriggerHandler_IgnoresDisabledActions` (line 95), `TestAutoTriggerHandler_IgnoresUnrelatedEvents` (line 131).

## Impact

Under CI load or slow machines, 100ms may not be enough for the goroutine to complete the DB query + event emission chain. This produces intermittent test failures that are hard to diagnose. The reviewer-backend context (`reviewer-backend.md`) notes that the triggers.go test stub race was already fixed once (2026-04-10), indicating this area is sensitive to timing.

## Recommendation

Replace `time.Sleep` with a polling helper or channel-based synchronization:

```go
// Poll until hook fires or timeout.
deadline := time.After(2 * time.Second)
for hook.callCount == 0 {
    select {
    case <-deadline:
        t.Fatal("timed out waiting for action.triggered event")
    default:
        time.Sleep(5 * time.Millisecond)
    }
}
```

Or have the `TestEventHook` expose a channel that signals when `Handle` is called.

Note: if finding 02 is resolved by deleting `auto_triggers.go`, these tests are also deleted and this finding is moot.

## References

- `internal/plugin/auto_triggers_test.go:46,95,131` — sleep-based synchronization
- `reviewer-backend.md` — notes trigger dispatch race sensitivity
