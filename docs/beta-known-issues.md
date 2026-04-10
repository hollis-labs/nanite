# Beta Known Issues

> **Purpose:** single tracking list of known issues that must be resolved (or explicitly deferred) before the beta release to the first user group. Not a roadmap, not a polish list — **bugs and concrete defects only**.
>
> **Out of scope:** `docs/vnext-backlog.md` (post-MVP roadmap), `docs/frontend-punchlist.md` (UI polish), `.nanite/agents/plugin-dev.md` §Known Limitations (plugin framework design choices). Those are tracked elsewhere and handled by the respective agents.
>
> **Post-beta items** are filed to the Engine backlog under `project_id: nanite` and removed from this list once captured. Don't duplicate them here — query `engine_backlog_list --project-id nanite` for the full deferred list.

## Status legend

- ✅ **Done** — fix committed and verified; waiting on merge or already merged
- 🔄 **In progress** — active fix branch, not yet complete
- 🟡 **Needs investigation** — root cause or scope unclear
- 🔴 **Not started**

Last updated: 2026-04-10 (afternoon — all P0 items resolved; P0-4 closed by documentation)

---

## P0 — Ship blockers

### 1. `TestShutdown` data race in `internal/worker` — ✅ Done (merged)

Race between `Manager.Shutdown()` writing `w.Status = StatusCancelled` and the in-flight `SpawnFull` goroutine writing terminal status values (`StatusFailed` / `StatusCompleted`). Both wrote `Worker.Status` without synchronization. Reproducible via `go test -race -run TestShutdown ./internal/worker/...`.

**Fix:** Added `sync.RWMutex` to `Worker` with `GetStatus()`/`SetStatus()` accessors; routed all in-package reads/writes through them; added a "don't clobber `StatusCancelled`" guard in `SpawnFull`'s terminal transitions.

**Merged:** `1343446` on `main` (2026-04-10). Landed together with #3 below via the combined branch `fix/worker-field-sync` (now deleted).

**Files:** `internal/worker/worker.go`, `internal/worker/manager.go`, `internal/worker/manager_test.go`

---

### 2. `TestTriggerDispatch_EmptyTemplate` data race in `internal/plugin` — ✅ Done (merged)

`TriggerDispatcher.Dispatch` is intentionally fire-and-forget (comment at `triggers.go:43`). The test stub `testConnector` wrote `lastPayload` before incrementing the `sendCount` atomic, so the atomic provided zero happens-before for the payload field. Same latent race in `TestTriggerDispatch_PayloadTemplate`.

**Fix:** Test-stub only — added `sync.Mutex` guarding `lastPayload`, flipped the write ordering so the mutex release happens after the atomic increment, added a `getLastPayload()` helper. Production `triggers.go` untouched.

**Merged:** `48bf67e` on `main` (2026-04-10). Branch `fix/plugin-trigger-dispatch-race` deleted.

**Files:** `internal/plugin/triggers_test.go` (+32 / -7)

**Note:** `TestTriggerDispatch_DisabledRule` and `TestTriggerDispatch_FilterExpr` check `sendCount == 0` without waiting. Currently safe because filter/disabled checks are synchronous in `Dispatch` before the goroutine spawn, but brittle if that ever changes. Not fixed — flagged for future-proofing.

---

### 3. Concurrent access to `Worker.SessionID` / `Worker.WorktreePath` — ✅ Done (merged)

Companion issue to #1. Nanite supports concurrent full-worker spawns today, and `Manager.List()` copies `*Worker` pointers out of `m.workers` while `SpawnFull` was still writing `SessionID` and `WorktreePath` after publication. No reproducible test failed on unmodified `main` beforehand, but the race was real under concurrent load — a new `TestListConcurrentFieldAccess` (4 spawners × 4 readers × 50 iterations, 20ms delegator delay) reproduces it on the unfixed tree and flags races on `Status`, `SessionID`, and `WorktreePath` simultaneously.

**Fix:** Extended the `sync.RWMutex` added in #1 to also guard `SessionID` and `WorktreePath`. Introduced a `Snapshot` value type + `(*Worker).Snapshot()` method capturing all fields under the lock. Changed `Manager.List()` from `[]*Worker` to `[]Snapshot` and `ReapStale()` similarly — this moves the synchronization boundary inside the Manager so nothing downstream (including reflection-based JSON marshaling in `internal/api/workers.go`) can reach raw `*Worker` fields unsynchronized. The only external caller (`internal/api/workers.go:13`) was unaffected because the new return type is JSON-compatible.

**Merged:** `d5da5bb` on `main` (2026-04-10). Branch `fix/worker-field-sync` deleted.

**Repo-wide verification:** `go test -race ./...` on `main` at `48bf67e` — all packages pass, no data races detected anywhere in the tree.

---

### 4. Dev-mode launches compiled user binary instead of dev build — ✅ Done (documentation)

Engine backlog: [BLG-20260312-018](https://engine-local/backlog/BLG-20260312-018) (P2, tags: `bug`, `cerberus`, `wails`, filed 2026-03-12)

**Original summary:** when running Nanite in dev mode via Cerberus, the launcher invokes `~/go/bin/nanite` instead of the local dev build at `./nanite`, causing "my fix isn't taking effect" confusion.

**Investigation (2026-04-10):** The bug as framed does not exist in the current architecture:

1. The `wails` tag on the backlog item is **stale**. `grep -ri wails` across `.go` files returns zero matches. Backlog item predates the current React SPA + `go:embed` setup.
2. The `-dev` flag (`cmd/nanite/main.go:85`) is purely a runtime flag on the binary: when set, `internal/server/spa.go:19` serves a placeholder HTML instead of the embedded UI so Vite can run HMR on a separate port. It does **not** change which binary is running or where it lives.
3. Cerberus `nanite-api` launches `~/go/bin/nanite` by design. Verified 2026-04-10 by `lsof` on the running PID — the `txt` mapping resolves to `/Users/chrispian/go/bin/nanite`, not the project-root `./nanite`.
4. The *actual* footgun is a developer muscle-memory issue: running `go build` writes `./nanite` in the project root (which Cerberus never looks at), then restarting the service leaves stale code running. `cerberus_rebuild nanite-api` is the only deploy path that both rebuilds and restarts.

**Resolution:** Documentation only, per the original acceptance criteria. Added a dedicated "Two binaries, only one is live" subsection under §Build & Run in `.nanite/agents/backend.md` with a table showing which commands produce which binary, a `lsof` recipe for verifying the live executable, and an explicit note that `-dev` is unrelated to the binary-location question. The old one-line note in the §Notes footer now cross-references the new subsection instead of duplicating the content. `CLAUDE.md` at repo root already has a compact version of the same warning under "Build & Test → Deploying changes" and does not need to change.

**Follow-up:** Update `BLG-20260312-018` in the Engine backlog to strip the stale `wails` tag and mark it closed-by-documentation, pointing at the new `.nanite/agents/backend.md` §Build & Run subsection. Not blocking for beta.

**Files:** `.nanite/agents/backend.md` (new subsection + footer fix)

---

## P1 — Should fix

*None currently.* Promote items here from investigation or deferred categories if beta-user impact is discovered.

---

## Deferred to Engine backlog (post-beta)

The following have been filed to Engine and are **not tracked in this document**. Query Engine for current status:

| ID | Title | Priority |
|---|---|---|
| [BLG-20260410-001](engine-local) | PTY tool-level presence — `tool_pending`/`tool_resolved` won't fire for PTY adapters | P3 |
| [BLG-20260410-002](engine-local) | Install service: implement `--dry-run` mode | P3 |
| [BLG-20260410-003](engine-local) | Provider error recovery: handle `prompt_too_long` during generation | P2 |
| [BLG-20260410-004](engine-local) | Plugin host: per-plugin event hook cleanup on unload | P3 |

---

## How to use this document

- When a P0 item lands, change its status to ✅ and link the merge commit.
- When an item is discovered during beta triage, add it here FIRST if it's a bug blocking release. If it's a feature, polish, or design-choice change, file it to Engine backlog instead — do not add it to this document.
- Close this document out after the beta release ships. Remaining items either move to Engine or are obsoleted by the release.

## Related documents

- `.nanite/agents/backend.md` — backend agent context, including "Beta Known Issues" pointer and `plugin-dev.md` §Known Limitations checklist
- `.nanite/agents/frontend.md` — frontend agent context, including frontend punch list and antipatterns checklist
- `docs/vnext-backlog.md` — post-MVP roadmap (not tracked here)
- `docs/frontend-punchlist.md` — UI polish list (tracked by frontend agent)
