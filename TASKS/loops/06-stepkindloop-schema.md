# `StepKindLoop` schema — widen `workflow_run_steps.kind` CHECK

**Phase:** 1 — Schema & storage foundation (`TASKS/loops`)
**Status:** not-started
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
<Worker fills this in as it goes: what was actually done, any deviation from plan and why,
anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
