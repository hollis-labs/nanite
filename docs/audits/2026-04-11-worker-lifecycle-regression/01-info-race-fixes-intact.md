# [Info] Three prior race fixes verified intact

**Scope:** `internal/worker/`
**Topic:** Concurrency correctness
**Date:** 2026-04-11

## Problem

The three P0 race fixes from 2026-04-10 (`fix/worker-field-sync`, `fix/plugin-trigger-dispatch-race`, `fix/worker-shutdown-race`) needed regression verification. This finding confirms all three are intact with no regressions.

## Evidence

### Fix 1: Worker.Status mutex protection

`internal/worker/worker.go:L81-92` — `GetStatus()` acquires `RLock`, `SetStatus()` acquires `Lock`. Every post-publication read of `w.Status` in `manager.go` goes through `GetStatus()` or `SetStatus()`:

- `manager.go:124` — `w.SetStatus(StatusRunning)`
- `manager.go:150` — `w.GetStatus() != StatusCancelled`
- `manager.go:151` — `w.SetStatus(StatusFailed)`
- `manager.go:163` — `w.GetStatus() != StatusCancelled`
- `manager.go:164` — `w.SetStatus(StatusFailed)`
- `manager.go:168` — `w.GetStatus() != StatusCancelled`
- `manager.go:169` — `w.SetStatus(StatusCompleted)`
- `manager.go:184` — `w.GetStatus()` in log
- `manager.go:245` — `w.SetStatus(StatusCancelled)` in Cancel
- `manager.go:261` — `w.GetStatus()` in ActiveCount
- `manager.go:281` — `w.GetStatus()` in ReapStale
- `manager.go:320` — `w.SetStatus(StatusCancelled)` in Shutdown

Zero direct field access to `w.Status` outside the mutex-guarded accessors or the `Snapshot()` method (which holds `RLock`).

### Fix 2: Worker.SessionID and WorktreePath mutex protection

`internal/worker/worker.go:L95-120` — `Get/SetSessionID()` and `Get/SetWorktreePath()` follow the same `RWMutex` pattern. All post-publication access in `manager.go` goes through accessors:

- `manager.go:104` — `w.GetWorktreePath()`
- `manager.go:120` — `w.SetWorktreePath(wtPath)`
- `manager.go:156` — `w.SetSessionID(delegResult.WorkerSessionID)`

### Fix 3: Snapshot-based List/ReapStale

`manager.go:224-233` — `List()` returns `[]Snapshot`, not `[]*Worker`. Each `Snapshot` is captured via `w.Snapshot()` which holds `RLock` for an atomic copy. `ReapStale()` at `manager.go:273-311` also uses `w.Snapshot()` for the stale-workers slice.

`writeWorkerStatus()` at `manager.go:330-346` captures a `Snapshot` before marshaling, preventing races on the JSON-marshal path.

### Fix 4: Regression test

`manager_test.go:L272-323` — `TestListConcurrentFieldAccess` uses 4 spawners x 50 iterations with 16 concurrent readers, exercising `w.Status`, `w.SessionID`, and `w.WorktreePath` on `Snapshot` values. Under `-race`, this catches any regression.

## Impact

No impact — fixes are intact. This is a positive verification.

## Recommendation

No action needed. The test provides ongoing regression coverage.

## References

- Commit `d5da5bb` — `fix(worker): protect Worker.SessionID and WorktreePath from concurrent access`
- Commit `1343446` — `fix(worker): protect Worker.Status from concurrent access during shutdown`
- `internal/worker/worker.go:L33-50` — field documentation of the mutable/immutable split
