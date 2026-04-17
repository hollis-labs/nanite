# [Info] Immutable field discipline is sound

**Scope:** `internal/worker/`
**Topic:** Concurrency correctness
**Date:** 2026-04-11

## Problem

No problem. This is a praise finding. The Worker struct documentation and implementation correctly distinguish between mutable fields (Status, SessionID, WorktreePath) and immutable fields (ID, Type, ParentSessionID, TaskID, AgentID, CreatedAt, cancel).

## Evidence

`internal/worker/worker.go:L33-50` documents which fields are mutable vs. immutable. The immutable fields are set once during construction at `manager.go:L79-88` and `m.workers.Store(workerID, w)` at line 89 publishes the worker. After publication:

- `w.TaskID` is read directly at `manager.go:249` in `Cancel()` — safe because TaskID is immutable.
- `w.cancel` is read at `manager.go:242-243` in `Cancel()` and `manager.go:318-319` in `Shutdown()` — safe because `cancel` is set once before publication.
- `w.ID` is read in `ReapStale` at `manager.go:286` — safe because ID is immutable.

No code outside the `worker` package references `worker.Worker` directly (confirmed via grep). External packages interact only through `*worker.Manager` and receive `worker.Snapshot` or `*worker.Result` values.

## Impact

The clean mutable/immutable split reduces the surface area for future race introductions. Maintainers adding new fields to Worker should follow the documented pattern: if the field is mutated after publication, add Get/Set accessors and update `Snapshot()`.

## Recommendation

No action needed. Consider adding a lint comment or `go vet` analyzer if the team grows, to enforce "no direct access to mutable fields" mechanically.

## References

- `internal/worker/worker.go:L33-50` — field documentation
- `internal/worker/manager.go:L79-89` — construction before publication
