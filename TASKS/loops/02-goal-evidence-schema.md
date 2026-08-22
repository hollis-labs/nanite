# Goal evidence schema — `goal_evidence` thin pointer table

**Phase:** 1 — Schema & storage foundation (`TASKS/loops`)
**Status:** reviewed
**Depends on:** `01-goals-schema.md` (`goal_id` FK)
**Touches:** `internal/store/migrations/` (new migration), `internal/store/goals.go` or a
new `internal/store/goal_evidence.go` (Go types, CRUD).

## Context

Implements `docs/engineering/architecture/21-loops.md`'s Goal-evidence model — a **thin
pointer table, not a duplicate content store**, quoted directly: *"It points into
`workflow_run_steps.verify_json`, a gate's resolution record, or an `event_log` row — the
same 'derived/observability, not new persistent state' discipline Teams applied to routing
provenance."* Goal satisfaction (`goal_met = acceptance_criteria_satisfied AND
constraints_satisfied AND invariants_preserved AND required_evidence_present`, design doc
§7) is computed by walking these pointers, not by trusting a bare status flag — task `07`'s
continuation-policy `decide()` function is the real consumer of this walk.

**This planning session's own decision on the design doc's open "free-text evidence"
question** (see this batch's README, "What this session decided..."): **no** — every row
points at something structured. `ref_table`/`ref_id` are both required (`NOT NULL`), never
a free-text-only row. A human's written acceptance note is captured as a real `event_log`
row first (`ref_table='event_log'`, `ref_id` = that row's ID) and pointed at from here —
`event_log` already exists as an append-only durable log (used by compaction/recovery
events, reflex firings) and is the natural home for a one-off text note, not a new nullable
free-text column on this table.

**Precedent template for this table's shape** — `internal/store/migrations/132_team_authority_grants.sql`
(the most recent "normalized sub-table pointing at a parent definition row" migration):
plain `CREATE TABLE`, one composite index, no rebuild dance.

## What to do

1. **New migration** — re-verify the actual next-available migration number at dispatch
   time (`ls internal/store/migrations/ | sort -t_ -k1 -n | tail -8`); this task
   provisionally claims `139` (after task `01`'s `138`) — **re-check, both cross-batch and
   against `01`'s own actually-landed number if it merged first.** Illustrative DDL:
   ```sql
   CREATE TABLE IF NOT EXISTS goal_evidence (
       id               TEXT PRIMARY KEY,
       goal_id          TEXT NOT NULL REFERENCES goals(id),
       loop_run_id      TEXT,
       iteration_number INTEGER,
       evidence_type    TEXT NOT NULL
                        CHECK (evidence_type IN ('test_suite','verify_result','gate_approval',
                               'human_acceptance','artifact')),
       ref_table        TEXT NOT NULL,
       ref_id           TEXT NOT NULL,
       result           TEXT,
       summary          TEXT,
       recorded_at      TEXT NOT NULL DEFAULT (datetime('now'))
   );
   CREATE INDEX idx_goal_evidence_goal ON goal_evidence(goal_id, evidence_type);
   CREATE INDEX idx_goal_evidence_loop_run ON goal_evidence(loop_run_id, iteration_number);
   ```
   `loop_run_id` is deliberately **not** a hard FK at this table's creation time (task `03`
   builds `loop_runs` in parallel with this task per the README's Wave 2 grouping — both
   depend on `01`, not on each other). If `03` lands first in practice, add the FK
   constraint (`REFERENCES loop_runs(id)`) here; if this task lands first, leave it a plain
   nullable `TEXT` and note the gap for whichever of `03`/`04` lands second to tighten, or
   accept it permanently loose (Goal evidence can exist independent of any `LoopRun` — a
   human-authored acceptance note against a `DEFINED` goal with no loop launched yet is a
   real, valid row with `loop_run_id IS NULL`). Document your actual call.

2. **Go types + store CRUD** — `GoalEvidence` struct mirroring the columns; `RecordGoalEvidence`,
   `ListGoalEvidence(goalID, filters...)` (at minimum: filter by `loop_run_id`, filter by
   `evidence_type`), `DeleteGoalEvidence` (rarely used — evidence is append-only in practice,
   but keep it symmetric with every other CRUD set in this store package). No `Update` —
   evidence rows are immutable once recorded, matching the append-only "one row per firing"
   discipline the design doc's own `loop_run_iterations` illustrative shape uses (task `04`).
   Go-layer `validateGoalEvidence`: `evidence_type` enum membership, `ref_table`/`ref_id`
   both non-empty (enforces the "always structured, never free-text-only" decision above).

3. **A real, callable evidence-walk helper belongs here, not deferred to task `07`** — a
   `EvidenceSatisfiesGoal(ctx, goalID string) (bool, []GoalEvidence, error)`-shaped function
   (exact signature is your call) that walks recorded evidence and reports whether
   `acceptance_criteria_satisfied AND constraints_satisfied AND invariants_preserved AND
   required_evidence_present` holds, per the design doc's §7 formula — this task builds the
   *query*, task `07` is the one that *calls* it inside `decide()`. Keep the four-clause
   evaluation logic in this package (`internal/store` or wherever `goal_evidence.go` lands)
   since it's a pure read over this table plus `goals`' own `acceptance_criteria_json`/
   `constraints_json`/`invariants_json` — no engine dependency needed to compute it.

## Done means

- Migration applies cleanly against a real backup copy of the database (isolated scratch
  path, per `EXECUTION-PROCESS.md`).
- `GoalEvidence` CRUD round-trips correctly, including a row with `loop_run_id IS NULL`
  (goal-only evidence, no loop yet) and a row with both `loop_run_id`/`iteration_number` set.
- `validateGoalEvidence` rejects a row missing `ref_table` or `ref_id`.
- `EvidenceSatisfiesGoal` (or equivalent) is unit-tested against at least: zero evidence
  (not satisfied), partial evidence covering only some acceptance criteria (not satisfied),
  and full coverage of all four clauses (satisfied) — using a real `Goal` row's
  `acceptance_criteria_json`/etc., not a hardcoded stub.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.

## Work log

**Migration number:** dispatch explicitly assigned `137` (not this task's own
re-verify-and-pick procedure) to avoid a repeat of Loops Wave 1's `01`/`06` both
independently landing on `135` (see `TASKS/ESCALATIONS.md`). Confirmed via `ls
internal/store/migrations/ | sort -t_ -k1 -n | tail -8` that `137_*.sql` did not already
exist and that `136_workflow_run_steps_loop_kind.sql` was the latest on disk (task `01`
actually landed as `135_goals.sql`, not the task file's own provisional `138`) — no
anomaly, `137` used exactly as assigned. Built `internal/store/migrations/137_goal_evidence.sql`
following `132_team_authority_grants.sql`'s precedent shape (plain `CREATE TABLE`, two
composite indexes, no rebuild dance — brand-new table).

**`loop_run_id` FK:** left as a plain nullable `TEXT` column with **no FK constraint**,
unconditionally, per this task's own dispatch instruction — not resolved by guessing task
`03`'s landing order. Documented in the migration's own doc comment as a deliberate,
order-independent choice; tightening it to `REFERENCES loop_runs(id)` once both tables are
known to coexist is a real follow-up for whichever later task/reviewer notices it, not this
task's job.

**Go layer** — `internal/store/goal_evidence.go`: `GoalEvidence` struct mirroring the table
1:1 (`IterationNumber *int64` nullable, following `agent_reflexes.go`'s
`RecurrenceOverrideSeconds`/`nullIfNilInt64` precedent — renamed `nullIfNilInt64Ptr` here
since `agent_reflexes.go` already owns the `nullIfNilInt64` name in this package).
`RecordGoalEvidence` (insert, append-only — no `UpdateGoalEvidence`), `GetGoalEvidence`,
`ListGoalEvidence(ctx, goalID, GoalEvidenceFilter{LoopRunID, EvidenceType})`,
`DeleteGoalEvidence` (symmetry only, rarely used in practice). `validateGoalEvidence`
enforces: `goal_id` non-empty, `evidence_type` enum membership (mirrors migration 137's own
CHECK, same "typed Go error before the DB round-trip" convention as
`team_authority.go`/`goals.go`), and `ref_table`/`ref_id` both non-empty — the "always
structured, never free-text-only" decision this task's Context section states as
load-bearing.

**Evidence-walk query** — `EvaluateGoalEvidence(ctx, goalID) (GoalEvidenceEvaluation, []GoalEvidence, error)`
plus the narrower `EvidenceSatisfiesGoal(ctx, goalID) (bool, []GoalEvidence, error)` shape
this task file's own "What to do" §3 names directly. Computes design doc §7's
`goal_met = acceptance_criteria_satisfied AND constraints_satisfied AND invariants_preserved
AND required_evidence_present` by reading `goal_evidence` alongside the parent `goals` row's
own `AcceptanceCriteria()`/`Constraints()`/`Invariants()` accessors (task `01`'s landed
helpers).

**Real design call this task had to make and the design doc left open — evidence-to-criterion
matching:** neither `21-loops.md` nor this table's fixed illustrative column shape names a
"which specific named criterion does this evidence address" column. To make "partial evidence
covering only some acceptance criteria" (a required test scenario) actually distinguishable
from "full coverage," I matched a `GoalEvidence` row to a specific criterion/constraint/
invariant string by **exact string equality against `GoalEvidence.Summary`**. This is a real,
load-bearing interpretation choice, documented in-code (`GoalEvidence.Summary`'s and
`EvaluateGoalEvidence`'s doc comments) rather than silently assumed — a future task wiring a
richer, non-exact-match evidence classifier (e.g. semantic matching, or a dedicated
`criterion_ref` column) is a real follow-up, not precluded by this shape.

**`required_evidence_present`** is treated as a genuinely separate fourth clause, not
redundant with the other three: true iff at least one `goal_evidence` row exists for the goal
at all, regardless of what it covers. This keeps the clause meaningful even for a goal whose
three JSON lists are all empty (which would otherwise vacuously satisfy the other three
clauses with zero evidence ever recorded).

**`Result` column semantics deliberately not consulted** by the evaluation — no `result`
vocabulary is locked anywhere in `21-loops.md`'s own "What this session did not decide" list
or this batch's README, so any matching evidence row counts as coverage regardless of its
`Result` value (`"pass"` vs `"fail"` aren't distinguished). Flagged in-code as a real
follow-up once a real Result vocabulary exists (task `07`/`08` territory), not built
speculatively here.

**GLOSSARY.md:** checked before adding any new name — no collision, and "Evidence" is
explicitly noted in `21-loops.md` as "not currently claimed elsewhere... safe to adopt." No
new entry added, matching the precedent of the structurally closest prior task
(`team_authority_grants`, `TASKS/teams/04-team-authority-schema.md`), which also didn't add a
GLOSSARY entry for its own new table.

**Testing:** `internal/store/goal_evidence_test.go` — full CRUD round-trip (including a
`loop_run_id IS NULL` row and a row with both `loop_run_id`/`iteration_number` set), FK
enforcement against `goals`, `validateGoalEvidence` rejection cases (missing `goal_id`,
invalid `evidence_type`, missing `ref_table`/`ref_id` individually and together), the DB-level
CHECK constraint via a raw bypass insert, and `EvidenceSatisfiesGoal`/`EvaluateGoalEvidence`
against zero evidence (not satisfied), partial acceptance-criteria coverage (not satisfied),
full four-clause coverage (satisfied), and an unknown `goal_id` (surfaces `ErrGoalNotFound`).
All against a real `Goal` row's JSON columns, not a hardcoded stub, per this task's own "Done
means." `internal/store/migration137_goal_evidence_backup_test.go` mirrors task `01`'s
`migration135_goals_backup_test.go` real-backup-DB pattern exactly — copies
`~/.local/share/nanite/workspaces/default/backups/main.db.pre-execution-backup-20260818-132726`
into an isolated `t.TempDir()` scratch path, confirms the migration applies cleanly alongside
343 real pre-existing `sessions` rows, then round-trips full `GoalEvidence` CRUD plus the
evidence-walk evaluation on top of the migrated schema. Ran and passed.

**Baseline checks:** `go build ./cmd/nanite/` — pass. `go vet ./...` — the only finding
(`internal/service/container.go`'s `stopReaper`/`stopRuntimeReaper` possible-context-leak
warnings) predates this task's changes, confirmed via `git log -1 -- internal/service/container.go`
(last touched by an unrelated Phase-6 commit) and `git status` (file untouched in this
worktree) — not introduced here. `go test ./...` — all packages pass, including
`internal/store` (25.2s) with every new test above green. Did not use `git stash`/`git stash
pop` at any point, per this task's explicit instruction.

## Review notes

Prior reviewer's substantive findings on this task's own diff (schema shape, FK usage,
evidence-walk formula, test coverage) stand — no re-review of that content performed here.
This pass confirmed the merged-tree `makeTestGoal` duplicate-declaration fix (commit
`d6da6efe`, which kept the definition in this file's `goal_evidence_test.go:22`):
`go vet ./internal/store/...` clean, `go test -count=1 ./internal/store/...` passes (15.5s,
non-cached), and `go test -count=1 ./...` passes across all 92 packages (non-cached,
exit-code verified, no `FAIL`/`panic` in output).
