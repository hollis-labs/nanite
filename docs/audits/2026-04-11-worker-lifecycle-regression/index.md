# Worker lifecycle regression audit — 2026-04-11

**Scope:** `internal/worker/` — guard the three race fixes from 2026-04-10 (`fix/worker-field-sync`, `fix/plugin-trigger-dispatch-race`, `fix/worker-shutdown-race`). Verify the fixes are still in place and no new races were introduced.

**Scope interpretation:** The worker package consists of three files: `manager.go`, `worker.go`, `manager_test.go`. No `snapshot.go` or `worker_test.go` exists (anchor files listed in the scope that don't exist are noted here for completeness). The package is self-contained — no code outside `internal/worker/` references the `worker.Worker` type directly; all external interaction is through `*worker.Manager` which returns `worker.Snapshot` or `*worker.Result` values. No files were sampled — all three files were read in full.

## Methodology

**Categories applied:**

- **Concurrency correctness** — primary focus. Verified every post-publication access to mutable Worker fields (Status, SessionID, WorktreePath) goes through RWMutex-guarded accessors. Verified immutable fields (ID, Type, ParentSessionID, TaskID, AgentID, CreatedAt, cancel) are set before publication and never written after. Verified all public Manager methods returning worker state return `Snapshot` values or primitive types, never `*Worker`.
- **Test quality** — verified `TestListConcurrentFieldAccess` still exercises all three mutable fields concurrently with 4 spawners x 50 iterations and 16 concurrent readers.

**Categories skipped:**

- **Security** — no trust boundary in this package. Worker requests come from internal chat delegation, not external input.
- **Memory and resource leaks** — the 30-second deferred-delete goroutine and Shutdown-without-wait are already catalogued by `concurrency-cancellation-sweep` (2026-04-11). Not re-flagged.
- **Error handling, idioms, antipatterns, standards/tooling** — deferred to broader scope passes. This is a narrow regression guard.

**Tooling deferred:** `go test -race ./internal/worker/` and `go vet ./internal/worker/` deferred to `whole-repo-tooling-and-tests-sweep` (queued item 35). This audit is a read-and-grep verification pass.

**Cross-audit preflight:** Read index files for `2026-04-11-concurrency-cancellation-sweep` and `2026-04-11-panic-recovery-sweep`. Both touch `internal/worker/manager.go`. Specific cross-referenced items:
- Concurrency sweep item 2: `worker.Manager.Shutdown` cancels without waiting (`manager.go:314`). Not re-flagged.
- Concurrency sweep item re: `manager.go:179` 30-second sleep goroutine leak. Not re-flagged.
- Panic sweep: `manager.go:179` cleanup goroutine catalogued as trivial body / low risk. Not re-flagged.

## Findings

### By severity

**Critical (0)**
- _none_

**High (0)**
- _none_

**Medium (0)**
- _none_

**Low (1)**
- [03 — Cancel() calls w.cancel() before SetStatus — status window](03-low-cancel-cancel-ordering.md)

**Info (2)**
- [01 — Three prior race fixes verified intact](01-info-race-fixes-intact.md)
- [02 — Immutable field discipline is sound](02-info-immutable-field-discipline.md)

### By topic

**Concurrency correctness**
- [01 — Three prior race fixes verified intact](01-info-race-fixes-intact.md)
- [02 — Immutable field discipline is sound](02-info-immutable-field-discipline.md)
- [03 — Cancel() calls w.cancel() before SetStatus — status window](03-low-cancel-cancel-ordering.md)

## Recommended next steps

1. Fix the Cancel/SetStatus ordering (finding 03) — one-line reorder, minimal risk.
2. Run `go test -race ./internal/worker/` to mechanically confirm the race detector is clean. Deferred from this audit to the tooling sweep.
3. If any new fields are added to `Worker` that are mutated after publication, add Get/Set accessors following the existing pattern and update `Snapshot()`.

## Known issues skipped

- **`worker.Manager.Shutdown` cancels without waiting** — catalogued in `2026-04-11-concurrency-cancellation-sweep` item 2. Not re-flagged.
- **`manager.go:179` 30-second sleep goroutine is uncancellable** — catalogued in both `concurrency-cancellation-sweep` and `panic-recovery-sweep`. Not re-flagged.
- **`manager.go:179` cleanup goroutine has no panic recovery** — catalogued in `panic-recovery-sweep`. Not re-flagged.

## Noticed but out of scope

- **`Cancel()` reads `w.cancel` without mutex at `manager.go:242`.** `cancel` is a `context.CancelFunc` set once during construction and never mutated — the immutable-field contract covers it. But `cancel` is not documented in the Snapshot struct (it's unexported + `json:"-"`), so a future maintainer might not realize it's part of the immutable set. A one-line comment on the `cancel` field saying "immutable after construction" would make this explicit. Follow-up scope: general code-quality pass on `internal/worker/`.
- **`Shutdown()` at `manager.go:314-324` iterates `workers.Range` and calls `w.cancel()` + `w.SetStatus(StatusCancelled)` for each worker. If a concurrent `SpawnFull` publishes a new worker to the sync.Map during the Range iteration, the new worker may or may not be visited** — `sync.Map.Range` does not guarantee it will see concurrent insertions. This means a spawn that races with Shutdown could survive Shutdown. This is a design choice (caller is expected to stop spawning before calling Shutdown), not a bug, but it's worth noting. Follow-up scope: `worker-manager-shutdown-completeness` if drain-before-shutdown semantics are ever needed.
