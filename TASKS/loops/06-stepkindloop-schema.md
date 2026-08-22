# `StepKindLoop` schema — widen `workflow_run_steps.kind` CHECK

**Phase:** 1 — Schema & storage foundation (`TASKS/loops`)
**Status:** reviewed
**Depends on:** none (parallel-safe — different table/column than tasks `01`-`05`)
**Touches:** `internal/store/migrations/` (new migration), `internal/agentworkflow/types.go`
(new `StepKind` constant only — no executor logic).

## Context

Implements the schema half of `docs/engineering/architecture/21-loops.md`'s "one new, thin
step kind" — `StepKindLoop`, so a Workflow can *contain* a Loop (design doc §12: "a
workflow node can invoke a loop"). Quoted directly: *"It reuses the identical
pause/external-resolve/`Resume` plumbing `StepKindFlex` already reused from `StepKindGate`
— a `StepKindLoop` step returns a waiting status when it starts, and something external (the
contained `LoopRun` reaching a terminal state) is what calls `Resume` on the *outer*
workflow. This is not new engine behavior, just the third reuse of a pattern the engine
already has twice."*

**This task is schema-only** — the constant and the `CHECK` widening, nothing else. This
deliberately mirrors Teams' own precedent exactly: `StepKindFlex`'s kind-CHECK widening
landed in `130_workflow_run_steps_flex_kind.sql` as part of Teams' Phase 1 schema task
(`TASKS/teams/03-stepkindflex-schema.md`), separately from the *status*-CHECK widening
(`RunStatusWaitingOnFlex`, migration `133`) which landed later, together with the real
executor work (`TASKS/teams/06-stepkindflex-executor.md`). This batch's task `09` is the
`StepKindLoop` equivalent of that later task — do not add `RunStatusWaitingOnLoop` here;
that's `09`'s migration, landing alongside the actual pause/resume wiring it's needed for.

**Real precedent DDL, read in full this session** —
`internal/store/migrations/130_workflow_run_steps_flex_kind.sql` widens
`workflow_run_steps.kind`'s `CHECK` from `('llm','tool','gate')` to
`('llm','tool','gate','flex')` via SQLite's rename-recreate-copy dance
(`PRAGMA foreign_keys = OFF`, build `workflow_run_steps_new` with the new CHECK, copy rows,
drop old, rename new → old, `PRAGMA foreign_keys = ON`) — SQLite has no `ALTER TABLE ...
ALTER COLUMN` for CHECK constraints, so this rebuild is the only way to widen one.

## What to do

1. **New migration** — re-verify the actual next-available migration number at dispatch
   time; this task provisionally claims `143` (deliberately sequenced after this batch's
   other five Phase 1 migrations in the provisional order, even though it has no real
   dependency on them — keeps the batch's own migrations contiguous for readability; adjust
   if dispatch order makes a different number more natural). Copy `130`'s rebuild pattern
   exactly, widening the CHECK from `('llm','tool','gate','flex')` (current, per Teams
   having already landed) to `('llm','tool','gate','flex','loop')`. Read the real, current
   `workflow_run_steps` schema at dispatch time (`sqlite3 <db> ".schema workflow_run_steps"`
   or the latest migration that touched it) to confirm the exact current column list before
   writing the rebuild — don't copy `130`'s column list blind if a later migration (`133` or
   anything since) added/changed columns on this table.

2. **Go constant** — `internal/agentworkflow/types.go`, alongside the existing
   `StepKindGate`/`StepKindFlex` block (confirmed this session at lines 12-33): add
   `StepKindLoop StepKind = "loop"`. No new struct fields, no executor wiring — a
   `StepKindLoop` step's actual config (which `LoopDefinition`/preset to launch, how to map
   the outer workflow's params into the inner Loop's `goal_id`/overrides) is task `09`'s job,
   matching how `TASKS/teams/03-stepkindflex-schema.md` added `flexStepConfig`'s columns
   without wiring the executor either.

## Done means

- Migration applies cleanly against a real backup copy of the database, including one with
  real `flex`-kind rows already present (the rebuild must preserve them — verify by row
  count and a spot-check of at least one `flex` row's other columns before/after).
- `StepKindLoop` constant exists and compiles; nothing else references it yet (expected —
  task `09` is the first real consumer).
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.

## Work log

**Migration number.** Ran `ls internal/store/migrations/ | sort -t_ -k1 -n | tail -8` at
dispatch time in this worktree: the highest landed migration was `134_agent_profiles_protocol_transport.sql`.
Used **`139`** (`139_workflow_run_steps_loop_kind.sql`), not the task file's provisional `143` —
per the task's own instruction to trust the worktree's real next-available number over the
provisional one. `TASKS/loops/01`-`05` were not present as landed migrations in this worktree at
dispatch time (they're being claimed in a sibling worktree per the task file's own note), so no
intra-batch collision was observed from this worktree's vantage point.

**Current `workflow_run_steps` schema, confirmed before writing the migration.** Grepped
`internal/store/migrations/*.sql` for every migration touching `workflow_run_steps` shape:
`051_agent_workflows.sql` (original `CREATE TABLE`), `091_workflow_gate_input.sql` (`ALTER TABLE
ADD COLUMN gate_input`), `130_workflow_run_steps_flex_kind.sql` (kind CHECK widened to include
`'flex'`), `133_workflow_run_flex_waiting_status.sql` (status CHECK widened to include
`'waiting_on_flex'`, also rebuilding `workflow_runs`). `134_agent_profiles_protocol_transport.sql`
(the last-landed migration) only touches `agent_profiles` — confirmed it does not touch
`workflow_run_steps`. So the live column list and both CHECKs going into `139` are exactly `133`'s
rebuilt shape: `id, workflow_run_id, step_id, kind CHECK(llm|tool|gate|flex), status
CHECK(pending|running|completed|failed|waiting_on_gate|waiting_on_flex|skipped), output, is_error,
tool_calls_json, verify_json, error, started_at, completed_at, updated_at, gate_input` — verified
directly against a real backup DB copy (`sqlite3 <scratch-copy> ".schema workflow_run_steps"`),
not just the migration ledger. `139` widens only the `kind` CHECK to add `'loop'`; the `status`
CHECK is carried forward byte-for-byte unchanged, per this task's explicit "do not add
`RunStatusWaitingOnLoop`/status-CHECK widening here" scope fence (that's task `09`'s job).

**Migration** — `internal/store/migrations/139_workflow_run_steps_loop_kind.sql`, copying
`130`'s rename-recreate-copy rebuild pattern exactly (`PRAGMA foreign_keys = OFF`, build
`workflow_run_steps_new` with the widened CHECK, copy every row, drop old, rename new → old,
recreate both original indexes, `PRAGMA foreign_keys = ON`). Down migration rebuilds back to the
immediately-prior (post-133) 4-value kind CHECK, matching `130`/`133`'s own downgrade precedent of
restoring the prior state rather than the full history.

**Go constant** — added `StepKindLoop StepKind = "loop"` to `internal/agentworkflow/types.go`,
directly after the existing `StepKindFlex` block, in the same doc-comment style (citing
`docs/engineering/architecture/21-loops.md` Decision 1 verbatim, same as `StepKindFlex` cites
`15-teams.md`). No struct fields, no executor wiring, no other reference added — confirmed nothing
else in the tree references `StepKindLoop` yet (expected; task `09` is the first real consumer).

**GLOSSARY check.** `StepKindLoop` and `RunStatusWaitingOnLoop` are already named and described in
`docs/engineering/GLOSSARY.md`'s `LoopRun` entry (pre-existing from the Loops design session) —
no new-name collision to resolve; this task's constant just makes the already-documented name
real Go code.

**Testing against a real backup DB copy.** `~/.local/share/nanite/workspaces/default/backups/`
has exactly one backup on this machine
(`main.db.pre-execution-backup-20260818-132726`, dated 2026-08-18, before this session's Loops
design work). Copied it into an isolated scratch path (never opened in place), and confirmed via
direct `sqlite3 <copy> ".schema workflow_run_steps"` / `SELECT kind, count(*) ... GROUP BY kind`
that this backup **predates migration 130** — its on-disk `kind` CHECK is still the original
3-value `('llm','tool','gate')`, and it has zero `flex`-kind rows (2 real rows total: one `llm`,
one `gate`). The live `~/.local/share/nanite/workspaces/default/main.db` was also spot-checked
(copied to scratch, non-destructively) for the same reason and has the identical 2 rows, 0 flex —
`StepKindFlex` genuinely has no real callers yet anywhere on this machine, consistent with
migration 130's own doc comment ("no live callers yet").

Because the Done-means explicitly requires verifying the rebuild preserves **real flex-kind
rows already present** by row count and a spot-check, and no backup on this machine has any, the
regression test (`internal/store/migration_139_workflow_run_steps_loop_kind_test.go`,
`TestRealBackupWorkflowRunStepsSurviveLoopKindMigration`) reconstructs the actual scenario the
Done-means describes directly against the real backup copy: it opens the copy via `store.New`
(full migrate, including 139), uses goose's `Provider.DownTo(134)` to roll the schema back to
immediately before migration 139 (same technique migration `105`'s own regression test uses for
its Down half), inserts a synthetic `flex`-kind row at that schema version (legal there, per
migration 130), then `Provider.UpTo(139)` to replay migration 139's rebuild over the real
backup's 2 pre-existing rows plus this now-present flex row. Verified: exact row count
(2 real + 1 synthetic = 3) preserved, every original row's `(workflow_run_id, step_id, kind,
status)` unchanged, and the flex row's `output` column spot-checked byte-for-byte before/after
the rebuild. A `loop`-kind row insert is then demonstrated to work against the same real,
migrated backup copy. This is a deliberate adaptation of the Done-means' literal wording to what
this machine's actual backup data supports — noted here rather than silently skipped, per
EXECUTION-PROCESS.md's real-backup-testing requirement.

Two more tests added alongside it, mirroring migration `130`'s own test file structure exactly:
`TestMigrate139WidensWorkflowRunStepsKindCheck` (isolated in-memory-equivalent store; all five
kinds — `llm`/`tool`/`gate`/`flex`/`loop` — round-trip; a bogus kind is still rejected; a
re-`migrate()` no-op leaves rows intact) and `TestMigrate139PreservesWorkflowRunStepsIndexes`
(both original indexes, including the load-bearing `UNIQUE (workflow_run_id, step_id)` index,
survive the rebuild with their original definitions).

**Baseline checks.** `go build ./cmd/nanite/` passes. `go vet ./...` reports four pre-existing
findings in `internal/service/container.go` (`stopReaper`/`stopRuntimeReaper` possible context
leak) — confirmed pre-existing and unrelated to this task by stashing this task's changes and
re-running `go vet ./...`, which reproduces the identical four findings on the unmodified base
branch. `go test ./...` passes in full (93 packages, all `ok`, including
`internal/store` and `internal/agentworkflow`).

No deviations from the task's own instructions beyond the real-backup-flex-row adaptation
documented above, and using `139` in place of the provisional `143`.

**Orchestrator fix, post-review (2026-08-21):** the fresh reviewer flagged one stale
doc-comment reference — `internal/agentworkflow/types.go`'s `StepKindLoop` comment still said
"migration 135" after the merge-time renumbering to `139` (every other reference was correctly
updated). Non-behavioral, one-line comment correction applied directly by the Orchestrator
rather than a full worker dispatch, given the fix was exactly specified and zero-risk;
`go build ./cmd/nanite/` re-confirmed clean after the edit. Left for the next reviewer pass to
confirm and mark `reviewed`, per this project's log-integrity discipline (only the reviewer
that actually checked it attests `reviewed`).

## Review notes

Pass. Re-checked the Orchestrator's post-review comment fix in
`internal/agentworkflow/types.go`: `StepKindLoop`'s doc comment now correctly cites migration
`139` (was `135`). Read the full `StepKind` const block end-to-end — no other stale migration
references or drift found. `go build ./cmd/nanite/` confirmed green. This is the first
reviewer sign-off on that specific fix, per this project's log-integrity discipline.
