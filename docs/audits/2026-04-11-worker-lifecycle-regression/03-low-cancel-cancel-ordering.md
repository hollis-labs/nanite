# [Low] Cancel() calls w.cancel() before SetStatus — status window

**Scope:** `internal/worker/`
**Topic:** Concurrency correctness
**Date:** 2026-04-11

## Problem

In `Cancel()`, the context cancel fires before the status is updated to `StatusCancelled`. Between the two calls, a concurrent `SpawnFull` observing the cancellation via `ctx.Done()` could complete its `DelegateTask` path (which returns `ctx.Err()`), check `w.GetStatus() != StatusCancelled` (still true because `SetStatus` hasn't run yet), and overwrite the status to `StatusFailed` before `Cancel()` reaches `SetStatus(StatusCancelled)`. The final status then depends on scheduling order.

## Evidence

`internal/worker/manager.go:L236-254`:

```go
func (m *Manager) Cancel(workerID string) error {
    val, ok := m.workers.Load(workerID)
    // ...
    w := val.(*Worker)
    if w.cancel != nil {
        w.cancel()           // <-- ctx cancelled here
    }
    w.SetStatus(StatusCancelled) // <-- status set later
    m.writeWorkerStatus(w)
    // ...
}
```

`internal/worker/manager.go:L149-172` — `SpawnFull` checks `w.GetStatus() != StatusCancelled` before setting `StatusFailed` or `StatusCompleted`. If `cancel()` fires but `SetStatus(StatusCancelled)` hasn't run yet, SpawnFull sees `StatusRunning` and writes `StatusFailed`.

The same pattern exists in `Shutdown()` at `manager.go:L314-324` but is mitigated by the fact that `Shutdown` sets status immediately after cancel in the same `Range` callback, and no other goroutine gets a scheduling opportunity between the two calls within the same `Range` iteration.

## Impact

Low. The window is narrow and the consequence is a cosmetic status mismatch: a cancelled worker briefly reports as "failed" before being overwritten to "cancelled". The `Result` struct returned to the caller is built from the delegation error, not the Worker status, so the caller still sees the correct outcome. The 30-second deferred delete ensures the stale status is short-lived.

## Recommendation

Swap the order: set status before calling cancel, so that `SpawnFull`'s `GetStatus() != StatusCancelled` check sees the terminal state immediately.

```go
w.SetStatus(StatusCancelled)
if w.cancel != nil {
    w.cancel()
}
```

This is a one-line reorder. The status write is under the worker's mutex, so it's atomic with respect to `SpawnFull`'s `GetStatus()` checks.

## References

- `internal/worker/manager.go:L236-254` — `Cancel()`
- `internal/worker/manager.go:L149-172` — `SpawnFull` status-transition logic
- `internal/worker/manager.go:L314-324` — `Shutdown()` (same pattern, narrower window)
